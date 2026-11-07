package mudserver

import (
	"strings"
	"testing"
	"time"

	"mud_server/src/internal/worldfile"
)

func at(h, m int) time.Time { return time.Date(2027, 1, 8, h, m, 0, 0, easternZone) }

func TestTimeOfDayFollowsTheEasternClock(t *testing.T) {
	for _, tc := range []struct {
		h    int
		want string
	}{{0, "night"}, {5, "night"}, {6, "dawn"}, {7, "dawn"}, {8, "day"}, {17, "day"}, {18, "dusk"}, {19, "dusk"}, {20, "night"}, {23, "night"}} {
		if got := timeOfDay(at(tc.h, 30)); got != tc.want {
			t.Errorf("%02d:30 = %s, want %s", tc.h, got, tc.want)
		}
	}
	if got := timeOfDay(at(9, 0).UTC()); got != "day" { // the same instant in UTC
		t.Errorf("09:00 Eastern in UTC = %s", got)
	}
}

// listener is a player whose frames pile up in a channel.
func listener(name string) *MudPlayer {
	return &MudPlayer{Username: name, Session: &Session{out: make(chan []byte, 512), closed: make(chan struct{})}}
}

func drain(p *MudPlayer) (lines []string) {
	for {
		select {
		case f := <-p.Session.out:
			lines = append(lines, string(f))
		default:
			return
		}
	}
}

func ambientRoom(p *MudPlayer, entries ...ambientEntry) *Room {
	r := &Room{Name: "Hall", UserMap: map[string]*MudPlayer{}, ambient: &roomAmbient{entries: entries, notes: map[string]string{"night": "The lamps are low."}}}
	if p != nil {
		r.UserMap[p.Username] = p
	}
	return r
}

func fixedGap(w *World) { w.ambientGap = func(lo, hi time.Duration) time.Duration { return lo } }

func TestAmbientLinesComeAfterAGapAndOnlyWhileSomeoneIsThere(t *testing.T) {
	w := &World{}
	fixedGap(w)
	p := listener("ann")
	r := ambientRoom(nil, ambientEntry{min: 30 * time.Second, max: 30 * time.Second, lines: []string{"A bell rings."}})
	now := at(12, 0)

	w.ambientTick(r, now) // empty: no schedule at all
	if r.ambientState != nil {
		t.Fatal("an empty room kept a schedule")
	}
	r.UserMap["ann"] = p
	w.ambientTick(r, now) // someone arrives: the schedule starts, nothing is said yet
	w.ambientTick(r, now.Add(29*time.Second))
	if got := drain(p); len(got) != 0 {
		t.Fatalf("a line before its gap: %v", got)
	}
	w.ambientTick(r, now.Add(30*time.Second))
	if got := drain(p); len(got) != 1 || !strings.Contains(got[0], "A bell rings.") {
		t.Fatalf("at the gap: %v", got)
	}
	w.ambientTick(r, now.Add(45*time.Second))
	if got := drain(p); len(got) != 0 {
		t.Fatalf("again too soon: %v", got)
	}
	delete(r.UserMap, "ann") // everyone leaves: the schedule goes
	w.ambientTick(r, now.Add(50*time.Second))
	if r.ambientState != nil {
		t.Error("the schedule outlived the players")
	}
}

func TestAmbientEntriesObeyTimeOfDayAndPlayerCount(t *testing.T) {
	w := &World{}
	fixedGap(w)
	p := listener("ann")
	r := ambientRoom(p,
		ambientEntry{min: time.Second, max: time.Second, timeOfDay: "night", lines: []string{"night line"}},
		ambientEntry{min: time.Second, max: time.Second, playersAtLeast: 2, lines: []string{"crowd line"}},
		ambientEntry{min: time.Second, max: time.Second, lines: []string{"plain line"}},
	)
	day := at(12, 0)
	w.ambientTick(r, day)
	w.ambientTick(r, day.Add(time.Second))
	got := strings.Join(drain(p), "|")
	if !strings.Contains(got, "plain line") || strings.Contains(got, "night line") || strings.Contains(got, "crowd line") {
		t.Fatalf("by day with one player: %q", got)
	}
	night := at(23, 0)
	w.ambientTick(r, night)
	got = strings.Join(drain(p), "|")
	if !strings.Contains(got, "night line") || strings.Contains(got, "crowd line") {
		t.Fatalf("at night: %q", got)
	}
	r.UserMap["bob"] = listener("bob")
	w.ambientTick(r, night.Add(2*time.Second))
	if got = strings.Join(drain(p), "|"); !strings.Contains(got, "crowd line") {
		t.Fatalf("with two players: %q", got)
	}
}

