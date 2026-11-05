package mudserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	muddatabase "mud_server/src/internal/database"
	"mud_server/src/internal/muddata"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gorilla/websocket"
)

/*
The game loop (run) owns all game state: the online players, rooms, items,
NPCs and spawners. Nothing else reads or writes it. Connection readers turn
what clients send into closures queued on events, and the loop runs them one
at a time, along with world ticks and periodic saves. So game code needs no
locks, and a slow client can only slow its own reader.

Login is the exception that stays off the loop: the database lookup and
bcrypt check run on the connection's reader, and only admission (checking
the name isn't online, placing the player) runs on the loop. Saving is off
the loop too; see persist.go.
*/
type MudServer struct {
	players       map[string]*MudPlayer // online players by name; loop-owned
	events        chan func()
	World         *World
	Commands      map[string]*MudCommand
	Configuration Configuration
	DB            muddatabase.MudDatabase

	// Batched saving (persist.go); all loop-owned except the channels.
	saves       chan saveBatch
	saverDone   chan struct{}
	saving      bool                     // a batch is with the saver
	flushAgain  bool                     // a flush was skipped while saving
	leaving     []leavingPlayer          // disconnected, not yet in a batch
	savingNames map[string]chan struct{} // disconnected, save not yet written

	// Every open connection, logged in or not, so Shutdown can close them all.
	sessions   map[*Session]struct{}
	sessionsMu sync.Mutex
	closes     *closeStats // why sessions ended; see closestats.go
	names      *nameRules  // what guests may call themselves; see names.go

	// Loop-owned. Guests by the hash of their resume token (online or dormant),
	// and dormant guests with the time their grace period ends; see resume.go.
	byToken map[string]*MudPlayer
	dormant map[*MudPlayer]time.Time

	recoveries *recoveryGuard // wrong recovery codes per name; used by readers, has its own lock

	done      chan struct{} // closed by Shutdown
	loopDone  chan struct{} // closed when the game loop has exited
	closeOnce sync.Once
}

/*
Use CreateMudServer() to generate a new server instance
*/
func CreateMudServer() *MudServer {
	configFile := os.Getenv("MUD_CONFIG")
	if configFile == "" {
		configFile = "config/server_config.json"
	}

	c := Configuration{}
	c = c.LoadConfig(configFile)
	c.ApplyEnvOverrides()

	server, err := NewMudServer(c)
	if err != nil {
		fmt.Println("Startup failed: " + err.Error())
		os.Exit(1)
	}
	return server
}

/*
NewMudServer builds a server from an already-loaded Configuration and starts
its game loop. Tests use this directly to point at the test database.
*/
func NewMudServer(c Configuration) (*MudServer, error) {
	maxConns := c.Database.MaxConnections
	if maxConns <= 0 {
		// The saver uses one; the rest serve logins, which run on connection
		// readers and so can arrive all at once after a restart.
		maxConns = 10
	}
	db := muddatabase.CreateDatabase(c.DatabaseConnectionURL(), maxConns)

	// Fail fast rather than start with an empty world: loading rooms from an
	// unreachable database silently yields none.
	wait := time.Duration(c.Database.StartupWaitSeconds) * time.Second
	if wait <= 0 {
		wait = 30 * time.Second
	}
	if err := db.WaitUntilReachable(wait); err != nil {
		db.Connection.Close()
		return nil, errors.New("database unreachable: " + err.Error())
	}
	if err := db.EnsureSchema(); err != nil {
		fmt.Println("Schema update failed (continuing): " + err.Error())
	}

	server := &MudServer{
		players:       make(map[string]*MudPlayer),
		events:        make(chan func(), 4096),
		World:         createWorld(&db, c.Crowd),
		Configuration: c,
		DB:            db,
		saves:         make(chan saveBatch, 1),
		saverDone:     make(chan struct{}),
		savingNames:   make(map[string]chan struct{}),
		sessions:      make(map[*Session]struct{}),
		closes:        newCloseStats(),
		names:         defaultNameRules(),
		byToken:       make(map[string]*MudPlayer),
		dormant:       make(map[*MudPlayer]time.Time),
		done:          make(chan struct{}),
		loopDone:      make(chan struct{}),
	}
	server.recoveries = newRecoveryGuard(server.recoveryLock())
	server.Commands = GenerateCommandList(&server.DB, server.findPlayer)

	go server.closes.logPeriodically(server.done, 10*time.Second)
	go server.saver(server.saves, server.saverDone)
	go server.run()

	return server, nil
}

