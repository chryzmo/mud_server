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

type MudServer struct {
	playerList  map[*MudPlayer]*MudPlayer
	playerMu    sync.RWMutex // guards playerList; connections add and remove players concurrently
	Broadcast   chan MudMessage
	WhisperChan chan MudMessage
	World       *World
	Commands    map[string]*MudCommand
	Configuration Configuration
	DB          muddatabase.MudDatabase
	done        chan struct{} // closed by Shutdown; every server goroutine exits when it closes
	closeOnce   sync.Once
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
	NewMudServer builds a server from an already-loaded Configuration.  Tests use
	this directly to point at the test database.
*/
func NewMudServer(c Configuration) (*MudServer, error) {
	db := muddatabase.CreateDatabase(c.DatabaseConnectionURL())

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

	done := make(chan struct{})
	serverWhisperChannel := make(chan MudMessage)
	w := createWorld(&db, c.Game.RoomTickDurationInMS, serverWhisperChannel, done)

	server := &MudServer{
		playerList:    make(map[*MudPlayer]*MudPlayer),
		Broadcast:     make(chan MudMessage),
		WhisperChan:   serverWhisperChannel,
		World:         w,
		Commands:      GenerateCommandList(&db),
		Configuration: c,
		DB:            db,
		done:          done,
	}

	go server.ProcessGlobalWhispers()

	return server, nil
}

/*
	Shutdown stops every server goroutine, saves and disconnects every player,
	saves every room, and closes the database pool.  Safe to call more than once.
*/
func (ms *MudServer) Shutdown(ctx context.Context) {
	ms.closeOnce.Do(func() {
		close(ms.done)

		ms.playerMu.RLock()
		players := make([]*MudPlayer, 0, len(ms.playerList))
		for _, player := range ms.playerList {
			players = append(players, player)
		}
		ms.playerMu.RUnlock()

		for _, player := range players {
			if ctx.Err() != nil {
				break
			}
			player.SendMessage("SERVER:  The server is restarting.  Reconnect in a moment.\n")
			ms.DeleteConnection(player)
		}

		ms.World.WriteWorldToDB(&ms.DB)
		ms.DB.Connection.Close()
	})
}

func (ms *MudServer) AddConnection(ws_conn *websocket.Conn) *MudPlayer {
	stats := PlayerStartStats{
		HP:      ms.Configuration.Game.PlayerStartHP,
		MaxHP:   ms.Configuration.Game.PlayerStartMaxHP,
		MP:      ms.Configuration.Game.PlayerStartMP,
		MaxMP:   ms.Configuration.Game.PlayerStartMaxMP,
		Attack:  ms.Configuration.Game.PlayerStartAttack,
		Defense: ms.Configuration.Game.PlayerStartDefense,
	}
	mc := CreateMudPlayer(ws_conn, ms.World.RoomMap[1], stats)

	// Add MudPlayer to Server connectionList
	ms.playerMu.Lock()
	ms.playerList[mc] = mc
	count := len(ms.playerList)
	ms.playerMu.Unlock()

	fmt.Printf("Client connection - number of connected clients: %d\n", count)

	mc.SendMessage("Welcome to the MUD!\nType /REGISTER <username> <password> to create an account.\nType /LOGIN <username> <password> to log in.\n")

	return mc
}

func (ms *MudServer) GetPlayerByMudPlayer(mp *MudPlayer) *MudPlayer {
	ms.playerMu.RLock()
	defer ms.playerMu.RUnlock()
	return ms.playerList[mp]
}

func (ms *MudServer) GetPlayerByUsername(u string) *MudPlayer {
	ms.playerMu.RLock()
	defer ms.playerMu.RUnlock()
	for conn := range ms.playerList {
		if conn.Username == u {
			return conn
		}
	}

	return nil
}

func (ms *MudServer) DeleteConnection(mp *MudPlayer) {
	// Remove the player first so only one caller does the cleanup: a failed send
	// and the read loop can both notice the same dead connection.
	ms.playerMu.Lock()
	if _, ok := ms.playerList[mp]; !ok {
		ms.playerMu.Unlock()
		return
	}
	delete(ms.playerList, mp)
	count := len(ms.playerList)
	ms.playerMu.Unlock()

	if mp.Username != "" {
		mp.WriteToDB(&ms.DB)
		mp.Location.Broadcast(mudutils.ColorString(mp.Username, mudutils.COLOR_BLUE)+" has poofed away.", mp.Username)
		mp.Location.RemovePlayer(mp.Username)
	}

	mp.Conn.Close()
	fmt.Printf("Client disconnect - number of connected clients: %d\n", count)
}

func (ms *MudServer) SendMessageToPlayer(mp *MudPlayer, message string) {
	err := mp.SendMessage(message)

	if err != nil {
		fmt.Println(err)
		ms.DeleteConnection(mp)
	}
}

func (ms *MudServer) getCommand(commandname string) (*MudCommand, int) {
	c, exists := ms.Commands[commandname]

	if !exists {
		return nil, muddata.ERR_COMMAND_UNKNOWN
	}
	return c, muddata.MUD_OK
}

func (ms *MudServer) ProcessGlobalWhispers() {
	for {
		var whisperMUDMessage MudMessage
		select {
		case whisperMUDMessage = <-ms.WhisperChan:
		case <-ms.done:
			return
		}

		targetPlayer := ms.GetPlayerByUsername(whisperMUDMessage.CommandTarget)
		messageContent := whisperMUDMessage.CommandParameter
		senderUsername := whisperMUDMessage.SenderPlayer.Username

		if targetPlayer == nil {
			whisperMUDMessage.SenderPlayer.SendMessage("Player '" + whisperMUDMessage.CommandTarget + "' is not online.\n")
			continue
		}

		msg := mudutils.ColorString(senderUsername+" whispers, \""+messageContent+"\"", mudutils.COLOR_MAGENTA)
		targetPlayer.SendMessage(msg)
	}
}

/*
	Server goroutine to continuously process user input being received from websocket.
	Queue the input in ms.Broadcast channel
*/
func (ms *MudServer) ProcessInput(player *MudPlayer) {
	for {
		var msg MudMessage

		_, messagebytes, err := player.Conn.ReadMessage()

		// Clean up the MudConn on error
		if err != nil {
			fmt.Println(err)
			ms.DeleteConnection(player)
			return
		}

		// If message is blank, disregard it
		if len(messagebytes) == 0 {
			continue
		}

		// Build MudMessage
		messagefields := bytes.Fields(messagebytes)
		msg.Command = strings.ToUpper(string(messagefields[0]))
		msg.CommandParameter = string(bytes.Join(messagefields[1:], []byte(" ")))
		msg.SenderPlayer = player

		// Require login before processing game commands.
		if player.Username == "" && msg.Command != "/LOGIN" && msg.Command != "/REGISTER" {
			ms.SendMessageToPlayer(player, "Please /LOGIN or /REGISTER first.\n")

		} else if player.Username == "" && msg.Command == "/REGISTER" {
			parts := strings.SplitN(msg.CommandParameter, " ", 2)
			if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
				player.SendMessage("Usage: /REGISTER <username> <password>\n")
				continue
			}
			username, password := parts[0], parts[1]

			if ms.GetPlayerByUsername(username) != nil {
				player.SendMessage("That username is already online.\n")
				continue
			}
			if player.CountUsernameFromDB(&ms.DB, username) > 0 {
				player.SendMessage("That username is already taken.\n")
				continue
			}

			player.Username = username
			if err := player.SetPassword(password); err != nil {
				fmt.Println("SetPassword error: " + err.Error())
				player.SendMessage("Registration failed. Please try again.\n")
				player.Username = ""
				continue
			}
			player.Location.AddPlayer(player)
			player.WriteToDB(&ms.DB)
			player.ID = player.GetIDFromDB(&ms.DB)

			player.SendMessage("Account created! Welcome, " + username + ".\n")
			player.Location.Broadcast(mudutils.ColorString(player.Username, mudutils.COLOR_BLUE)+" has appeared in the room.", player.Username)
			c, _ := ms.getCommand("LOOK")
			c.action(player, "")
			player.SendStatUpdate()

		} else if player.Username == "" && msg.Command == "/LOGIN" {
			parts := strings.SplitN(msg.CommandParameter, " ", 2)
			if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
				player.SendMessage("Usage: /LOGIN <username> <password>\n")
				continue
			}
			username, password := parts[0], parts[1]

			if ms.GetPlayerByUsername(username) != nil {
				player.SendMessage("That user is already logged in.\n")
				continue
			}
			if player.CountUsernameFromDB(&ms.DB, username) == 0 {
				player.SendMessage("Unknown username. Use /REGISTER to create an account.\n")
				continue
			}

			if err := player.LoadFromDB(&ms.DB, ms.World, player, username); err != nil {
				fmt.Println("LoadFromDB error: " + err.Error())
				player.SendMessage("Login failed. Please try again.\n")
				continue
			}

			if !player.CheckPassword(password) {
				player.Username = ""
				player.SendMessage("Incorrect password.\n")
				continue
			}

			player.Location.AddPlayer(player)
			player.SendMessage("Welcome back, " + username + "!\n")
			player.Location.Broadcast(mudutils.ColorString(player.Username, mudutils.COLOR_BLUE)+" has appeared in the room.", player.Username)
			c, _ := ms.getCommand("LOOK")
			c.action(player, "")
			player.SendStatUpdate()

		} else {
			select {
			case ms.Broadcast <- msg:
			case <-ms.done:
				return
			}
		}
	}
}

