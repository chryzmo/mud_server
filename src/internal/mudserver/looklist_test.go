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
		"\x1b[1;34mann\x1b[0m\x1b]look;crimson;crown\a",
		"\x1b[1;34mbob\x1b[0m\x1b]look;;lantern\a",
		"\x1b[1;34mcy\x1b[0m\n", // wearing nothing: no marker
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the players list lacks %q in %q", want, got)
		}
	}
}
