package mudserver

import (
	"fmt"
	"testing"

	"mud_server/src/internal/muddata"
)

// crowdWorld is the seeded layout without a database: Start (1) -N-> Balcony
// (2) -E-> Arena (3), with a Garden (4) reached from the Arena that is outside
// any area. Rooms 1-3 are an area; the Arena has a spawner and the Start Room
// holds one item.
func crowdWorld(t *testing.T, softCap, maxCopies int) *World {
	t.Helper()
	return crowdWorldCaps(t, softCap, 0, maxCopies)
}

// crowdWorldCaps is crowdWorld with a hard cap (0 takes the default).
func crowdWorldCaps(t *testing.T, softCap, hardCap, maxCopies int) *World {
	t.Helper()
	w := &World{RoomMap: make(map[int32]*Room)}
	room := func(id int32, name string) *Room {
		r := &Room{
			ID: id, Name: name, Description: name + " description",
			ExitMap: map[string]*Room{}, UserMap: map[string]*MudPlayer{}, BaddieMap: map[string]*BaddieNPC{},
			Inventory: CreateInventory(), ExitJSON: "{}",
		}
		w.RoomMap[id] = r
		return r
	}
	start, balcony, arena, garden := room(1, "Start Room"), room(2, "Balcony"), room(3, "Arena"), room(4, "Garden")
	start.ExitMap["N"] = balcony
	balcony.ExitMap["S"], balcony.ExitMap["E"] = start, arena
	arena.ExitMap["W"], arena.ExitMap["E"] = balcony, garden
	garden.ExitMap["W"] = arena
	start.Inventory.AddItemByQuantity(&Item{ID: 1, Name: "lamp", Quantity: 1}, 1)
	w.Spawners = []*BaddieSpawner{{ID: 1, TotalNumber: 1, tickDurationMS: 5000, Location: arena}}

	w.configureAreas(CrowdConfig{SoftCap: softCap, HardCap: hardCap, MaxCopies: maxCopies, Areas: []AreaConfig{{Name: "start", Rooms: []int32{1, 2, 3}}}})
	return w
}

func arrive(w *World, templateID int32, name string) *Room {
	room, ok := w.place(templateID)
	if !ok {
		return nil
	}
	room.AddPlayer(&MudPlayer{Username: name, Session: &Session{out: make(chan []byte, 64), closed: make(chan struct{})}})
	return room
}

func TestPlaceWithoutAreasUsesTheRoomItself(t *testing.T) {
	w := &World{RoomMap: map[int32]*Room{1: {ID: 1, UserMap: map[string]*MudPlayer{}}, 7: {ID: 7, UserMap: map[string]*MudPlayer{}}}}
	w.configureAreas(CrowdConfig{HardCap: 1000})
	for i := 0; i < 500; i++ {
		if got := arrive(w, 7, fmt.Sprint("p", i)); got != w.RoomMap[7] {
			t.Fatalf("player %d went to room %v, want the room itself", i, got)
		}
	}
	if got, _ := w.place(99); got != w.RoomMap[1] {
		t.Errorf("an unknown room should fall back to the start room, got %v", got)
	}
	if len(w.RoomMap) != 2 {
		t.Errorf("rooms = %d, want no clones without areas", len(w.RoomMap))
	}
}

func TestPlaceFillsTheFullestCopyBeforeOpeningAnother(t *testing.T) {
	w := crowdWorld(t, 3, 10)
	var copies []*Room
	seen := map[*Room]bool{}
	for i := 0; i < 7; i++ {
		r := arrive(w, 1, fmt.Sprint("p", i))
		if !seen[r] {
			seen[r] = true
			copies = append(copies, r)
		}
	}
	// 3 + 3 + 1: copy 0 fills, then copy 1, then a third copy opens.
	if len(copies) != 3 {
		t.Fatalf("7 players at soft cap 3 used %d copies, want 3", len(copies))
	}
	for i, want := range []int{3, 3, 1} {
		if got := len(copies[i].UserMap); got != want {
			t.Errorf("copy %d holds %d, want %d", i, got, want)
		}
	}
	if copies[0] != w.RoomMap[1] {
		t.Error("the first copy should be the original room")
	}

	// Someone leaves copy 0: the next arrival refills it before the lone third copy
	// (fullest copy below the cap wins, so crowds re-form instead of thinning).
	copies[0].RemovePlayer("p0")
	if got := arrive(w, 1, "late"); got != copies[0] {
		t.Errorf("late arrival went to a copy with %d players, want the fuller original (2)", len(got.UserMap))
	}
	// And the third copy, with 1, still takes new players only once the others are full.
	if got := arrive(w, 1, "late2"); got != copies[2] {
		t.Errorf("after the others filled, arrival should use the third copy, got %d players there", len(got.UserMap))
	}
}

