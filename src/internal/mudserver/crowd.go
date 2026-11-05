package mudserver

import (
	"fmt"
	"time"
)

/*
Areas and copies (plans/crowd-control.md). An area is a set of rooms that can be
copied: when every copy of a room is at the soft cap, a new copy of the whole
area opens, so a crowd spreads over self-contained clusters instead of piling
into one room. Copy 0 is the database's own rooms; later copies are runtime
clones that are never saved. Everything here runs on the game loop, and
RoomMap is loop-owned once copies exist: connection readers must not read it.
*/

// defaultStartRoomID is where a new player starts and where a saved location
// that no longer exists falls back to.
const defaultStartRoomID int32 = 1

const (
	defaultSoftCap   = 50
	defaultHardCap   = 60
	defaultMaxCopies = 200
)

// AreaConfig names the rooms (by database ID) that copy together.
type AreaConfig struct {
	Name  string  `json:"Name"`
	Rooms []int32 `json:"Rooms"`
}

// CrowdConfig tunes copying. Zero values take the defaults; no Areas means no
// copying at all.
type CrowdConfig struct {
	SoftCap   int          `json:"SoftCap"`   // placement fills a copy of a room up to this (default 50)
	HardCap   int          `json:"HardCap"`   // movement into any room is refused at this many occupants (default 60)
	MaxCopies int          `json:"MaxCopies"` // most copies of one area (default 200)
	Areas     []AreaConfig `json:"Areas"`
}

// Area is a set of template rooms and the copies made of them.
type Area struct {
	Name      string
	templates []int32           // template room IDs, in configuration order
	copies    []map[int32]*Room // copies[i][templateID]; copies[0] are the originals
}

// configureAreas sets up copying from c on an already loaded world. It records
// each templated room's starting contents so later copies begin the same way.
func (w *World) configureAreas(c CrowdConfig) {
	w.softCap, w.hardCap, w.maxCopies = c.SoftCap, c.HardCap, c.MaxCopies
	if w.softCap <= 0 {
		w.softCap = defaultSoftCap
	}
	if w.hardCap <= 0 {
		w.hardCap = defaultHardCap
	}
	if w.maxCopies <= 0 {
		w.maxCopies = defaultMaxCopies
	}
	w.areas = make(map[int32]*Area)

	highest := int32(0)
	for id := range w.RoomMap {
		highest = max(highest, id)
	}
	w.nextRoomID = highest + 1

	for _, ac := range c.Areas {
		area := &Area{Name: ac.Name}
		originals := make(map[int32]*Room)
		for _, id := range ac.Rooms {
			room := w.RoomMap[id]
			switch {
			case room == nil:
				fmt.Printf("Area %q: room %d does not exist; skipped\n", ac.Name, id)
				continue
			case w.areas[id] != nil || originals[id] != nil:
				fmt.Printf("Area %q: room %d is already in an area; skipped\n", ac.Name, id)
				continue
			}
			area.templates = append(area.templates, id)
			originals[id] = room
			room.pristine = room.Inventory.copyOf()
			room.area = area
		}
		if len(originals) == 0 {
			fmt.Printf("Area %q has no rooms; ignored\n", ac.Name)
			continue
		}
		area.copies = []map[int32]*Room{originals}
		for id := range originals {
			w.areas[id] = area
		}
	}
}

// templateRoom returns the original room for a saved room ID, or the default
// start room if it no longer exists. Loop only.
func (w *World) templateRoom(id int32) *Room {
	if room := w.RoomMap[id]; room != nil && !room.clone {
		return room
	}
	return w.RoomMap[defaultStartRoomID]
}

// place picks the room a player should arrive in for a saved room ID. See
// placeNear; this is the form for a player with no history in the area.
func (w *World) place(templateID int32) (*Room, bool) {
	return w.placeNear(templateID, -1)
}

// placeNear picks the room for an arrival at a template room. Rooms outside any
// area are used as they are, unless they are at the hard cap, in which case the
// arrival goes to the default start room instead. In an area it prefers the
// copy the player was last in (preferred, if it is below the hard cap), then
// the fullest copy below the soft cap, so crowds form instead of thinning out.
// When every copy is full it opens a new one, and it refuses (false) once the
// copy limit is reached. Loop only.
func (w *World) placeNear(templateID int32, preferred int) (*Room, bool) {
	start := w.templateRoom(templateID)
	if start == nil {
		return nil, false
	}
	area := w.areas[start.ID]
	if area == nil {
		if w.atHardCap(start) && start.ID != defaultStartRoomID {
			return w.placeNear(defaultStartRoomID, -1)
		}
		return start, true
	}

	if preferred >= 0 && preferred < len(area.copies) {
		if room := area.copies[preferred][start.ID]; !w.atHardCap(room) {
			return room, true
		}
	}

	best, bestCount := -1, -1
	for i, copy := range area.copies {
		if n := len(copy[start.ID].UserMap); n < w.softCap && n > bestCount {
			best, bestCount = i, n
		}
	}
	if best < 0 {
		if len(area.copies) >= w.maxCopies {
			return nil, false
		}
		best = w.cloneArea(area)
	}
	return area.copies[best][start.ID], true
}

