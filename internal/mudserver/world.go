package mudserver

type World struct {
	StartingRooms map[string]*Room
	RoomMap       map[string]*Room
}

func createWorld() *World {
	w := &World{
		RoomMap: make(map[string]*Room),
	}

	// Build sample rooms
	startroom := CreateRoom("Start Room", "You find yourself in a generic looking room.  There is a door to the North.")
	northroom := CreateRoom("Balcony", "You are at a balcony overlooking the woods.  There is a door to the East and to the South.")
	battleroom := CreateRoom("Balcony", "You are in the arena.  There is a door to the West.")

	// Item test
	startkey := &Item{
		Name:       "KEY",
		Persistent: false,
		Quantity:   1,
	}

	startroom.Inventory.AddItem(startkey)

	// Spawner test
	baddie := BaddieNPC{
		Name:           "Orc",
		AttackAccuracy: 0,
		AttackPower:    0,
		Defense:        0,
		HP:             5,
		MP:             0,
	}

	testspawn := &BaddieSpawner{
		TotalNumber:    5,
		tickDurationMS: 3000,
		Location:       startroom,
		baddie:         baddie,
	}

	go testspawn.RunTicker()

	startroom.BaddieSpawnerMap["orc"] = testspawn

	// Link sample rooms
	startroom.ExitMap["N"] = northroom
	northroom.ExitMap["S"] = startroom
	northroom.ExitMap["E"] = battleroom
	battleroom.ExitMap["W"] = northroom

	w.RoomMap[startroom.Name] = startroom
	w.RoomMap[northroom.Name] = northroom
	w.RoomMap[battleroom.Name] = battleroom

	return w
}

func (w World) getRoomByName(roomname string) *Room {
	return w.RoomMap[roomname]
}