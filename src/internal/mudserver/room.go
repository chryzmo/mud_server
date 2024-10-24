package mudserver

import (
	//muddatabase "mud_server/src/internal/database"
	"errors"
	"fmt"
	muddatabase "mud_server/src/internal/database"
	"strings"

	"github.com/jackc/pgx/v5"
)

/*
	UsersInZone key is Player.Username
	ExitList key is <direction> of exit
*/

type Room struct {
	ID int32
	Name string
	Description string
	ExitMap map[string]*Room
	RoomChannel chan(RoomMessage)
	UserMap map[string]*MudPlayer
	BaddieMap map[string]*BaddieNPC
	Inventory *Inventory
	ExitJSON string
}

type RoomDB struct {
	ID int32
	Name string
	Description string
	ExitJSON string
	InventoryIDSlice []int32
}

type RoomExits struct {
	N int32 `json:"N"`
	S int32 `json:"S"`
	E int32 `json:"E"`
	W int32 `json:"W"`
}

func CreateRoom(name string, description string) (*Room) {
	r := &Room{
		Name: name,
		Description: description,
		RoomChannel: make(chan RoomMessage,5),
		UserMap: make(map[string]*MudPlayer),
		ExitMap: make(map[string]*Room),
		Inventory: CreateInventory(),
		BaddieMap: make(map[string]*BaddieNPC),
	}

	// Launch goroutine to continuously process broadcast messages
	go r.ProcessBroadcast()

	return r
}

/*func (room *Room) RunTicker () {
	room.ticker = time.NewTicker(time.Millisecond * 250) //TODO:  Server config?

	for range room.ticker.C {
		// check if RoomChannel is not empty
			// Perform action (broadcast)
	}
}*/

func (r Room) ProcessBroadcast() {
	for {
		msg := <-r.RoomChannel

		for _,player := range r.UserMap {
			if player.Username != msg.SenderUsername {
				player.SendMessage(msg.CommandParameter)
			}
		}
	}
}

func (r Room) Broadcast(msg string, senderusername string) () {
	roommsg := RoomMessage{
		Command: "BROADCAST",
		CommandParameter: msg,
		SenderUsername: senderusername,
	}

	r.RoomChannel <- roommsg
}

func (r Room) LookDescription() (string) {
	desc := "You are in '"+r.Name+"'.\n\n"+r.Description+"\n"
	
	if len(r.BaddieMap) > 0 {
		desc += "\nThe monsters here are:\n"

		for _,baddie := range r.BaddieMap {
			desc += baddie.Name+"\n"
		}
	}

	if len(r.UserMap) > 0 {
		desc += "\nThe players here are:\n"

		for k := range r.UserMap {
			desc += k+"\n"
		}
	}

	if !r.Inventory.isEmpty() {
		desc += "\nYou see:\n"
		desc += r.Inventory.ListAsString(false)
	}

	desc += "\n"

	return desc
}

func (r *Room) RemovePlayer(username string) {
	delete(r.UserMap,username)
}

// Remove NPC and broadcast kill
func (r *Room) KillNPC(npcname string, attackername string) {
	delete(r.BaddieMap,strings.ToUpper(npcname))
	r.Broadcast(attackername+" kills "+npcname+"!",attackername)
}

func (r *Room) AddPlayer(mp *MudPlayer) {
	r.UserMap[mp.Username] = mp
}

func (r *Room) FetchFromDB (db muddatabase.MudDatabase, roomid interface{}) (*Room) {
	query := ""
	var err error
	room := Room{}

	switch roomid.(type) {
	case string:
		query = "SELECT id,name,description,exitjson,inventoryidslice FROM room WHERE name=$1"
	case int, int8, int32, int64:
		query = "SELECT id,name,description,exitjson,inventoryidslice FROM room WHERE id=$1"
	default:
		err = errors.New("Room.FetchFromDB invalid ID.  ID must be string|int32")
	}

	rows,err := db.Connection.Query(db.Context,query,roomid)

	if err != nil {
		fmt.Println("Room error: "+err.Error())
	}

	ret,err := pgx.CollectOneRow(rows,pgx.RowToStructByName[RoomDB])

	if err != nil {
		fmt.Println("Room error: "+err.Error())
	}

	// Build Room from RoomDB struct
	if err == nil {
		room = Room{
			ID: ret.ID,
			Name: ret.Name,
			Description: ret.Description,
			Inventory: CreateInventory(),
			RoomChannel: make(chan RoomMessage,5),
			UserMap: make(map[string]*MudPlayer),
			ExitMap: make(map[string]*Room),
			BaddieMap: make(map[string]*BaddieNPC),
			ExitJSON: ret.ExitJSON,
		}

		// Populate room inventory
		for _,item_id := range ret.InventoryIDSlice {
			i := Item{}
			room.Inventory.AddItem(i.FetchFromDB(db,item_id))
		}

		go room.ProcessBroadcast()
	}

	return &room
}