// canAdmit reports whether place would admit someone saved in templateID,
// without changing anything. Loop only.
func (w *World) canAdmit(templateID int32) bool {
	start := w.templateRoom(templateID)
	if start == nil {
		return false
	}
	area := w.areas[start.ID]
	if area == nil {
		if w.atHardCap(start) && start.ID != defaultStartRoomID {
			return w.canAdmit(defaultStartRoomID)
		}
		return true
	}
	if len(area.copies) < w.maxCopies {
		return true
	}
	for _, copy := range area.copies {
		if len(copy[start.ID].UserMap) < w.softCap {
			return true
		}
	}
	return false
}

// atHardCap reports whether movement into the room is refused.
func (w *World) atHardCap(room *Room) bool {
	return w.hardCap > 0 && len(room.UserMap) >= w.hardCap
}

// resolveMove decides where a player moving from one room into dest actually
// ends up. Inside a copy it is dest itself. Crossing an area boundary (into an
// area from outside it, or from another area) is a placement: the player's last
// copy of that area if it has room, else the fullest copy with space, opening a
// new one if needed. Anywhere, a room at the hard cap refuses (false). Loop only.
func (w *World) resolveMove(mp *MudPlayer, from, dest *Room) (*Room, bool) {
	if dest.area != nil && dest.area != from.area {
		preferred := -1
		if copy, ok := mp.lastCopy[dest.area]; ok {
			preferred = copy
		}
		return w.placeNear(dest.persistID(), preferred)
	}
	if w.atHardCap(dest) {
		return nil, false
	}
	return dest, true
}

// cloneArea opens another copy of every room in the area, wired together
// (exits that leave the area still lead to the shared rooms), with the
// templates' starting contents and their own spawners. It returns the new
// copy's index. Loop only.
func (w *World) cloneArea(area *Area) int {
	index := len(area.copies)
	originals := area.copies[0]
	copyOf := make(map[int32]*Room, len(originals))

	for _, tid := range area.templates {
		orig := originals[tid]
		room := &Room{
			ID:          w.nextRoomID,
			DB:          orig.DB,
			Name:        orig.Name,
			Description: orig.Description,
			ExitMap:     make(map[string]*Room, len(orig.ExitMap)),
			UserMap:     make(map[string]*MudPlayer),
			BaddieMap:   make(map[string]*BaddieNPC),
			Inventory:   orig.pristine.copyOf(),
			ExitJSON:    orig.ExitJSON,
			pristine:    orig.pristine,
			TemplateID:  tid,
			clone:       true,
			area:        area,
			copy:        index,
		}
		w.nextRoomID++
		w.RoomMap[room.ID] = room
		copyOf[tid] = room
	}

	for tid, room := range copyOf {
		for dir, target := range originals[tid].ExitMap {
			if sibling := copyOf[target.ID]; sibling != nil {
				room.ExitMap[dir] = sibling
			} else {
				room.ExitMap[dir] = target
			}
		}
	}

	for _, spawner := range w.Spawners {
		for tid, room := range copyOf {
			if spawner.Location == originals[tid] {
				clone := *spawner
				clone.Location = room
				clone.nextSpawnAt = time.Time{}
				w.Spawners = append(w.Spawners, &clone)
			}
		}
	}

	area.copies = append(area.copies, copyOf)
	return index
}

// copyOf returns an independent inventory with the same items.
func (i *Inventory) copyOf() *Inventory {
	out := CreateInventory()
	if i == nil {
		return out
	}
	for name, item := range i.items {
		out.items[name] = item.createCopy()
	}
	out.weight = i.weight
	return out
}

// canEnter resolves a move into *dest (see resolveMove), replacing it with the
// room the player will actually enter. False means the way is too crowded.
func (w *World) canEnter(mp *MudPlayer, dest **Room) bool {
	room, ok := w.resolveMove(mp, mp.Location, *dest)
	if ok {
		*dest = room
	}
	return ok
}

// rememberCopy notes which copy of its area the player's room belongs to, so a
// later boundary crossing can bring them back to the same one.
func (mp *MudPlayer) rememberCopy() {
	if mp.Location == nil || mp.Location.area == nil {
		return
	}
	if mp.lastCopy == nil {
		mp.lastCopy = make(map[*Area]int)
	}
	mp.lastCopy[mp.Location.area] = mp.Location.copy
}

// preferredCopy is the player's last copy of the area that contains room, or -1.
func (mp *MudPlayer) preferredCopy(room *Room) int {
	if room != nil && room.area != nil {
		if copy, ok := mp.lastCopy[room.area]; ok {
			return copy
		}
	}
	return -1
}
