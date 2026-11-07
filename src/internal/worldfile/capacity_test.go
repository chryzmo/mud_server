package worldfile

import (
	"strings"
	"testing"
)

func TestRoomCapacityDefaultsAndProblems(t *testing.T) {
	w, res, _ := ValidateDir(write(t, map[string]string{"w.yaml": `
start_room: a
rooms:
  a: {name: A, description: d, exits: {E: b}, capacity: 150}
  b: {name: B, description: d, exits: {W: a, E: c}, capacity: 20, quiet_at: 8}
  c: {name: C, description: d, exits: {W: b, E: d}}
  d: {name: D, description: d, exits: {W: c}, quiet_at: 10}
`}))
	if len(res.Errors) != 0 {
		t.Fatalf("errors %v", res.Errors)
	}
	want := map[string]RoomCrowd{"a": {150, 30}, "b": {20, 8}, "c": {0, 0}, "d": {0, 10}}
	for k, c := range want {
		if got := w.Rooms[k].ResolvedCrowd(); got != c {
			t.Errorf("room %s crowd = %+v, want %+v", k, got, c)
		}
	}
	small := Room{Capacity: 12}
	if got := small.ResolvedCrowd(); got.QuietAt != 12 {
		t.Errorf("a room of 12 should go quiet at 12, got %+v", got)
	}

	_, res, _ = ValidateDir(write(t, map[string]string{"w.yaml": `
start_room: a
rooms:
  a: {name: A, description: d, exits: {E: b}, capacity: 500}
  b: {name: B, description: d, exits: {W: a, E: c}, capacity: -1}
  c: {name: C, description: d, exits: {W: b, E: d}, capacity: 10, quiet_at: 11}
  d: {name: D, description: d, exits: {W: c}, quiet_at: 99}
`}))
	var all []string
	for _, p := range res.Errors {
		all = append(all, p.String())
	}
	text := strings.Join(all, "\n")
	for _, want := range []string{"rooms.a.capacity", "rooms.b.capacity", "rooms.c.quiet_at", "rooms.d.quiet_at"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing a problem for %s in:\n%s", want, text)
		}
	}
}

func TestCapacityIsRenderedForReview(t *testing.T) {
	out := render(t, map[string]string{"w.yaml": "start_room: a\nrooms:\n  a: {name: A, description: d, capacity: 150}\n"})
	if !strings.Contains(out, "holds 150, quiet from 30") {
		t.Errorf("overview is missing the capacity\n%s", out)
	}
}
