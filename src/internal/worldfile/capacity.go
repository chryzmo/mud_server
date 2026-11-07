package worldfile

/*
Event-room capacity (plans/event-systems.md, D1). A room may set capacity: the most players it holds
(movement in is refused at that number; without it the server's hard cap, 60 by default, applies).
Past quiet_at players the room goes quiet: SAY and socials are limited to one line every ten seconds
for everyone except game masters and performers with the stage voice, who carry the room. A room
that sets a capacity and no quiet_at goes quiet at DefaultQuietAt players (or at its capacity if that
is smaller); a room that sets neither never goes quiet.
*/

const (
	MaxRoomCapacity = 200 // the most any room may be given
	DefaultHardCap  = 60  // what a room holds when it sets no capacity (the server's default hard cap)
	DefaultQuietAt  = 30
)

// RoomCrowd is a room's resolved capacity: Capacity 0 means the server's default; QuietAt 0 means never quiet.
type RoomCrowd struct{ Capacity, QuietAt int }

// ResolvedCrowd reads the room's capacity and quiet threshold with the defaults applied.
func (r Room) ResolvedCrowd() RoomCrowd {
	c := RoomCrowd{Capacity: r.Capacity, QuietAt: r.QuietAt}
	if c.QuietAt == 0 && c.Capacity > 0 {
		c.QuietAt = min(DefaultQuietAt, c.Capacity)
	}
	return c
}

func (w *World) validateCapacity(res *Result) {
	for _, key := range sortedKeys(w.Rooms) {
		r := w.Rooms[key]
		f, p := w.source["room:"+key], "rooms."+key
		if r.Capacity < 0 || r.Capacity > MaxRoomCapacity {
			res.errorf(f, p+".capacity", "must be from 1 to %d", MaxRoomCapacity)
		}
		if r.QuietAt < 0 {
			res.errorf(f, p+".quiet_at", "must be a positive number of players")
		}
		limit := DefaultHardCap
		if r.Capacity > 0 {
			limit = r.Capacity
		}
		if r.QuietAt > limit {
			res.errorf(f, p+".quiet_at", "%d is above the room's capacity of %d, so it could never go quiet", r.QuietAt, limit)
		}
	}
}
