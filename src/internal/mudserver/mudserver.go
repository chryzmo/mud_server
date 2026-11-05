package mudserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	muddatabase "mud_server/src/internal/database"
	"mud_server/src/internal/muddata"
	"mud_server/src/internal/mudutils"
	"os"
	"strings"
	"sync"
	"time"

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
		World:         createWorld(&db),
		Configuration: c,
		DB:            db,
		saves:         make(chan saveBatch, 1),
		saverDone:     make(chan struct{}),
		savingNames:   make(map[string]chan struct{}),
		sessions:      make(map[*Session]struct{}),
		done:          make(chan struct{}),
		loopDone:      make(chan struct{}),
	}
	server.Commands = GenerateCommandList(&server.DB, server.findPlayer)

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
			runEvent("world tick", func() { ms.World.tick(now) })
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
			s.Close()
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
		player.SendMessage("SERVER:  The server is restarting.  Reconnect in a moment.\n")
		final.players = append(final.players, player.snapshot())
	}
	for _, l := range ms.leaving {
		final.players = append(final.players, l.snapshot)
	}
	for _, room := range ms.World.RoomMap {
		final.rooms = append(final.rooms, room.snapshot())
	}

	// A batch may still be with the saver; the channel has room for this one
	// behind it, and the saver writes both before it finishes.
	ms.saves <- final
	close(ms.saves)
	<-ms.saverDone

	for _, player := range ms.players {
		player.Session.Close()
	}
}