// run is the game loop.
func (ms *MudServer) run() {
	defer close(ms.loopDone)

	tickEvery := time.Duration(ms.Configuration.Game.RoomTickDurationInMS) * time.Millisecond
	if tickEvery <= 0 {
		tickEvery = 250 * time.Millisecond
	}
	tick := time.NewTicker(tickEvery)
	defer tick.Stop()

	saveEvery := time.Duration(ms.Configuration.Server.SaveIntervalMS) * time.Millisecond
	if saveEvery <= 0 {
		saveEvery = 5 * time.Second
	}
	save := time.NewTicker(saveEvery)
	defer save.Stop()

	for {
		select {
		case event := <-ms.events:
			runEvent("queued event", event)
		case now := <-tick.C:
			runEvent("world tick", func() {
				ms.World.tick(now)
				ms.expireDormant(now)
				ms.reclaimIdle(now)
			})
		case <-save.C:
			runEvent("periodic save", ms.flushDirty)
		case <-ms.done:
			ms.shutdownOnLoop()
			return
		}
	}
}

// enqueue queues f to run on the game loop. It returns false if the server is
// shutting down, in which case f will never run.
func (ms *MudServer) enqueue(f func()) bool {
	select {
	case <-ms.done:
		return false
	default:
	}
	select {
	case ms.events <- f:
		return true
	case <-ms.done:
		return false
	}
}

// onLoop runs f on the game loop and waits for it to finish. It returns false
// if the server shut down before f ran.
func (ms *MudServer) onLoop(f func()) bool {
	finished := make(chan struct{})
	if !ms.enqueue(func() { defer close(finished); f() }) {
		return false
	}
	select {
	case <-finished:
		return true
	case <-ms.loopDone:
		select {
		case <-finished:
			return true
		default:
			return false
		}
	}
}

/*
Shutdown stops the game loop, which warns, saves and disconnects every player
and saves every room; then closes any connections that never logged in, and
the database pool. Safe to call more than once; later calls wait for the first.
*/
func (ms *MudServer) Shutdown(ctx context.Context) {
	ms.closeOnce.Do(func() {
		close(ms.done)
		select {
		case <-ms.loopDone:
		case <-ctx.Done():
		}

		ms.sessionsMu.Lock()
		for s := range ms.sessions {
			s.CloseWith(closeShutdown)
		}
		ms.sessionsMu.Unlock()

		ms.DB.Connection.Close()
	})
}

// shutdownOnLoop is the game loop's last act: warn everyone, write one final
// batch with every player and room, wait for the saver, then disconnect.
func (ms *MudServer) shutdownOnLoop() {
	var final saveBatch
	for _, player := range ms.players {
		player.SendError(codeRestarting, "The server is restarting.  Reconnect in a moment.")
		final.players = append(final.players, player.snapshot())
	}
	for _, l := range ms.leaving {
		final.players = append(final.players, l.snapshot)
	}
	for _, room := range ms.World.RoomMap {
		if !room.clone {
			final.rooms = append(final.rooms, room.snapshot())
		}
	}

	// A batch may still be with the saver; the channel has room for this one
	// behind it, and the saver writes both before it finishes.
	ms.saves <- final
	close(ms.saves)
	<-ms.saverDone

	for _, player := range ms.players {
		player.Session.CloseWith(closeShutdown)
	}
}

// SessionCloseCounts reports how many sessions have ended, by reason: queue_full,
// write_error, ping_error, client_closed, read_timeout, read_limit, read_error,
// shutdown or server.
func (ms *MudServer) SessionCloseCounts() map[string]int64 { return ms.closes.Counts() }

