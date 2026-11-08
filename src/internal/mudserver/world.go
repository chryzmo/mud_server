package mudserver

import (
	"encoding/json"
	"errors"
	"fmt"
	muddatabase "mud_server/src/internal/database"
	"mud_server/src/internal/worldfile"
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

	worldFlags    map[string]bool                          // set for everyone by events or the GM (scheduler.go); loop only
	ambientBudget commandLimiter                           // every ambient line spends from this (ambient.go); loop only
	ambientGap    func(lo, hi time.Duration) time.Duration // nil: random; tests fix it
	onboarding    *worldfile.Onboarding                    // the walkthrough script; nil when the content has none
	mobCap        int                                      // most live monsters in the world; spawners wait at this (mobs.go)
	liveMobs      int                                      // monsters alive, counted once per world tick; loop only
	prog          worldfile.Progression                    // XP and level tuning; set when the world is built and never changed
	guide         *guide                                   // puzzles and regions the content declares; nil without content
	roomsByKey    map[string]*Room                         // content rooms by key (the originals)
	itemsByKey    map[string]*Item                         // content items by key: templates for what rules give

	// Where new players start, and where a saved location that no longer exists falls
	// back to. Set when the world is built and never changed, so connection readers may read it.
	startRoomID int32

	// Area copies; see crowd.go. Loop only.
	areas      map[int32]*Area // template room ID -> its area
	nextRoomID int32           // next ID for a cloned room
	softCap    int             // placement fills a copy of a room up to this
	hardCap    int             // movement into a room is refused at this; 0 means no limit
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
		worldFlags:       map[string]bool{},
		guide:            newGuide(nil, nil), // no puzzles or regions without content
		prog:             worldfile.DefaultProgression(),
		mobCap:           defaultMobCap,
	}
	if content != nil {
		w.startRoomID = content.startRoomID
		crowd.Areas = content.areas
		w.guide = content.guide
		w.prog = content.progression
		w.onboarding = content.onboarding
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

	w.indexContent(db)
	if content != nil {
		for _, room := range w.roomsByKey {
			room.ambient = content.ambient[room.Key]
			room.crowd = content.crowd[room.Key]
		}
		w.applyMobTraits(content.mobs)
	}
	w.configureAreas(crowd)

	return w
}

// indexContent builds the key lookups rules need and links each gate to the room it leads to.
func (w *World) indexContent(db *muddatabase.MudDatabase) {
	w.roomsByKey = make(map[string]*Room)
	for _, room := range w.RoomMap {
		if room.Key != "" && !room.clone {
			w.roomsByKey[room.Key] = room
		}
	}
	for _, room := range w.RoomMap {
		for _, g := range room.Gates {
			g.To = w.roomsByKey[g.ToKey]
		}
	}
	w.itemsByKey = make(map[string]*Item)
	rows, err := db.Connection.Query(db.Context, "SELECT id, contentkey FROM item WHERE contentkey IS NOT NULL")
	if err != nil {
		fmt.Println("item keys error: " + err.Error())
		return
	}
	type itemKey struct {
		ID  int32
		Key string
	}
	keys, err := pgx.CollectRows(rows, pgx.RowToStructByPos[itemKey])
	if err != nil {
		fmt.Println("item keys error: " + err.Error())
		return
	}
	for _, k := range keys {
		it := Item{}
		w.itemsByKey[k.Key] = it.FetchFromDB(db, k.ID)
	}
}

// tick advances the world by one step: every room's NPCs act, then every
// spawner checks whether it's due.
func (w *World) tick(now time.Time) {
	w.liveMobs = 0
	for _, room := range w.RoomMap {
		room.tick(now)
		w.ambientTick(room, now)
		w.liveMobs += len(room.BaddieMap)
	}
	for _, spawner := range w.Spawners {
		spawner.tick(now, w)
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
