package mudserver

import (
	"encoding/json"
	"errors"
	"fmt"
	muddatabase "mud_server/src/internal/database"
	"strconv"

	"github.com/jackc/pgx/v5"
)

type World struct {
	StartingRooms map[string]*Room
	RoomMap       map[int32]*Room
	BaddieSpawnerMap map[string]*BaddieSpawner
	NPCTurnActionMap map[int32]NPCCommand
}

func createWorld(db *muddatabase.MudDatabase, roomTickDuration int32, whisperchan chan(MudMessage)) *World {
	w := &World{
		RoomMap: make(map[int32]*Room),
		BaddieSpawnerMap: make(map[string]*BaddieSpawner),
	}

	// Load rooms into RoomMap; this ensures that all rooms are loaded prior to linking
	roomIDList := w.RetrieveListofRoomIDsFromDB(db)
	for _,id := range roomIDList {
		r := Room{GlobalWhisperChannel: whisperchan}
		w.RoomMap[id] = r.FetchFromDB(db,roomTickDuration,id)
	}

	// Load NPC TurnAction functions here
	w.NPCTurnActionMap = GenerateNPCCommandList()

	// Link rooms & BaddieSpawners after loading rooms
	for _,room := range w.RoomMap {
		w.LinkRoomExits(room)
		w.LoadAndLinkRoomBaddieSpawners(db,room)
	}

	return w
}

// Use JSON column - Inventory for example?
// ReLinkRoomExits clears the room's ExitMap and re-populates it from its
// current ExitJSON. Call this after updating a room's exits at runtime.
func (w *World) ReLinkRoomExits(room *Room) {
	room.ExitMap = make(map[string]*Room)
	w.LinkRoomExits(room)
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
		fmt.Println("Room linking error: "+err.Error())
	}
}

func (w World) LoadAndLinkRoomBaddieSpawners(db *muddatabase.MudDatabase, room *Room) {
	query := "SELECT id FROM baddiespawner WHERE roomid=$1"
	var err error
	tmpspawner := BaddieSpawner{}

	rows,err := db.Connection.Query(db.Context,query,room.ID)

	if err != nil {
		fmt.Println("LinkRoomBaddiespawners query error: "+err.Error())
	}

	// Collect the ID list to []int32
	idslice, err := pgx.CollectRows(rows, pgx.RowTo[int32])

	if err != nil {
		fmt.Println("LinkRoomBaddiespawners row error: "+err.Error())
	} else {
		for _,id := range idslice {
			spawner := tmpspawner.FetchGenericFromDB(db,id)
			spawner.Location = room
			go spawner.RunTicker()
	
			w.BaddieSpawnerMap[spawner.baddie.Race] = spawner
		}
	}
}

func (w World) RetrieveListofRoomIDsFromDB(db *muddatabase.MudDatabase) []int32 {
	query := "SELECT id FROM room"
	var err error

	rows,err := db.Connection.Query(db.Context,query)

	if err != nil {
		fmt.Println("Room List error: "+err.Error())
	}

	// Collect the ID list to []int32
	idslice, err := pgx.CollectRows(rows, pgx.RowTo[int32])

	if err != nil {
		fmt.Println("Room Row error: "+err.Error())
	}

	return idslice
}

func (w World) WriteWorldToDB(db *muddatabase.MudDatabase) {
	// Iterate over all rooms and write to DB
	for _,room := range w.RoomMap {
		room.WriteToDB(db)
	}
}

/* For API export */
func (w World) PrintWorldRoomsAsJSON()  string {
	type roomjson struct {
		Name string
		Description string
		ExitJSON string
		InventorySliceID string
	}
	jsonreadymap := make(map[string]roomjson)

	for id, room := range(w.RoomMap) {
		exitmap := make(map[string]string)
		for dir, r := range room.ExitMap {
			exitmap[dir] = strconv.Itoa(int(r.ID))
		}

		jsonexit, _ := json.Marshal(exitmap)

		jsonroom := roomjson {
			Name: room.Name,
			Description: room.Description,
			ExitJSON: string(jsonexit),
		}
		jsonreadymap[strconv.Itoa(int(id))] = jsonroom

	}

	jsonreturn, err := json.Marshal(jsonreadymap)

	if err != nil {
		fmt.Println("PrinWorldRoomsAsJSON error: "+err.Error())
	}

	return string(jsonreturn);
}

/* For API export */
func (w World) PrintWorldEdgesAsJSON()  string {
	type edgejson struct {
		ID string
		Source string
		Target string
	}

	jsonreadymap := make(map[string]edgejson)

	for _, room := range(w.RoomMap) {
		jsonMap := make(map[string]interface{})
		err := json.Unmarshal([]byte(room.ExitJSON), &jsonMap)
	
		for _, roomid := range jsonMap {
			// JSON uses float64, so we need to assert interface{} to float64, then cast to int32
			if rid, ok := roomid.(float64); ok { 
				sourceid := strconv.Itoa(int(room.ID))
				targetid := strconv.Itoa(int(rid))
				idstring := fmt.Sprintf("%s_%s", sourceid, targetid)

				// Janky way to avoid duplicate edges for now
				reverseidstring := fmt.Sprintf("%s_%s", targetid, sourceid)

				roomedge := edgejson{
					ID: idstring,
					Source: sourceid,
					Target: targetid,
				}

				// If the reverse edge does not exist, create this edge
				_, alreadyexists := jsonreadymap[reverseidstring]
				if (!alreadyexists) {
					jsonreadymap[idstring] = roomedge;
				}
				
			} else {
				err = errors.New("PrintWorldEdgesAsJSON assertion failed.  Received a non-int32 value!")
			}
		}
	
		if err != nil {
			fmt.Println("PrintWorldEdgesAsJSON error: "+err.Error())
		}

	}

	jsonreturn, err := json.Marshal(jsonreadymap)

	if err != nil {
		fmt.Println("PrintWorldEdgesAsJSON JSON error: "+err.Error())
	}

	return string(jsonreturn);
}