func TestPlaceRefusesWhenCopiesAreExhausted(t *testing.T) {
	w := crowdWorld(t, 2, 2)
	for i := 0; i < 4; i++ {
		if arrive(w, 1, fmt.Sprint("p", i)) == nil {
			t.Fatalf("player %d refused before the limit", i)
		}
	}
	before := len(w.RoomMap)
	if room, ok := w.place(1); ok || room != nil {
		t.Errorf("5th player placed in %v at capacity, want a refusal", room)
	}
	if len(w.RoomMap) != before {
		t.Error("a refusal must not open another copy")
	}
	// Other rooms of the area have their own occupancy: the Balcony is empty.
	if arrive(w, 2, "balconyfan") == nil {
		t.Error("an empty room in the same area should still take arrivals")
	}
}

func TestClonesAreSelfContainedCopies(t *testing.T) {
	w := crowdWorld(t, 1, 5)
	arrive(w, 1, "first")
	second := arrive(w, 1, "second") // opens copy 1
	if second == w.RoomMap[1] || !second.clone || second.TemplateID != 1 || second.persistID() != 1 {
		t.Fatalf("second copy = %+v, want a clone of room 1 that saves as room 1", second)
	}
	if second.ID <= 4 {
		t.Errorf("clone ID %d collides with the database's rooms", second.ID)
	}
	if w.RoomMap[second.ID] != second {
		t.Error("clones belong in RoomMap so ticks and lookups see them")
	}

	balcony := second.ExitMap["N"]
	arena := balcony.ExitMap["E"]
	if balcony == w.RoomMap[2] || arena == w.RoomMap[3] || !balcony.clone || !arena.clone {
		t.Fatal("exits inside a copy must lead to the same copy, not the originals")
	}
	if balcony.ExitMap["S"] != second || arena.ExitMap["W"] != balcony {
		t.Error("exits back inside the copy must close the loop")
	}
	if arena.ExitMap["E"] != w.RoomMap[4] {
		t.Error("an exit leaving the area must lead to the shared room")
	}
	if w.RoomMap[1].ExitMap["N"] != w.RoomMap[2] || w.RoomMap[3].ExitMap["E"] != w.RoomMap[4] {
		t.Error("the originals' exits must be untouched")
	}
	if w.RoomMap[4].ExitMap["W"] != w.RoomMap[3] {
		t.Error("the shared room's exits are not rewritten")
	}

	// Contents are independent copies of the starting contents.
	if second.Inventory.GetItem("lamp") == nil {
		t.Fatal("the copy should start with the template's lamp")
	}
	second.Inventory.RemoveItem(second.Inventory.GetItem("lamp"))
	if w.RoomMap[1].Inventory.GetItem("lamp") == nil {
		t.Error("taking the lamp from a copy removed it from the original")
	}
	third := arrive(w, 1, "third")
	if third.Inventory.GetItem("lamp") == nil {
		t.Error("a later copy should start with the template's starting contents, not a copy's current ones")
	}

	// Only the Arena has a spawner; each copy gets its own, pointed at its own Arena.
	spawnersAt := map[*Room]int{}
	for _, s := range w.Spawners {
		spawnersAt[s.Location]++
	}
	if len(w.Spawners) != 3 || spawnersAt[w.RoomMap[3]] != 1 || spawnersAt[arena] != 1 {
		t.Errorf("spawners = %d (%v), want one per Arena copy", len(w.Spawners), spawnersAt)
	}
}

func TestConfigureAreasSkipsBadRooms(t *testing.T) {
	w := &World{RoomMap: map[int32]*Room{
		1: {ID: 1, UserMap: map[string]*MudPlayer{}, Inventory: CreateInventory()},
		2: {ID: 2, UserMap: map[string]*MudPlayer{}, Inventory: CreateInventory()},
	}}
	w.configureAreas(CrowdConfig{Areas: []AreaConfig{
		{Name: "a", Rooms: []int32{1, 99, 1}},
		{Name: "b", Rooms: []int32{1}}, // 1 is taken by a
		{Name: "empty", Rooms: []int32{99}},
	}})
	if len(w.areas) != 1 || w.areas[1] == nil || len(w.areas[1].templates) != 1 {
		t.Errorf("areas = %v, want only room 1 in area a", w.areas)
	}
	if w.softCap != defaultSoftCap || w.maxCopies != defaultMaxCopies {
		t.Errorf("defaults = %d/%d, want %d/%d", w.softCap, w.maxCopies, defaultSoftCap, defaultMaxCopies)
	}
}

