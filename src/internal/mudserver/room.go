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
	"time"

	"github.com/jackc/pgx/v5"
)

// A Room is owned by the game loop: only the loop reads or changes it, so it
// needs no locks. NPCs act when the loop calls tick on each world tick.
type Room struct {
	ID          int32
	DB          *muddatabase.MudDatabase
	Name        string
	Description string
	ExitMap     map[string]*Room
	UserMap     map[string]*MudPlayer
	BaddieMap   map[string]*BaddieNPC
	Inventory   *Inventory
	ExitJSON    string
	dirty       bool // contents changed since the last save; see persist.go

	// Copies of a room (see crowd.go). A clone has no database row: it is never
	// saved, and players' saved locations name its template instead.
	TemplateID int32
	clone      bool
	pristine   *Inventory // the template's starting contents, copied into each clone
	area       *Area      // the area this room belongs to, or nil
	copy       int        // which copy of the area: 0 for the originals
}

// persistID is the database ID that names this room in saved data: the room's
// own for an original, its template's for a copy.
func (r *Room) persistID() int32 {
	if r.clone {
		return r.TemplateID
	}
	return r.ID
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

// tick runs one turn for every NPC in the room.
func (r *Room) tick(now time.Time) {
	// Snapshot first: a turn can kill an NPC or a player, changing the maps.
	npcs := make([]*BaddieNPC, 0, len(r.BaddieMap))
	for _, npc := range r.BaddieMap {
		npcs = append(npcs, npc)
	}
	for _, npc := range npcs {
		npc.TurnAction(r, npc, now)
	}
}

// Broadcast sends msg to everyone in the room except senderusername.
func (r *Room) Broadcast(msg string, senderusername string) {
	r.BroadcastFrame(encodeText(msg), senderusername)
}

// BroadcastFrame queues one already-encoded frame to everyone in the room
// except senderusername: format once, share the bytes.
func (r *Room) BroadcastFrame(frame []byte, senderusername string) {
	for _, player := range r.UserMap {
		if player.Username != senderusername {
			player.sendFrame(frame)
		}
	}
}

func (r *Room) LookDescription() string {
	// Append each section once rather than copying the growing player list
	// for every occupant. The room's map iteration order remains unchanged.
	var desc strings.Builder
	desc.Grow(len(r.Name) + len(r.Description) + 32)
	desc.WriteString("You are in '")
	desc.WriteString(r.Name)
	desc.WriteString("'\n")
	desc.WriteString(r.Description)
	desc.WriteByte('\n')

	if len(r.BaddieMap) > 0 {
		desc.WriteString("\nThe monsters here are:\n")
		for _, baddie := range r.BaddieMap {
			desc.WriteString(mudutils.ColorString(baddie.Name, mudutils.COLOR_RED))
			desc.WriteByte('\n')
		}
	}

	if len(r.UserMap) > 0 {
		desc.WriteString("\nThe players here are:\n")
		for k := range r.UserMap {
			desc.WriteString(mudutils.ColorString(k, mudutils.COLOR_BLUE))
			desc.WriteByte('\n')
		}
	}

	if !r.Inventory.isEmpty() {
		desc.WriteString("\nYou see:\n")
		desc.WriteString(r.Inventory.ListAsString(false))
	}

	if len(r.ExitMap) > 0 {
		exits := make([]string, 0, len(r.ExitMap))
		for dir := range r.ExitMap {
			exits = append(exits, dir)
		}
		sort.Strings(exits)
		desc.WriteString("\nExits: [")
		desc.WriteString(strings.Join(exits, "] ["))
		desc.WriteString("]\n")
	}

	desc.WriteByte('\n')
	return desc.String()
}

// Players returns a snapshot of the players in the room, safe to iterate while
// a turn moves, kills or respawns players.
func (r *Room) Players() []*MudPlayer {
	players := make([]*MudPlayer, 0, len(r.UserMap))
	for _, p := range r.UserMap {
		players = append(players, p)
	}
	return players
}

// GetPlayer returns the named player if they're in the room, or nil.
func (r *Room) GetPlayer(username string) *MudPlayer {
	return r.UserMap[username]
}

func (r *Room) RemovePlayer(username string) {
	delete(r.UserMap, username)
}

// KillNPC removes the named NPC, distributes loot to the attacker, and
// broadcasts the kill to the room.
func (r *Room) KillNPC(npcname string, attackername string) {
	npcKey := strings.ToUpper(npcname)

	deadNPC := r.BaddieMap[npcKey]

	if deadNPC != nil && deadNPC.LootGoldMax > 0 {
		attacker := r.UserMap[attackername]

		if attacker != nil {
			i := Item{}
			gploot_quantity := rand.Int31n(deadNPC.LootGoldMax-deadNPC.LootGoldMin) + deadNPC.LootGoldMin
			msg := fmt.Sprint("You receive ", gploot_quantity, " gold.\n")
			attacker.SendMessage(msg)
			attacker.Inventory.AddItemByQuantity(i.FetchFromDB(r.DB, "GOLD"), gploot_quantity)
		}
	}

	delete(r.BaddieMap, npcKey)

	r.Broadcast(attackername+" kills "+npcname+"!", attackername)
}

// KillPC respawns the player at their spawn location and broadcasts the kill.
func (r *Room) KillPC(player *MudPlayer, attackername string) {
	r.Broadcast(attackername+" kills "+player.Username+"!", player.Username)
	player.SendMessage("You were killed by " + attackername + "!")
	player.Respawn()
}

func (r *Room) AddPlayer(mp *MudPlayer) {
	r.UserMap[mp.Username] = mp
}

func (r *Room) FetchFromDB(db *muddatabase.MudDatabase, roomid interface{}) *Room {
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
			ID:          ret.ID,
			DB:          db,
			Name:        ret.Name,
			Description: ret.Description,
			Inventory:   CreateInventory(),
			UserMap:     make(map[string]*MudPlayer),
			ExitMap:     make(map[string]*Room),
			BaddieMap:   make(map[string]*BaddieNPC),
			ExitJSON:    ret.ExitJSON,
		}

		for iid := range ret.InventoryIDSlice {
			i := Item{}
			sid, _ := strconv.ParseInt(iid[1:], 10, 32)
			item_id := int32(sid)
			room.Inventory.AddItemByQuantity(i.FetchFromDB(db, item_id), ret.InventoryIDSlice[iid])
		}

	}

	return &room
}
