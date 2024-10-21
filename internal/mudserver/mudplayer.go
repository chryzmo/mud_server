package mudserver

import (
	"fmt"
	muddata "mud_server/internal/muddata"
	"strings"
	"sync"
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
	AttackPower int
	Defense int
	NPCTarget *BaddieNPC
	send_mutex sync.Mutex
}

func CreateMudPlayer(ws_conn *websocket.Conn, room *Room) (*MudPlayer) {
	return &MudPlayer{
		Conn: ws_conn,
		TimeConnected: time.Now(),
		Location: room,
		Inventory: CreateInventory(),
		AttackPower: 99,
		Defense: 0,
	}
}

func (mp *MudPlayer) SendMessage(message string) (error){
	mp.send_mutex.Lock()
	defer mp.send_mutex.Unlock()
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

	if mp.Location.ExitMap[dir] == nil {
		// Room does not exist
		retcode = muddata.ERR_ROOM_DNE
	} else {
		// Clean up location info in old room, set new room
		mp.Location.RemovePlayer(mp.Username)
		mp.Location.Broadcast(mp.Username+" has left to the "+dir+".",mp.Username)

		// Set new location for Player
		newroom := mp.Location.ExitMap[dir]
		mp.Location = newroom
		mp.Location.Broadcast(mp.Username+" has entered the room from the "+dir+".",mp.Username)
		mp.Location.AddPlayer(mp)
	}

	return retcode
}

