package mudserver

import (
	"bytes"
	"fmt"
	"mud_server/internal/muddata"
	"strings"

	"github.com/gorilla/websocket"
)

type MudServer struct {
	playerList map[*MudPlayer]*MudPlayer
	//ticker *time.Ticker
	Broadcast chan(MudMessage)
	World *World
	Commands map[string]*MudCommand
}

/*
	Use CreateMudServer() to generate a new server instance
*/
func CreateMudServer() *MudServer {
	w := createWorld()

	server := &MudServer{
		playerList: make(map[*MudPlayer]*MudPlayer),
		Broadcast: make(chan MudMessage),
		World: w,
		Commands: GenerateCommandList(),
	}

	return server
}

func (ms *MudServer) AddConnection(ws_conn *websocket.Conn) *MudPlayer {
	mc := CreateMudPlayer(ws_conn,ms.World.getRoomByName("Start Room"))

	// Add MudPlayer to Server connectionList
	ms.playerList[mc] = mc

	fmt.Printf("Created new user.  Number of connected clients: %d\n", len(ms.playerList))

	mc.SendMessage("Welcome!  Please set your NICK.")

	return mc
}

func (ms *MudServer) GetPlayerByMudPlayer(mp *MudPlayer) *MudPlayer {
	return ms.playerList[mp]
}

func (ms *MudServer) GetPlayerByUsername(u string) *MudPlayer {
	for conn := range ms.playerList {
		if conn.Username == u {
			return conn
		}
	}

	return nil
}

func (ms *MudServer) DeleteConnection(mp *MudPlayer) {
	ms.playerList[mp].Conn.Close()
	delete(ms.playerList, mp)
	fmt.Printf("Deleted client.  Number of connected clients: %d\n",len(ms.playerList))

}

func (ms *MudServer) SendMessageToPlayer(mp *MudPlayer,message string) {
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

		// Build MudMessage
		messagefields := bytes.Fields(messagebytes)
		msg.Command = strings.ToUpper(string(messagefields[0]))
		msg.CommandParameter = string(bytes.Join(messagefields[1:],[]byte(" ")))
		msg.SenderPlayer = player

		// Ensure that the user has selected a valid username
		if player.Username == "" && msg.Command != "/NICK" {
			ms.SendMessageToPlayer(player,"User not created.  Please send NICK command to create.")
		} else if player.Username == "" && msg.Command == "/NICK" {
			if ms.GetPlayerByUsername(msg.CommandParameter) == nil {
				player.Username = msg.CommandParameter

				// TODO:  Remove this when we persist locations
				player.Location.UserMap[player.Username] = player
			
				player.SendMessage("You are now known as "+msg.CommandParameter+".\n")	
			} else {
				player.SendMessage("That username is already in use.  Please try another username.\n")
			}
		} else {
			ms.Broadcast <- msg
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
		msg := <-ms.Broadcast

		// Prepare variables for lookup & action
		player := ms.GetPlayerByMudPlayer(msg.SenderPlayer)
		command_name := msg.Command
		arguments := msg.CommandParameter

		// Translate aliases
		command_name, arguments = ms.processAliases(command_name, arguments)

		command, err := ms.getCommand(command_name)
		if err == muddata.ERR_COMMAND_UNKNOWN {
			ms.SendMessageToPlayer(player,"SERVER:  Unknown command.\n")
		} else {
			// Run command
			command.action(player, arguments)
		}

	}
}

func (ms *MudServer) processAliases (cn string, args string) (string, string) {
	command_name := cn
	arguments := args

	switch command_name {
	case "NORTH":
		command_name = "MOVE"
		arguments = "NORTH"
	case "SOUTH":
		command_name = "MOVE"
		arguments = "SOUTH"
	case "EAST":
		command_name = "MOVE"
		arguments = "EAST"
	case "WEST":
		command_name = "MOVE"
		arguments = "WEST"
	case "L":
		command_name = "LOOK"
	case "I": 
		command_name = "INVENTORY"
	}

	return command_name, arguments
}