func (ms *MudServer) AddConnection(ws_conn *websocket.Conn) *MudPlayer {
	session := NewSession(ws_conn, ms.Configuration.SessionSettings(), ms.closes)

	stats := PlayerStartStats{
		HP:      ms.Configuration.Game.PlayerStartHP,
		MaxHP:   ms.Configuration.Game.PlayerStartMaxHP,
		MP:      ms.Configuration.Game.PlayerStartMP,
		MaxMP:   ms.Configuration.Game.PlayerStartMaxMP,
		Attack:  ms.Configuration.Game.PlayerStartAttack,
		Defense: ms.Configuration.Game.PlayerStartDefense,
	}
	mc := CreateMudPlayer(session, stats)

	ms.sessionsMu.Lock()
	ms.sessions[session] = struct{}{}
	count := len(ms.sessions)
	ms.sessionsMu.Unlock()

	fmt.Printf("Client connection - number of connected clients: %d\n", count)

	session.Send(encode(helloMsg{T: msgHello, Message: "Welcome to the MUD!\nJoin with a name to play.\n"}))

	return mc
}

// findPlayer returns the online player with that name, or nil. Loop only.
func (ms *MudServer) findPlayer(username string) *MudPlayer {
	return ms.players[username]
}

// isOnline asks the game loop whether a player with that name is online. If
// they've just disconnected and their save isn't written yet, saved is a
// channel that closes once it is.
func (ms *MudServer) isOnline(username string) (online bool, saved <-chan struct{}) {
	ms.onLoop(func() {
		_, online = ms.players[username]
		if ch, ok := ms.savingNames[username]; ok {
			saved = ch
		}
	})
	return online, saved
}

// admit places a player who has just registered or logged in into the world,
// on the game loop, in the copy of their saved room that has space (see
// World.place). It returns "" when they are in, codeAtCapacity if every copy
// is full and no more can open, and codeNameOnline if the name is already
// online (someone else logged in first) or the server is shutting down.
func (ms *MudServer) admit(player *MudPlayer, hello welcome) string {
	refusal := codeNameOnline
	ms.onLoop(func() {
		if _, online := ms.players[player.Username]; online {
			return
		}
		room, ok := ms.World.place(player.savedRoomID)
		if !ok {
			refusal = codeAtCapacity
			return
		}
		refusal = ""
		player.Location = room
		player.rememberCopy()
		player.SpawnLocation = ms.World.templateRoom(player.savedSpawnID)
		player.world = ms.World
		player.lastActive = time.Now()
		if player.canResume() {
			player.replaySize = ms.replayFrames()
		}
		ms.players[player.Username] = player
		if player.tokenHash != "" {
			if ms.byToken == nil {
				ms.byToken = make(map[string]*MudPlayer)
			}
			ms.byToken[player.tokenHash] = player
		}
		player.Location.AddPlayer(player)
		player.SendWelcome(hello)
		player.Location.BroadcastFrame(encodeArrive(player.Username, ""), player.Username)
		c, _ := ms.getCommand("LOOK")
		c.action(player, "")
		player.SendStatUpdate()
	})
	return refusal
}

// disconnect removes a player from the world and queues their save. Loop only.
func (ms *MudServer) disconnect(player *MudPlayer) {
	if ms.players[player.Username] != player {
		player.Session.Close()
		return
	}
	delete(ms.players, player.Username)
	delete(ms.dormant, player)
	if player.tokenHash != "" {
		delete(ms.byToken, player.tokenHash)
	}

	l := leavingPlayer{snapshot: player.snapshot(), saved: make(chan struct{})}
	ms.leaving = append(ms.leaving, l)
	ms.savingNames[player.Username] = l.saved
	ms.flushDirty() // save them now, not at the next interval

	player.Location.BroadcastFrame(encodeLeave(player.Username, ""), player.Username)
	player.Location.RemovePlayer(player.Username)
	player.Session.Close()
}

func (ms *MudServer) getCommand(commandname string) (*MudCommand, int) {
	c, exists := ms.Commands[commandname]

	if !exists {
		return nil, muddata.ERR_COMMAND_UNKNOWN
	}
	return c, muddata.MUD_OK
}