/*
	Evaluate messages, commands, etc. which are queued in ms.Broadcast
	Take action based on input (chat message, command, etc.)
*/
func (ms *MudServer) HandleMessages() {
	for {
		// Pop message off queue
		var msg MudMessage
		select {
		case msg = <-ms.Broadcast:
		case <-ms.done:
			return
		}

		// Prepare variables for lookup & action
		player := ms.GetPlayerByMudPlayer(msg.SenderPlayer)
		command_name := msg.Command
		arguments := msg.CommandParameter

		// Translate aliases
		command_name, arguments = ms.processAliases(command_name, arguments)

		command, err := ms.getCommand(command_name)
		if err == muddata.ERR_COMMAND_UNKNOWN {
			ms.SendMessageToPlayer(player, "SERVER:  Unknown command.\n")
		} else {
			// Run command
			command.action(player, arguments)
		}
	}
}

/*
	Periodic world/player save goroutine.
*/
func (ms *MudServer) RunMaintenanceactions() {
	maintenanceTicker := time.NewTicker(time.Minute * time.Duration(ms.Configuration.Server.WorldSaveDelayMinutes))

	defer maintenanceTicker.Stop()

	for {
		select {
		case <-maintenanceTicker.C:
		case <-ms.done:
			return
		}

		// Save all MudPlayer states to DB.  Iterate over a snapshot: a failed send
		// calls DeleteConnection, which needs the lock.
		ms.playerMu.RLock()
		players := make([]*MudPlayer, 0, len(ms.playerList))
		for _, player := range ms.playerList {
			players = append(players, player)
		}
		ms.playerMu.RUnlock()

		for _, player := range players {
			ms.SendMessageToPlayer(player, "SERVER:  World saving to DB.")
			player.WriteToDB(&ms.DB)
		}

		// Save all Room states to DB
		ms.World.WriteWorldToDB(&ms.DB)
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
