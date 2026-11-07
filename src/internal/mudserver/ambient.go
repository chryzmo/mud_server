package mudserver

import (
	"math/rand/v2"
	"strings"
	"time"

	"mud_server/src/internal/worldfile"
)

/*
Ambient life and time of day (plans/interactive-world.md section 9). A room with ambient entries sends
its lines to the players in it, at a random gap within each entry's range, and only while someone is
there: an empty room keeps no schedule and costs a nil check per tick. A person's idle lines are the
same thing, written beside the person. One budget caps ambient lines per second across the whole
world, so a crowd of occupied rooms cannot flood the outbound queues (capacity rule 1). The time of
day comes from the real clock in the con's zone, and a room's day notes add a line to LOOK.
*/

const (
	ambientPerSecond = 20 // lines the whole world may send per second
	ambientBurst     = 40
)

type ambientEntry struct {
	min, max       time.Duration
	timeOfDay      string // "" for any time
	playersAtLeast int
	lines          []string
}

// roomAmbient is a room's ambient lines and day notes. Immutable, shared by a room's copies.
type roomAmbient struct {
	entries []ambientEntry
	notes   map[string]string // time of day -> the line LOOK adds
}

// ambientState is one room's schedule: when each entry is next due. Per room (and per copy), loop only.
type ambientState struct {
	next []time.Time
	last []int // the line each entry sent last, so it is not repeated at once
}

// buildAmbient turns the content's ambient entries, day notes and people's idle lines into each room's roomAmbient.
func buildAmbient(w *worldfile.World) map[string]*roomAmbient {
	out := map[string]*roomAmbient{}
	for _, key := range sortedKeys(w.Rooms) {
		r := w.Rooms[key]
		ra := &roomAmbient{notes: map[string]string{}}
		for _, a := range r.Ambient {
			e := ambientEntry{min: time.Duration(a.Every[0]) * time.Second, max: time.Duration(a.Every[1]) * time.Second, lines: trimLines(a.Lines)}
			if a.When != nil {
				e.timeOfDay, e.playersAtLeast = a.When.TimeOfDay, a.When.PlayersAtLeast
			}
			ra.entries = append(ra.entries, e)
		}
		for _, pk := range sortedKeys(r.People) {
			pe := r.People[pk]
			if len(pe.Idle) == 0 {
				continue
			}
			lo, hi := worldfile.DefaultIdleMin, worldfile.DefaultIdleMax
			if len(pe.IdleEvery) == 2 {
				lo, hi = pe.IdleEvery[0], pe.IdleEvery[1]
			}
			ra.entries = append(ra.entries, ambientEntry{min: time.Duration(lo) * time.Second, max: time.Duration(hi) * time.Second, lines: trimLines(pe.Idle)})
		}
		for phase, note := range r.DayNotes {
			if note = strings.TrimSpace(note); note != "" {
				ra.notes[phase] = note
			}
		}
		if len(ra.entries) > 0 || len(ra.notes) > 0 {
			out[key] = ra
		}
	}
	return out
}

func trimLines(in []string) []string {
	out := make([]string, 0, len(in))
	for _, l := range in {
		out = append(out, strings.Join(strings.Fields(l), " "))
	}
	return out
}

// timeOfDay is the phase of the day at the con: dawn 06-08, day 08-18, dusk 18-20, night 20-06, Eastern.
func timeOfDay(now time.Time) string {
	switch h := now.In(easternZone).Hour(); {
	case h >= 6 && h < 8:
		return "dawn"
	case h >= 8 && h < 18:
		return "day"
	case h >= 18 && h < 20:
		return "dusk"
	}
	return "night"
}

// dayNote is the line LOOK adds for the current time of day, or "".
func (r *Room) dayNote(now time.Time) string {
	if r.ambient == nil {
		return ""
	}
	return r.ambient.notes[timeOfDay(now)]
}

func (w *World) ambientGapFor(e *ambientEntry) time.Duration {
	if w.ambientGap != nil {
		return w.ambientGap(e.min, e.max)
	}
	span := int64((e.max - e.min) / time.Second)
	return e.min + time.Duration(rand.Int64N(span+1))*time.Second
}

// ambientTick sends a room's due lines. It is called for every room each world tick, so the first
// lines are the cheap checks: no ambient, or no players, return at once. Loop only.
func (w *World) ambientTick(r *Room, now time.Time) {
	if r.ambient == nil || len(r.ambient.entries) == 0 {
		return
	}
	if len(r.UserMap) == 0 {
		r.ambientState = nil // nothing runs in an empty room; the schedule starts afresh when someone comes
		return
	}
	entries := r.ambient.entries
	st := r.ambientState
	if st == nil {
		st = &ambientState{next: make([]time.Time, len(entries)), last: make([]int, len(entries))}
		for i := range entries {
			st.next[i] = now.Add(w.ambientGapFor(&entries[i])) // the first line comes after a gap, not on arrival
			st.last[i] = -1
		}
		r.ambientState = st
		return
	}
	phase := timeOfDay(now)
	for i := range entries {
		e := &entries[i]
		if now.Before(st.next[i]) {
			continue
		}
		st.next[i] = now.Add(w.ambientGapFor(e))
		if (e.timeOfDay != "" && e.timeOfDay != phase) || len(r.UserMap) < e.playersAtLeast {
			continue
		}
		if w.ambientBudget.capacity == 0 {
			w.ambientBudget = newLimiter(ambientPerSecond, ambientBurst, now)
		}
		if ok, _ := w.ambientBudget.allow(now); !ok {
			continue // the world is busy; skip this one
		}
		pick := rand.IntN(len(e.lines))
		if len(e.lines) > 1 && pick == st.last[i] {
			pick = (pick + 1) % len(e.lines)
		}
		st.last[i] = pick
		r.Broadcast(e.lines[pick], "")
	}
}