/*
ProcessInput is a connection's reader goroutine. Until the player is admitted it
handles join, resume and login (and register) itself; after that it queues each
command on the game loop. When the connection ends, it queues what happens to the
player (a guest goes dormant; anyone else is removed).

The reader keeps its own session and never reads player.Session after admission:
a resume can hand the player to a newer connection, and the loop is the only
goroutine allowed to change which session a player has.
*/
func (ms *MudServer) ProcessInput(player *MudPlayer) {
	session := player.Session
	admitted := false
	defer func() {
		p := player
		if !admitted || !ms.enqueue(func() { ms.connectionEnded(p, session) }) {
			session.Close()
		}
		ms.sessionsMu.Lock()
		delete(ms.sessions, session)
		count := len(ms.sessions)
		ms.sessionsMu.Unlock()
		fmt.Printf("Client disconnect - number of connected clients: %d\n", count)
	}()
	defer recoverPanic("connection reader", nil)

	for {
		messagebytes, err := session.ReadMessage()
		if err != nil {
			fmt.Println(err)
			return
		}

		// Limit every data message before parsing, DB lookups or bcrypt. This
		// includes pre-login input and blanks, so neither can bypass the limit.
		if !session.allowCommand(time.Now()) {
			continue
		}

		var in inbound
		if err := json.Unmarshal(messagebytes, &in); err != nil || in.T == "" {
			session.sendError(codeBadFrame, "Messages must be JSON objects with a type: {\"t\":\"cmd\",\"line\":\"LOOK\"}.")
			continue
		}

		switch in.T {
		case msgCmd:
			if in.Line == nil {
				session.sendError(codeBadRequest, "A cmd needs {\"line\":\"...\"}.")
				continue
			}
			if !admitted {
				session.sendAuth(false, codeNotLoggedIn, "Please log in or register first.")
				continue
			}
			messagefields := strings.Fields(*in.Line)
			if len(messagefields) == 0 {
				continue
			}
			var msg MudMessage
			msg.Command = strings.ToUpper(messagefields[0])
			msg.CommandParameter = strings.Join(messagefields[1:], " ")
			p := player
			msg.SenderPlayer = p
			if !ms.enqueue(func() { ms.handleCommand(p, msg) }) {
				return
			}
		case msgJoin:
			if in.Name == "" {
				session.sendAuth(false, codeBadRequest, "A join needs a name.")
				continue
			}
			if admitted {
				session.sendError(codeAlreadyLoggedIn, "You are already in the game.")
				continue
			}
			admitted = ms.join(player, in.Name)
		case msgResume:
			if admitted {
				session.sendError(codeAlreadyLoggedIn, "You are already in the game.")
				continue
			}
			if resumed := ms.resume(player, session, in.Token, in.Received); resumed != nil {
				player = resumed
				admitted = true
			}
		case msgRecover:
			if admitted {
				session.sendError(codeAlreadyLoggedIn, "You are already in the game.")
				continue
			}
			if recovered := ms.recoverGuest(player, session, in.Name, in.Code); recovered != nil {
				player = recovered
				admitted = true
			}
		case msgRegister, msgLogin:
			if in.Username == "" || in.Password == "" {
				session.sendAuth(false, codeBadRequest, "A "+in.T+" needs a username and a password.")
				continue
			}
			if admitted {
				session.sendError(codeAlreadyLoggedIn, "You are already logged in.")
				continue
			}
			if strings.IndexFunc(in.Username, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
				session.sendAuth(false, codeInvalidUsername, "Usernames can't contain spaces or control characters.")
				continue
			}
			if in.T == msgRegister {
				admitted = ms.register(player, in.Username, in.Password)
			} else {
				admitted = ms.login(player, in.Username, in.Password)
			}
		default:
			session.sendError(codeBadRequest, "Unknown message type "+strconv.Quote(in.T)+".")
		}
	}
}

