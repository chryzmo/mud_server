package mudserver

import (
	"encoding/json"
	"errors"
	"fmt"
	muddatabase "mud_server/src/internal/database"

	"github.com/jackc/pgx/v5"
)

type World struct {
	StartingRooms map[string]*Room
	RoomMap       map[int32]*Room
	BaddieSpawnerMap map[string]*BaddieSpawner
}

func createWorld(db muddatabase.MudDatabase) *World {
	w := &World{
		RoomMap: make(map[int32]*Room),
		BaddieSpawnerMap: make(map[string]*BaddieSpawner),
	}

	// Load Items?

	// Build sample rooms
	r := Room{}

	// Load rooms into RoomMap
	roomIDList := w.RetrieveListofRoomIDs(db)
	for _,id := range roomIDList {
		w.RoomMap[id] = r.FetchFromDB(db,id)
	}

	// Link rooms after loading
	for _,room := range w.RoomMap {
		w.LinkRoom(room)
	}

	// Spawner test
	orcbaddie := BaddieNPC{}
	orcbaddie = orcbaddie.FetchGenericFromDB(db, "ORC")

	testspawn := &BaddieSpawner{
		TotalNumber:    1,
		tickDurationMS: 3000,
		Location:       w.RoomMap[3],
		baddie:         orcbaddie,
	}

	go testspawn.RunTicker()

	w.BaddieSpawnerMap["orc"] = testspawn

	return w
}

func (w World) getRoomByID(roomid int32) *Room {
	return w.RoomMap[roomid]
}

func (w World) LinkRoom(room *Room) {
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
		fmt.Println("Room linking error: "+err.Error())
	}
}

func (w World) RetrieveListofRoomIDs(db muddatabase.MudDatabase) []int32 {
	query := "SELECT id FROM room"
	var err error

	rows,err := db.Connection.Query(db.Context,query)

	if err != nil {
		fmt.Println("Room List error: "+err.Error())
	}

	// Collect the ID list to []int32
	numbers, err := pgx.CollectRows(rows, pgx.RowTo[int32])

	return numbers
}