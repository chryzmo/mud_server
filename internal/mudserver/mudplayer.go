package mudserver

import (
	"fmt"
	muddata "mud_server/internal/muddata"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type MudPlayer struct {
	Conn	*websocket.Conn
	TimeConnected	time.Time
	State int8
	Location *Room
	Username string
	Inventory *Inventory
}

func CreateMudPlayer(ws_conn *websocket.Conn, room *Room) (*MudPlayer) {
	return &MudPlayer{
		Conn: ws_conn,
		TimeConnected: time.Now(),
		Location: room,
		Inventory: CreateInventory(),
	}
}

func (mp *MudPlayer) SendMessage(message string) (error){
	err := mp.Conn.WriteMessage(websocket.TextMessage, []byte(message))

	if err != nil {
		fmt.Println(err)
	}
	return err
}

func (mp *MudPlayer) MoveRoom (dir string) int {
	retcode := muddata.MUD_OK

	// Standardize direction
	if strings.ToUpper(dir) == "NORTH"{
		dir = "N"
	}
	if strings.ToUpper(dir) == "SOUTH"{
		dir = "S"
	}
	if strings.ToUpper(dir) == "EAST"{
		dir = "E"
	}
	if strings.ToUpper(dir) == "WEST"{
		dir = "W"
	}

	if mp.Location.ExitList[dir] == nil {
		// Room does not exist
		retcode = muddata.ERR_ROOM_DNE
	} else {
		// Clean up location info in old room, set new room
		mp.Location.RemovePlayer(mp.Username)

		// Set new location for Player
		newroom := mp.Location.ExitList[dir]
		mp.Location = newroom
		mp.Location.AddPlayer(mp)
	}

	return retcode
}

