package mudserver

import (
	"encoding/json"
	"errors"
	"fmt"
	muddatabase "mud_server/src/internal/database"

	"github.com/jackc/pgx/v5"
)

type World struct {
	StartingRooms    map[string]*Room
	RoomMap          map[int32]*Room
	BaddieSpawnerMap map[string]*BaddieSpawner
	NPCTurnActionMap map[int32]NPCCommand
}

func createWorld(db *muddatabase.MudDatabase, roomTickDuration int32, whisperchan chan MudMessage, done <-chan struct{}) *World {
	w := &World{
		RoomMap:          make(map[int32]*Room),
		BaddieSpawnerMap: make(map[string]*BaddieSpawner),
	}

	// Load rooms into RoomMap; ensures all rooms are loaded prior to linking
	roomIDList := w.RetrieveListofRoomIDsFromDB(db)
	for _, id := range roomIDList {
		r := Room{GlobalWhisperChannel: whisperchan, done: done}
		w.RoomMap[id] = r.FetchFromDB(db, roomTickDuration, id)
	}

	// Load NPC TurnAction functions here
	w.NPCTurnActionMap = GenerateNPCCommandList()

	// Link rooms & BaddieSpawners after loading rooms
	for _, room := range w.RoomMap {
		w.LinkRoomExits(room)
		w.LoadAndLinkRoomBaddieSpawners(db, room)
	}

	return w
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

func (w World) LoadAndLinkRoomBaddieSpawners(db *muddatabase.MudDatabase, room *Room) {
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
			go spawner.RunTicker()

			w.BaddieSpawnerMap[spawner.baddie.Race] = spawner
		}
	}
}

func (w World) RetrieveListofRoomIDsFromDB(db *muddatabase.MudDatabase) []int32 {
	query := "SELECT id FROM room"
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

func (w World) WriteWorldToDB(db *muddatabase.MudDatabase) {
	for _, room := range w.RoomMap {
		room.WriteToDB(db)
	}
}