func (ms *MudServer) AddConnection(ws_conn *websocket.Conn) *MudPlayer {
	session := NewSession(ws_conn, ms.Configuration.SessionSettings())

	stats := PlayerStartStats{
		HP:      ms.Configuration.Game.PlayerStartHP,
		MaxHP:   ms.Configuration.Game.PlayerStartMaxHP,
		MP:      ms.Configuration.Game.PlayerStartMP,
		MaxMP:   ms.Configuration.Game.PlayerStartMaxMP,
		Attack:  ms.Configuration.Game.PlayerStartAttack,
		Defense: ms.Configuration.Game.PlayerStartDefense,
	}
	mc := CreateMudPlayer(session, ms.World.RoomMap[1], stats)

	ms.sessionsMu.Lock()
	ms.sessions[session] = struct{}{}
	count := len(ms.sessions)
	ms.sessionsMu.Unlock()

	fmt.Printf("Client connection - number of connected clients: %d\n", count)

	mc.SendMessage("Welcome to the MUD!\nType /REGISTER <username> <password> to create an account.\nType /LOGIN <username> <password> to log in.\n")

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
// on the game loop. It returns false if the name is already online (someone
// else logged in first) or the server is shutting down.
func (ms *MudServer) admit(player *MudPlayer, greeting string) bool {
	admitted := false
	ms.onLoop(func() {
		if _, online := ms.players[player.Username]; online {
			return
		}
		admitted = true
		ms.players[player.Username] = player
		player.Location.AddPlayer(player)
		player.SendMessage(greeting)
		player.Location.Broadcast(mudutils.ColorString(player.Username, mudutils.COLOR_BLUE)+" has appeared in the room.", player.Username)
		c, _ := ms.getCommand("LOOK")
		c.action(player, "")
		player.SendStatUpdate()
	})
	return admitted
}

// disconnect removes a player from the world and queues their save. Loop only.
func (ms *MudServer) disconnect(player *MudPlayer) {
	if ms.players[player.Username] != player {
		player.Session.Close()
		return
	}
	delete(ms.players, player.Username)

	l := leavingPlayer{snapshot: player.snapshot(), saved: make(chan struct{})}
	ms.leaving = append(ms.leaving, l)
	ms.savingNames[player.Username] = l.saved
	ms.flushDirty() // save them now, not at the next interval

	player.Location.Broadcast(mudutils.ColorString(player.Username, mudutils.COLOR_BLUE)+" has poofed away.", player.Username)
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
ProcessInput is a connection's reader goroutine. Until the player logs in it
handles /REGISTER and /LOGIN itself; after that it queues each command on the
game loop. When the connection ends, it queues the player's disconnect.
*/
func (ms *MudServer) ProcessInput(player *MudPlayer) {
	admitted := false
	defer func() {
		if !admitted || !ms.enqueue(func() { ms.disconnect(player) }) {
			player.Session.Close()
		}
		ms.sessionsMu.Lock()
		delete(ms.sessions, player.Session)
		count := len(ms.sessions)
		ms.sessionsMu.Unlock()
		fmt.Printf("Client disconnect - number of connected clients: %d\n", count)
	}()
	defer recoverPanic("connection reader", nil)

	for {
		var msg MudMessage

		messagebytes, err := player.Session.ReadMessage()
		if err != nil {
			fmt.Println(err)
			return
		}

		// Limit every data message before parsing, DB lookups or bcrypt. This
		// includes pre-login input and blanks, so neither can bypass the limit.
		if !player.Session.allowCommand(time.Now()) {
			continue
		}

		// If message is blank, disregard it
		if len(messagebytes) == 0 {
			continue
		}

		// Build MudMessage
		messagefields := bytes.Fields(messagebytes)
		if len(messagefields) == 0 {
			continue
		}
		msg.Command = strings.ToUpper(string(messagefields[0]))
		msg.CommandParameter = string(bytes.Join(messagefields[1:], []byte(" ")))
		msg.SenderPlayer = player

		switch {
		case admitted:
			if !ms.enqueue(func() { ms.handleCommand(player, msg) }) {
				return
			}
		case msg.Command == "/REGISTER":
			admitted = ms.register(player, msg.CommandParameter)
		case msg.Command == "/LOGIN":
			admitted = ms.login(player, msg.CommandParameter)
		default:
			player.SendMessage("Please /LOGIN or /REGISTER first.\n")
		}
	}
}

// register runs on the connection's reader goroutine; the player isn't in the
// world yet, so the reader owns it until admit succeeds.
func (ms *MudServer) register(player *MudPlayer, params string) bool {
	parts := strings.SplitN(params, " ", 2)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		player.SendMessage("Usage: /REGISTER <username> <password>\n")
		return false
	}
	username, password := parts[0], parts[1]

	if online, _ := ms.isOnline(username); online {
		player.SendMessage("That username is already online.\n")
		return false
	}
	if player.CountUsernameFromDB(&ms.DB, username) > 0 {
		player.SendMessage("That username is already taken.\n")
		return false
	}

	player.Username = username
	if err := player.SetPassword(password); err != nil {
		fmt.Println("SetPassword error: " + err.Error())
		player.SendMessage("Registration failed. Please try again.\n")
		player.Username = ""
		return false
	}
	if err := player.InsertIntoDB(&ms.DB); err != nil {
		player.Username = ""
		if errors.Is(err, errUsernameTaken) {
			// Someone registered the same name between the check and the insert.
			player.SendMessage("That username is already taken.\n")
		} else {
			fmt.Println("Register insert error: " + err.Error())
			player.SendMessage("Registration failed. Please try again.\n")
		}
		return false
	}

	if !ms.admit(player, "Account created! Welcome, "+username+".\n") {
		player.SendMessage("That username is already online.\n")
		player.Username = ""
		return false
	}
	return true
}

// login runs on the connection's reader goroutine, like register.
func (ms *MudServer) login(player *MudPlayer, params string) bool {
	parts := strings.SplitN(params, " ", 2)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		player.SendMessage("Usage: /LOGIN <username> <password>\n")
		return false
	}
	username, password := parts[0], parts[1]

	online, saved := ms.isOnline(username)
	if online {
		player.SendMessage("That user is already logged in.\n")
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
		player.SendMessage("Unknown username. Use /REGISTER to create an account.\n")
		return false
	}

	if err := player.LoadFromDB(&ms.DB, ms.World, player, username); err != nil {
		fmt.Println("LoadFromDB error: " + err.Error())
		player.SendMessage("Login failed. Please try again.\n")
		return false
	}

	if !player.CheckPassword(password) {
		player.Username = ""
		player.SendMessage("Incorrect password.\n")
		return false
	}

	if !ms.admit(player, "Welcome back, "+username+"!\n") {
		player.Username = ""
		player.SendMessage("That user is already logged in.\n")
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

	// Translate aliases
	command_name, arguments := ms.processAliases(msg.Command, msg.CommandParameter)

	command, err := ms.getCommand(command_name)
	if err == muddata.ERR_COMMAND_UNKNOWN {
		player.SendMessage("SERVER:  Unknown command.\n")
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