// register runs on the connection's reader goroutine; the player isn't in the
// world yet, so the reader owns it until admit succeeds.
func (ms *MudServer) register(player *MudPlayer, username, password string) bool {
	if online, _ := ms.isOnline(username); online {
		player.SendAuth(false, codeNameOnline, "That username is already online.")
		return false
	}
	if player.CountUsernameFromDB(&ms.DB, username) > 0 {
		player.SendAuth(false, codeNameTaken, "That username is already taken.")
		return false
	}

	player.Username = username
	if err := player.SetPassword(password); err != nil {
		fmt.Println("SetPassword error: " + err.Error())
		player.SendAuth(false, codeFailed, "Registration failed. Please try again.")
		player.Username = ""
		return false
	}
	if err := player.InsertIntoDB(&ms.DB); err != nil {
		player.Username = ""
		if errors.Is(err, errUsernameTaken) {
			// Someone registered the same name between the check and the insert.
			player.SendAuth(false, codeNameTaken, "That username is already taken.")
		} else {
			fmt.Println("Register insert error: " + err.Error())
			player.SendAuth(false, codeFailed, "Registration failed. Please try again.")
		}
		return false
	}

	if refusal := ms.admit(player, welcome{message: "Account created! Welcome, " + username + "."}); refusal != "" {
		if refusal == codeAtCapacity {
			player.SendAuth(false, refusal, "Your account was created, but the event is at capacity right now. Log in again in a moment.")
		} else {
			player.SendAuth(false, refusal, "That username is already online.")
		}
		player.Username = ""
		return false
	}
	return true
}

// login runs on the connection's reader goroutine, like register.
func (ms *MudServer) login(player *MudPlayer, username, password string) bool {
	online, saved := ms.isOnline(username)
	if online {
		player.SendAuth(false, codeNameOnline, "That user is already logged in.")
		return false
	}
	if saved != nil {
		// They just left; wait for that save so we load their latest state.
		select {
		case <-saved:
		case <-ms.done:
			return false
		}
	}
	if player.CountUsernameFromDB(&ms.DB, username) == 0 {
		player.SendAuth(false, codeUnknownUser, "Unknown username. Register to create an account.")
		return false
	}

	if err := player.LoadFromDB(&ms.DB, player, username); err != nil {
		fmt.Println("LoadFromDB error: " + err.Error())
		player.SendAuth(false, codeFailed, "Login failed. Please try again.")
		return false
	}

	if !player.CheckPassword(password) {
		player.Username = ""
		player.SendAuth(false, codeBadPassword, "Incorrect password.")
		return false
	}

	if refusal := ms.admit(player, welcome{message: "Welcome back, " + username + "!"}); refusal != "" {
		player.Username = ""
		if refusal == codeAtCapacity {
			player.SendAuth(false, refusal, "The event is at capacity right now. Please try again in a moment.")
		} else {
			player.SendAuth(false, refusal, "That user is already logged in.")
		}
		return false
	}
	return true
}

// handleCommand runs one player command. Loop only.
func (ms *MudServer) handleCommand(player *MudPlayer, msg MudMessage) {
	defer recoverPanic("player command", player)
	if ms.players[player.Username] != player {
		return // disconnected while the command was queued
	}
	player.lastActive = time.Now()

	// Translate aliases
	command_name, arguments := ms.processAliases(msg.Command, msg.CommandParameter)

	command, err := ms.getCommand(command_name)
	if err == muddata.ERR_COMMAND_UNKNOWN {
		player.SendError(codeUnknownCommand, "Unknown command.")
	} else {
		// Run command
		command.action(player, arguments)
	}
}

func (ms *MudServer) processAliases(cn string, args string) (string, string) {
	command_name := cn
	arguments := args

	switch command_name {
	case "NORTH", "N":
		command_name = "MOVE"
		arguments = "NORTH"
	case "SOUTH", "S":
		command_name = "MOVE"
		arguments = "SOUTH"
	case "EAST":
		command_name = "MOVE"
		arguments = "EAST"
	case "WEST", "W":
		command_name = "MOVE"
		arguments = "WEST"
	case "UP":
		command_name = "MOVE"
		arguments = "UP"
	case "DOWN":
		command_name = "MOVE"
		arguments = "DOWN"
	case "L":
		command_name = "LOOK"
	case "I", "INV":
		command_name = "INVENTORY"
	}

	return command_name, arguments
}
