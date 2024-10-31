package mudserver

import (
	//muddatabase "mud_server/src/internal/database"
	"errors"
	"fmt"
	"math/rand"
	muddatabase "mud_server/src/internal/database"
	mudutils "mud_server/src/internal/mudutils"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type Room struct {
	ID int32
	DB *muddatabase.MudDatabase
	Name string
	Description string
	ExitMap map[string]*Room
	RoomBroadcastChannel chan(RoomMessage)
	UserMap map[string]*MudPlayer
	BaddieMap map[string]*BaddieNPC
	Inventory *Inventory
	ExitJSON string
	ticker *time.Ticker
	tickDurationInMS int32
	GlobalWhisperChannel chan(MudMessage)
}

type RoomDB struct {
	ID int32
	Name string
	Description string
	ExitJSON string
	InventoryIDSlice map[string]int32
}

type RoomExits struct {
	N int32 `json:"N"`
	S int32 `json:"S"`
	E int32 `json:"E"`
	W int32 `json:"W"`
	U int32 `json:"U"`
	D int32 `json:"D"`
}

func CreateRoom(name string, description string, tickdur int32, whisperchan chan(MudMessage)) (*Room) {
	r := &Room{
		Name: name,
		Description: description,
		RoomBroadcastChannel: make(chan RoomMessage,5),
		UserMap: make(map[string]*MudPlayer),
		ExitMap: make(map[string]*Room),
		Inventory: CreateInventory(),
		BaddieMap: make(map[string]*BaddieNPC),
		tickDurationInMS: tickdur,
		GlobalWhisperChannel: whisperchan,
	}

	// Launch goroutine to continuously process broadcast messages
	go r.ProcessBroadcast()
	go r.RunTicker()

	return r
}

func (room *Room) RunTicker () {
	room.ticker = time.NewTicker(time.Millisecond * time.Duration(room.tickDurationInMS))

	for {
		for range room.ticker.C {
			for _,npc := range room.BaddieMap {
				npc.TurnAction(room,npc)
			}
		}
	}
}

func (r Room) ProcessBroadcast() {
	for {
		msg := <-r.RoomBroadcastChannel

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

	r.RoomBroadcastChannel <- roommsg
}

func (r Room) LookDescription() (string) {
	desc := "You are in '"+r.Name+"'\n"+r.Description+"\n"
	
	if len(r.BaddieMap) > 0 {
		desc += "\nThe monsters here are:\n"

		for _,baddie := range r.BaddieMap {
			desc += mudutils.ColorString(baddie.Name,mudutils.COLOR_RED)+"\n"
		}
	}

	if len(r.UserMap) > 0 {
		desc += "\nThe players here are:\n"

		for k := range r.UserMap {
			desc += mudutils.ColorString(k,mudutils.COLOR_BLUE)+"\n"
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
	
	// Set up loot for victorious MudPlayer
	if r.UserMap[attackername] != nil {
		if r.UserMap[attackername].NPCTarget.LootGoldMax > 0 {
			i := Item{}
			gploot_quantity := rand.Int31n(r.BaddieMap[strings.ToUpper(npcname)].LootGoldMax-r.BaddieMap[strings.ToUpper(npcname)].LootGoldMin) + r.BaddieMap[strings.ToUpper(npcname)].LootGoldMin
			msg := fmt.Sprint("You receive ",gploot_quantity," gold.\n")
			r.UserMap[attackername].SendMessage(msg)			
			r.UserMap[attackername].Inventory.AddItemByQuantity(i.FetchFromDB(r.DB,"GOLD"),gploot_quantity)
		}
	}
	delete(r.BaddieMap,strings.ToUpper(npcname))
	r.Broadcast(attackername+" kills "+npcname+"!",attackername)
}

// Remove NPC and broadcast kill
func (r *Room) KillPC(player *MudPlayer, attackername string) {
	r.Broadcast(attackername+" kills "+player.Username+"!",player.Username)
	player.SendMessage("You were killed by "+attackername+"!")
	player.Respawn()
}

func (r *Room) AddPlayer(mp *MudPlayer) {
	r.UserMap[mp.Username] = mp
}

func (r *Room) FetchFromDB (db *muddatabase.MudDatabase, tickdur int32, roomid interface{}) (*Room) {
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
			DB: db,
			Name: ret.Name,
			Description: ret.Description,
			Inventory: CreateInventory(),
			RoomBroadcastChannel: make(chan RoomMessage,5),
			UserMap: make(map[string]*MudPlayer),
			ExitMap: make(map[string]*Room),
			BaddieMap: make(map[string]*BaddieNPC),
			ExitJSON: ret.ExitJSON,
			tickDurationInMS: tickdur,
			GlobalWhisperChannel: r.GlobalWhisperChannel,
		}

		// Populate room inventory
		for iid,_ := range ret.InventoryIDSlice {
			i := Item{}
			sid,_ := strconv.ParseInt(iid[1:],10,32)
			item_id := int32(sid)
			
			room.Inventory.AddItemByQuantity(i.FetchFromDB(db,item_id),ret.InventoryIDSlice[iid])
		}

		go room.ProcessBroadcast()
		go room.RunTicker()
	}

	return &room
}

func (r *Room) WriteToDB (db *muddatabase.MudDatabase) {
	// User exists, do UPDATE
	query := "UPDATE room SET name=@name, description=@description, exitjson=@exitjson, inventoryidslice=@inventoryidslice WHERE id=@roomid;"

	// Build ExitJSON

	args := pgx.NamedArgs{
		"name": r.Name,
		"description": r.Description,
		"exitjson": r.ExitJSON,
		"inventoryidslice": r.Inventory.IDMap(),
		"roomid": r.ID,
	  }
	
	  db.ExecWithArgs(query,args)
}