func TestAmbientDoesNotRepeatALineAtOnce(t *testing.T) {
	w := &World{}
	fixedGap(w)
	p := listener("ann")
	r := ambientRoom(p, ambientEntry{min: time.Second, max: time.Second, lines: []string{"one", "two"}})
	now := at(12, 0)
	w.ambientTick(r, now)
	var last string
	for i := 1; i <= 40; i++ {
		w.ambientTick(r, now.Add(time.Duration(i)*time.Second))
		for _, l := range drain(p) {
			if l == last {
				t.Fatalf("%q twice in a row", l)
			}
			last = l
		}
	}
	if last == "" {
		t.Fatal("no lines at all")
	}
}

func TestAWorldWideBudgetCapsAmbientLines(t *testing.T) {
	w := &World{}
	fixedGap(w)
	now := at(12, 0)
	var rooms []*Room
	var players []*MudPlayer
	for i := 0; i < 200; i++ {
		p := listener("p")
		players = append(players, p)
		rooms = append(rooms, ambientRoom(p, ambientEntry{min: time.Second, max: time.Second, lines: []string{"hum"}}))
	}
	for _, r := range rooms {
		w.ambientTick(r, now)
	}
	for _, r := range rooms {
		w.ambientTick(r, now.Add(time.Second)) // every room is due in the same instant
	}
	sent := 0
	for _, p := range players {
		sent += len(drain(p))
	}
	if sent == 0 || sent > ambientBurst+ambientPerSecond {
		t.Errorf("%d lines in one instant from 200 rooms, want at most about %d", sent, ambientBurst)
	}
}

func TestDayNoteIsTheLineForTheCurrentPhase(t *testing.T) {
	r := ambientRoom(nil)
	if got := r.dayNote(at(22, 0)); got != "The lamps are low." {
		t.Errorf("night note = %q", got)
	}
	if got := r.dayNote(at(12, 0)); got != "" {
		t.Errorf("day note = %q, want none", got)
	}
	if got := (&Room{}).dayNote(at(22, 0)); got != "" {
		t.Errorf("a room without ambient has note %q", got)
	}
}

func TestBuildAmbientMergesRoomEntriesPeopleIdlesAndNotes(t *testing.T) {
	w := &worldfile.World{Rooms: map[string]worldfile.Room{
		"hall": {
			Ambient:  []worldfile.Ambient{{Every: []int{60, 120}, When: &worldfile.AmbientWhen{TimeOfDay: "night", PlayersAtLeast: 2}, Lines: []string{"  A  bell\nrings. "}}},
			DayNotes: map[string]string{"night": " Lamps low. ", "day": ""},
			People: map[string]worldfile.Person{
				"bell":  {Idle: []string{"Bell polishes a key."}},
				"quiet": {},
				"tune":  {Idle: []string{"Tune hums."}, IdleEvery: []int{30, 40}},
			},
		},
		"empty": {},
	}}
	got := buildAmbient(w)
	if len(got) != 1 || got["hall"] == nil {
		t.Fatalf("rooms with ambient = %v", got)
	}
	h := got["hall"]
	if len(h.entries) != 3 || h.notes["night"] != "Lamps low." || len(h.notes) != 1 {
		t.Fatalf("hall = %+v", h)
	}
	if e := h.entries[0]; e.min != time.Minute || e.max != 2*time.Minute || e.timeOfDay != "night" || e.playersAtLeast != 2 || e.lines[0] != "A bell rings." {
		t.Errorf("room entry = %+v", e)
	}
	if e := h.entries[1]; e.min != 120*time.Second || e.max != 300*time.Second {
		t.Errorf("a person's default idle gap = %v-%v", e.min, e.max)
	}
	if e := h.entries[2]; e.min != 30*time.Second || e.max != 40*time.Second {
		t.Errorf("a person's own idle gap = %v-%v", e.min, e.max)
	}
}
