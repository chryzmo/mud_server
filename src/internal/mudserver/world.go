package mudserver

import (
	"encoding/json"
	"errors"
	"fmt"
	muddatabase "mud_server/src/internal/database"
	"time"

	"github.com/jackc/pgx/v5"
)

// The World is owned by the game loop, like everything in it. RoomMap gains
// cloned rooms as crowds arrive (crowd.go), so connection readers must not
// read it: login only records where a player was saved, and the loop places
// them.
type World struct {
	StartingRooms    map[string]*Room
	RoomMap          map[int32]*Room
	BaddieSpawnerMap map[string]*BaddieSpawner
	Spawners         []*BaddieSpawner // every spawner; BaddieSpawnerMap keeps one per race
	NPCTurnActionMap map[int32]NPCCommand

	// Where new players start, and where a saved location that no longer exists falls
	// back to. Set when the world is built and never changed, so connection readers may read it.
	startRoomID int32

	// Area copies; see crowd.go. Loop only.
	areas      map[int32]*Area // template room ID -> its area
	nextRoomID int32           // next ID for a cloned room
	softCap    int // placement fills a copy of a room up to this
	hardCap    int // movement into a room is refused at this; 0 means no limit
	maxCopies  int
}

// createWorld loads the world from the database. With content (rooms defined by
// content files) only those rooms are loaded, the start room and the areas come from
// the content, and the config's id-based Areas are ignored; without it the world is
// whatever the database holds, as seeded by src/sql.
func createWorld(db *muddatabase.MudDatabase, crowd CrowdConfig, content *contentResult) *World {
	w := &World{
		RoomMap:          make(map[int32]*Room),
		BaddieSpawnerMap: make(map[string]*BaddieSpawner),
		startRoomID:      defaultStartRoomID,
	}
	if content != nil {
		w.startRoomID = content.startRoomID
		crowd.Areas = content.areas
	}

	// Load rooms into RoomMap; ensures all rooms are loaded prior to linking
	roomIDList := w.RetrieveListofRoomIDsFromDB(db, content != nil)
	for _, id := range roomIDList {
		r := Room{}
		w.RoomMap[id] = r.FetchFromDB(db, id)
	}

	// Load NPC TurnAction functions here
	w.NPCTurnActionMap = GenerateNPCCommandList()

	// Link rooms & BaddieSpawners after loading rooms
	for _, room := range w.RoomMap {
		w.LinkRoomExits(room)
		w.LoadAndLinkRoomBaddieSpawners(db, room)
	}

	w.configureAreas(crowd)

	return w
}

// tick advances the world by one step: every room's NPCs act, then every
// spawner checks whether it's due.
func (w *World) tick(now time.Time) {
	for _, room := range w.RoomMap {
		room.tick(now)
	}
	for _, spawner := range w.Spawners {
		spawner.tick(now)
	}
}

func (w World) LinkRoomExits(room *Room) {
	jsonMap := make(map[string]interface{})
	err := json.Unmarshal([]byte(room.ExitJSON), &jsonMap)

	for direction, roomid := range jsonMap {
		// JSON uses float64, so we need to assert interface{} to float64, then cast to int32
		if rid, ok := roomid.(float64); ok {
			room.ExitMap[direction] = w.RoomMap[int32(rid)]
		} else {
			err = errors.New("RoomID assertion failed.  Received a non-int32 value!")
		}
	}

	if err != nil {
		fmt.Println("Room linking error: " + err.Error())
	}
}

func (w *World) LoadAndLinkRoomBaddieSpawners(db *muddatabase.MudDatabase, room *Room) {
	query := "SELECT id FROM baddiespawner WHERE roomid=$1"
	var err error
	tmpspawner := BaddieSpawner{}

	rows, err := db.Connection.Query(db.Context, query, room.ID)

	if err != nil {
		fmt.Println("LinkRoomBaddiespawners query error: " + err.Error())
	}

	// Collect the ID list to []int32
	idslice, err := pgx.CollectRows(rows, pgx.RowTo[int32])

	if err != nil {
		fmt.Println("LinkRoomBaddiespawners row error: " + err.Error())
	} else {
		for _, id := range idslice {
			spawner := tmpspawner.FetchGenericFromDB(db, id)
			spawner.Location = room

			w.Spawners = append(w.Spawners, spawner)
			w.BaddieSpawnerMap[spawner.baddie.Race] = spawner
		}
	}
}

func (w World) RetrieveListofRoomIDsFromDB(db *muddatabase.MudDatabase, contentOnly bool) []int32 {
	query := "SELECT id FROM room"
	if contentOnly {
		query += " WHERE contentkey IS NOT NULL"
	}
	var err error

	rows, err := db.Connection.Query(db.Context, query)

	if err != nil {
		fmt.Println("Room List error: " + err.Error())
	}

	// Collect the ID list to []int32
	idslice, err := pgx.CollectRows(rows, pgx.RowTo[int32])

	if err != nil {
		fmt.Println("Room Row error: " + err.Error())
	}

	return idslice
}
