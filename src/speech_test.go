package main

import (
	"strings"
	"testing"
)

// Spoken words reach the client in colour codes (cyan for what you said, magenta for what a character in the
// world says), so the client can tell them apart. The text around them is left plain.

func TestYourOwnSpeechIsColouredAndTheRestIsNot(t *testing.T) {
	t.Parallel()
	wsURL := startTestServer(t)
	conn := passwordPlayer(t, wsURL, "talker")
	send(t, conn, "SAY hello there")
	got := expect(t, conn, "You say: ")
	if want := "You say: \x1b[1;36m\"hello there\"\x1b[0m"; got != want {
		t.Errorf("own SAY = %q, want %q", got, want)
	}
	send(t, conn, "SAY sneaky \x1b[0m\x1b[1;31mred")
	if got := expect(t, conn, "sneaky"); strings.Count(got, "\x1b") != 2 {
		t.Errorf("an escape typed into SAY got through: %q", got)
	}
}

func TestACharactersSpokenWordsAreColoured(t *testing.T) {
	t.Parallel()
	wsURL := cafeServer(t, cafeWorld, nil)
	conn := cafeVisitor(t, wsURL, "visitor")
	send(t, conn, "TALK barista")
	got := expect(t, conn, "Welcome in")
	if !strings.Contains(got, "\x1b[1;35m\"Welcome in,\"\x1b[0m says Mira.") {
		t.Errorf("greeting = %q, want the quoted words in the magenta code and the narration plain", got)
	}
	send(t, conn, "ASK barista ABOUT coffee")
	if got := expect(t, conn, "Dark, strong"); !strings.Contains(got, "\x1b[1;35m\"Dark, strong, and free.\"\x1b[0m") {
		t.Errorf("topic answer = %q", got)
	}
	send(t, conn, "ASK barista ABOUT nonsense")
	if got := expect(t, conn, "No idea"); !strings.Contains(got, "Mira laughs. \x1b[1;35m\"No idea!\"\x1b[0m") {
		t.Errorf("shrug = %q", got)
	}
}
