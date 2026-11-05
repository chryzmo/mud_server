package mudserver

import (
	"errors"
	"fmt"
	"math/rand"
	muddatabase "mud_server/src/internal/database"
	mudutils "mud_server/src/internal/mudutils"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

type Room struct {
	ID                   int32
	DB                   *muddatabase.MudDatabase
	Name                 string
	Description          string
	ExitMap              map[string]*Room
	RoomBroadcastChannel chan RoomMessage
	UserMap              map[string]*MudPlayer
	BaddieMap            map[string]*BaddieNPC
	Inventory            *Inventory
	ExitJSON             string
	ticker               *time.Ticker
	tickDurationInMS     int32
	GlobalWhisperChannel chan MudMessage
	done                 <-chan struct{} // the server's shutdown signal
	userMu               sync.RWMutex
	baddieMu             sync.RWMutex
}

type RoomDB struct {
	ID               int32
	Name             string
	Description      string
	ExitJSON         string
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

func CreateRoom(name string, description string, tickdur int32, whisperchan chan MudMessage) *Room {
	r := &Room{
		Name:                 name,
		Description:          description,
		RoomBroadcastChannel: make(chan RoomMessage, 5),
		UserMap:              make(map[string]*MudPlayer),
		ExitMap:              make(map[string]*Room),
		Inventory:            CreateInventory(),
		BaddieMap:            make(map[string]*BaddieNPC),
		tickDurationInMS:     tickdur,
		GlobalWhisperChannel: whisperchan,
	}

	go r.ProcessBroadcast()
	go r.RunTicker()

	return r
}

func (room *Room) RunTicker() {
	room.ticker = time.NewTicker(time.Millisecond * time.Duration(room.tickDurationInMS))
	defer room.ticker.Stop()

	for {
		select {
		case <-room.ticker.C:
		case <-room.done:
			return
		}

		// Snapshot BaddieMap so TurnAction (which may call KillNPC) can safely
		// acquire the write lock without deadlocking the ticker.
		room.baddieMu.RLock()
		npcs := make([]*BaddieNPC, 0, len(room.BaddieMap))
		for _, npc := range room.BaddieMap {
			npcs = append(npcs, npc)
		}
		room.baddieMu.RUnlock()

		for _, npc := range npcs {
			npc.TurnAction(room, npc)
		}
	}
}

func (r *Room) ProcessBroadcast() {
	for {
		var msg RoomMessage
		select {
		case msg = <-r.RoomBroadcastChannel:
		case <-r.done:
			return
		}

		r.userMu.RLock()
		for _, player := range r.UserMap {
			if player.Username != msg.SenderUsername {
				player.SendMessage(msg.CommandParameter)
			}
		}
		r.userMu.RUnlock()
	}
}

func (r *Room) Broadcast(msg string, senderusername string) {
	roommsg := RoomMessage{
		Command:          "BROADCAST",
		CommandParameter: msg,
		SenderUsername:   senderusername,
	}
	select {
	case r.RoomBroadcastChannel <- roommsg:
	case <-r.done:
	}
}

func (r *Room) LookDescription() string {
	desc := "You are in '" + r.Name + "'\n" + r.Description + "\n"

	r.baddieMu.RLock()
	if len(r.BaddieMap) > 0 {
		desc += "\nThe monsters here are:\n"
		for _, baddie := range r.BaddieMap {
			desc += mudutils.ColorString(baddie.Name, mudutils.COLOR_RED) + "\n"
		}
	}
	r.baddieMu.RUnlock()

	r.userMu.RLock()
	if len(r.UserMap) > 0 {
		desc += "\nThe players here are:\n"
		for k := range r.UserMap {
			desc += mudutils.ColorString(k, mudutils.COLOR_BLUE) + "\n"
		}
	}
	r.userMu.RUnlock()

	if !r.Inventory.isEmpty() {
		desc += "\nYou see:\n"
		desc += r.Inventory.ListAsString(false)
	}

	if len(r.ExitMap) > 0 {
		exits := make([]string, 0, len(r.ExitMap))
		for dir := range r.ExitMap {
			exits = append(exits, dir)
		}
		sort.Strings(exits)
		desc += "\nExits: [" + strings.Join(exits, "] [") + "]\n"
	}

	desc += "\n"
	return desc
}

func (r *Room) RemovePlayer(username string) {
	r.userMu.Lock()
	delete(r.UserMap, username)
	r.userMu.Unlock()
}

// KillNPC removes the named NPC, distributes loot to the attacker, and
// broadcasts the kill to the room.
func (r *Room) KillNPC(npcname string, attackername string) {
	npcKey := strings.ToUpper(npcname)

	// Read loot info before acquiring the write lock.
	r.baddieMu.RLock()
	deadNPC := r.BaddieMap[npcKey]
	r.baddieMu.RUnlock()

	if deadNPC != nil && deadNPC.LootGoldMax > 0 {
		r.userMu.RLock()
		attacker := r.UserMap[attackername]
		r.userMu.RUnlock()

		if attacker != nil {
			i := Item{}
			gploot_quantity := rand.Int31n(deadNPC.LootGoldMax-deadNPC.LootGoldMin) + deadNPC.LootGoldMin
			msg := fmt.Sprint("You receive ", gploot_quantity, " gold.\n")
			attacker.SendMessage(msg)
			attacker.Inventory.AddItemByQuantity(i.FetchFromDB(r.DB, "GOLD"), gploot_quantity)
		}
	}

	r.baddieMu.Lock()
	delete(r.BaddieMap, npcKey)
	r.baddieMu.Unlock()

	r.Broadcast(attackername+" kills "+npcname+"!", attackername)
}

// KillPC respawns the player at their spawn location and broadcasts the kill.
func (r *Room) KillPC(player *MudPlayer, attackername string) {
	r.Broadcast(attackername+" kills "+player.Username+"!", player.Username)
	player.SendMessage("You were killed by " + attackername + "!")
	player.Respawn()
}

func (r *Room) AddPlayer(mp *MudPlayer) {
	r.userMu.Lock()
	r.UserMap[mp.Username] = mp
	r.userMu.Unlock()
}

func (r *Room) FetchFromDB(db *muddatabase.MudDatabase, tickdur int32, roomid interface{}) *Room {
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

	rows, err := db.Connection.Query(db.Context, query, roomid)

	if err != nil {
		fmt.Println("Room error: " + err.Error())
	}

	ret, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[RoomDB])

	if err != nil {
		fmt.Println("Room error: " + err.Error())
	}

	if err == nil {
		room = Room{
			ID:                   ret.ID,
			DB:                   db,
			Name:                 ret.Name,
			Description:          ret.Description,
			Inventory:            CreateInventory(),
			RoomBroadcastChannel: make(chan RoomMessage, 5),
			UserMap:              make(map[string]*MudPlayer),
			ExitMap:              make(map[string]*Room),
			BaddieMap:            make(map[string]*BaddieNPC),
			ExitJSON:             ret.ExitJSON,
			tickDurationInMS:     tickdur,
			GlobalWhisperChannel: r.GlobalWhisperChannel,
			done:                 r.done,
		}

		for iid := range ret.InventoryIDSlice {
			i := Item{}
			sid, _ := strconv.ParseInt(iid[1:], 10, 32)
			item_id := int32(sid)
			room.Inventory.AddItemByQuantity(i.FetchFromDB(db, item_id), ret.InventoryIDSlice[iid])
		}

		go room.ProcessBroadcast()
		go room.RunTicker()
	}

	return &room
}

func (r *Room) WriteToDB(db *muddatabase.MudDatabase) {
	query := "UPDATE room SET name=@name, description=@description, exitjson=@exitjson, inventoryidslice=@inventoryidslice WHERE id=@roomid;"

	args := pgx.NamedArgs{
		"name":             r.Name,
		"description":      r.Description,
		"exitjson":         r.ExitJSON,
		"inventoryidslice": r.Inventory.IDMap(),
		"roomid":           r.ID,
	}

	db.ExecWithArgs(query, args)
}
