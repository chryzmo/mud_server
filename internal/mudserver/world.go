package mudserver

type World struct {
	StartingRooms map[string]*Room
	RoomMap map[string]*Room
}

func createWorld () *World {
	w := &World{
		RoomMap: make(map[string]*Room),
	}

	// Build sample rooms
	startzone := CreateRoom("Start Room","You find yourself in a generic looking room.  There is a door to the North.")
	northroom := CreateRoom("Balcony","You are at a balcony overlooking the woods.  There is a door to the South.")

	// Item test
	startkey := &Item{
		Name: "KEY",
		Persistent: false,
		Quantity: 1,
	}

	startzone.Inventory.AddItem(startkey)

	// Link sample rooms
	startzone.ExitList["N"] = northroom
	northroom.ExitList["S"] = startzone

	w.RoomMap[startzone.Name] = startzone
	w.RoomMap[northroom.Name] = northroom

	return w
}

func (w World) getRoomByName(roomname string) *Room {
	return w.RoomMap[roomname]
}