func TestRespawnPlacesInACopyWithSpace(t *testing.T) {
	w := crowdWorld(t, 1, 2)
	start := w.RoomMap[1]
	arrive(w, 1, "occupant") // copy 0 is now at the soft cap

	player := &MudPlayer{Username: "ghost", Session: &Session{out: make(chan []byte, 64), closed: make(chan struct{})},
		Location: w.RoomMap[3], SpawnLocation: start, world: w, Equipment: NewEquipmentSlots()}
	w.RoomMap[3].AddPlayer(player)
	player.Respawn()
	if player.Location == start || !player.Location.clone {
		t.Errorf("respawn went to %v, want a copy with space", player.Location.ID)
	}
	if _, here := w.RoomMap[3].UserMap["ghost"]; here {
		t.Error("the respawned player is still in the room they died in")
	}

}

// With every copy full, a respawn still returns to the spawn room: it never strands anyone.
func TestRespawnAtCapacityStillReturnsToTheSpawnRoom(t *testing.T) {
	w := crowdWorld(t, 1, 1)
	start := w.RoomMap[1]
	arrive(w, 1, "occupant") // the only copy is at the soft cap

	player := &MudPlayer{Username: "ghost", Session: &Session{out: make(chan []byte, 64), closed: make(chan struct{})},
		Location: w.RoomMap[3], SpawnLocation: start, world: w, Equipment: NewEquipmentSlots()}
	w.RoomMap[3].AddPlayer(player)
	player.Respawn()
	if player.Location != start {
		t.Errorf("respawn at capacity went to room %d, want the spawn room", player.Location.ID)
	}
	if len(w.RoomMap) != 4 {
		t.Error("a respawn must not open copies beyond the limit")
	}
}

func TestCloneRoomsAreNeverPersistedAndNameTheirTemplate(t *testing.T) {
	w := crowdWorld(t, 1, 3)
	arrive(w, 1, "a")
	clone := arrive(w, 1, "b")
	p := &MudPlayer{Username: "b", Location: clone, SpawnLocation: w.RoomMap[1], Equipment: NewEquipmentSlots(), Inventory: CreateInventory()}
	snap := p.snapshot()
	if snap.RoomID != 1 || snap.SpawnLocationID != 1 {
		t.Errorf("snapshot saved room %d / spawn %d, want the template 1 / 1", snap.RoomID, snap.SpawnLocationID)
	}
}

// crowdPlayer is a player standing in room, able to move and be told things.
func crowdPlayer(w *World, name string, room *Room) *MudPlayer {
	p := &MudPlayer{Username: name, Session: &Session{out: make(chan []byte, 256), closed: make(chan struct{})},
		Equipment: NewEquipmentSlots(), Inventory: CreateInventory(), world: w, Location: room}
	room.AddPlayer(p)
	p.rememberCopy()
	return p
}

func fillRoom(room *Room, n int, prefix string) {
	for i := 0; i < n; i++ {
		name := fmt.Sprint(prefix, i)
		room.UserMap[name] = &MudPlayer{Username: name, Session: &Session{out: make(chan []byte, 256), closed: make(chan struct{})}}
	}
}

func TestMoveIsRefusedAtTheHardCapAndChangesNothing(t *testing.T) {
	w := crowdWorldCaps(t, 50, 2, 10)
	balcony := w.RoomMap[2]
	fillRoom(balcony, 2, "fan")
	mover := crowdPlayer(w, "mover", w.RoomMap[1])
	bystander := crowdPlayer(w, "bystander", w.RoomMap[1])

	if got := mover.MoveRoom("N"); got != muddata.ERR_ROOM_FULL {
		t.Fatalf("MoveRoom into a full room = %d, want ERR_ROOM_FULL", got)
	}
	if mover.Location != w.RoomMap[1] || w.RoomMap[1].UserMap["mover"] != mover || balcony.UserMap["mover"] != nil {
		t.Error("a refused move must leave the player where they were")
	}
	if len(bystander.Session.out) != 0 || len(balcony.UserMap["fan0"].Session.out) != 0 {
		t.Error("a refused move must not announce a departure or an arrival")
	}

	delete(balcony.UserMap, "fan0")
	if got := mover.MoveRoom("N"); got != muddata.MUD_OK || mover.Location != balcony {
		t.Errorf("after space opened, MoveRoom = %d into room %d, want a normal move into the Balcony", got, mover.Location.ID)
	}
}

