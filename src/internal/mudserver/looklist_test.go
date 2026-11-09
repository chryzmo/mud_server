package mudserver

import (
	"strings"
	"testing"
)

// A player's name in "The players here are:" carries their name colour and badge, so the client can draw it as
// it does in a SAY. A player wearing neither gets a plain name.
func TestThePlayersListCarriesEachNamesColourAndBadge(t *testing.T) {
	room, w := mobRoom()
	ann := mobPlayer(room, w, "ann")
	bob := mobPlayer(room, w, "bob")
	cy := mobPlayer(room, w, "cy")
	ann.st().active["color"], ann.st().active["badge"] = "crimson", "crown"
	bob.st().active["badge"] = "lantern"
	_ = cy

	got := room.lookDescription(ann) // ann looking at the room sees her own look too
	for _, want := range []string{
		"\x1b[1;34mann\x1b[0m\x1b]look;crimson;crown\a (you), level 1\n",
		"\x1b[1;34mbob\x1b[0m\x1b]look;;lantern\a, level 1\n",
		"\x1b[1;34mcy\x1b[0m, level 1\n", // wearing nothing: no marker
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the players list lacks %q in %q", want, got)
		}
	}
}

// You are listed first with "(you)", then everyone else by name, each with their level.
func TestThePlayersListPutsYouFirstThenOthersByName(t *testing.T) {
	room, w := mobRoom()
	ann := mobPlayer(room, w, "ann")
	cy := mobPlayer(room, w, "cy")
	bob := mobPlayer(room, w, "Bob")
	_, _ = ann, bob

	got := room.lookDescription(cy)
	i := strings.Index(got, "The players here are:\n")
	lines := strings.Split(strings.TrimSpace(got[i:]), "\n")[1:4]
	want := []string{
		"\x1b[1;34mcy\x1b[0m (you), level 1",
		"\x1b[1;34mann\x1b[0m, level 1",
		"\x1b[1;34mBob\x1b[0m, level 1", // by name, ignoring case
	}
	for n := range want {
		if lines[n] != want[n] {
			t.Errorf("line %d = %q, want %q (all: %q)", n, lines[n], want[n], lines)
		}
	}
	// Someone looking from outside the room (no viewer) sees no "(you)".
	if strings.Contains(room.LookDescription(), "(you)") {
		t.Error("a look with no viewer says (you)")
	}
}