func TestSharedRoomsAreCappedToo(t *testing.T) {
	w := crowdWorldCaps(t, 50, 1, 10)
	fillRoom(w.RoomMap[4], 1, "gardener")
	p := crowdPlayer(w, "p", w.RoomMap[3])
	if got := p.MoveRoom("E"); got != muddata.ERR_ROOM_FULL {
		t.Errorf("MoveRoom into a full shared room = %d, want ERR_ROOM_FULL", got)
	}
	// An arrival whose saved room is a full shared room starts in the start area instead.
	room, ok := w.place(4)
	if !ok || room.area == nil || room.ID == 4 {
		t.Errorf("arrival saved in a full shared room went to %v, want the start area", room)
	}
}

func TestMovingInsideACopyStaysInTheCopy(t *testing.T) {
	w := crowdWorld(t, 1, 10)
	arrive(w, 1, "first")
	p := crowdPlayer(w, "p", mustPlace(t, w, 1)) // opens copy 1
	if p.Location.copy != 1 {
		t.Fatalf("setup: player is in copy %d, want 1", p.Location.copy)
	}
	if got := p.MoveRoom("N"); got != muddata.MUD_OK {
		t.Fatalf("MoveRoom = %d", got)
	}
	if p.Location.copy != 1 || !p.Location.clone || p.Location.TemplateID != 2 {
		t.Errorf("after N the player is in copy %d template %d, want copy 1's Balcony", p.Location.copy, p.Location.TemplateID)
	}
	if p.lastCopy[p.Location.area] != 1 {
		t.Errorf("remembered copy = %d, want 1", p.lastCopy[p.Location.area])
	}
}

func mustPlace(t *testing.T, w *World, id int32) *Room {
	t.Helper()
	room, ok := w.place(id)
	if !ok {
		t.Fatalf("place(%d) refused", id)
	}
	return room
}

// Leaving the area through a shared room and coming back is a placement, and
// the player returns to the copy they left, not whichever copy is fullest.
func TestReturningToAnAreaBringsYouBackToYourCopy(t *testing.T) {
	w := crowdWorld(t, 2, 10)
	area := w.areas[3]
	arrive(w, 3, "stayer") // copy 0's Arena holds 1: the fullest copy with space
	copyOne := area.copies[w.cloneArea(area)][3]
	p := crowdPlayer(w, "p", copyOne)

	if got := p.MoveRoom("E"); got != muddata.MUD_OK || p.Location != w.RoomMap[4] {
		t.Fatalf("leaving to the Garden: %d, in room %d", got, p.Location.ID)
	}
	if got := p.MoveRoom("W"); got != muddata.MUD_OK {
		t.Fatalf("coming back: %d", got)
	}
	if p.Location != copyOne {
		t.Errorf("came back to copy %d, want the copy they left (1); fill-first alone would have picked copy 0", p.Location.copy)
	}
}

func TestReturningFallsBackWhenYourCopyIsFull(t *testing.T) {
	w := crowdWorldCaps(t, 2, 3, 10)
	area := w.areas[3]
	copyOne := area.copies[w.cloneArea(area)][3]
	p := crowdPlayer(w, "p", copyOne)
	p.MoveRoom("E")
	fillRoom(copyOne, 3, "squatter") // their old Arena fills while they are away
	if got := p.MoveRoom("W"); got != muddata.MUD_OK {
		t.Fatalf("coming back: %d", got)
	}
	if p.Location == copyOne || p.Location.persistID() != 3 {
		t.Errorf("came back to room %d, want another copy of the Arena", p.Location.ID)
	}
}

func TestCrossingIntoAFullAreaIsRefusedAtTheCopyLimit(t *testing.T) {
	w := crowdWorldCaps(t, 1, 5, 1) // one copy, soft cap 1
	arrive(w, 3, "stayer")
	p := crowdPlayer(w, "p", w.RoomMap[4])
	if got := p.MoveRoom("W"); got != muddata.ERR_ROOM_FULL {
		t.Errorf("MoveRoom into an area with no space = %d, want ERR_ROOM_FULL", got)
	}
	if p.Location != w.RoomMap[4] {
		t.Error("a refused crossing must leave the player in the Garden")
	}
}

func TestRespawnPrefersTheCopyYouDiedIn(t *testing.T) {
	w := crowdWorld(t, 3, 10)
	area := w.areas[3]
	arrive(w, 1, "a")
	arrive(w, 1, "b") // copy 0's Start Room is the fuller one
	copyOne := area.copies[w.cloneArea(area)]
	p := crowdPlayer(w, "p", copyOne[3])
	p.SpawnLocation = w.RoomMap[1]
	p.Respawn()
	if p.Location != copyOne[1] {
		t.Errorf("respawned in copy %d, want the copy they died in (1)", p.Location.copy)
	}
}
