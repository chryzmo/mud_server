package main

import (
	"strings"
	"testing"
	"time"

	mudserver "mud_server/src/internal/mudserver"
)

// JOURNAL, HINT and MAP (plans/interactive-world.md section 8), played in testdata/puzzle-world.

const hintWait = 2100 * time.Millisecond // a little over the cooldown that stops a double tap burning a hint

func TestJournalShowsOnlyWhatThePlayerHasLearned(t *testing.T) {
	t.Parallel()
	conn := puzzler(t, puzzleServer(t, nil), "scribe")
	send(t, conn, "JOURNAL")
	out := expect(t, conn, "Rooms discovered: 1 of 6.")
	if !strings.Contains(out, "The Hall Clocks") || !strings.Contains(out, "The Hall hums, and two clocks tick out of step.") {
		t.Errorf("journal = %q, want the clocks puzzle and its first step", out)
	}
	if strings.Contains(out, "The Echo") || strings.Contains(out, "struck three") {
		t.Errorf("journal = %q mentions what the player has not reached", out)
	}

	turns(t, conn, "TURN first clock", 3)
	turns(t, conn, "TURN second clock", 3)
	expect(t, conn, "Both clocks strike three at once.")
	send(t, conn, "JOURNAL")
	out = expect(t, conn, "Rooms discovered")
	for _, want := range []string{"Both clocks struck three together. (solved)", "The Echo", "The clocks agree and something stirs."} {
		if !strings.Contains(out, want) {
			t.Errorf("journal = %q, want %q", out, want)
		}
	}
}

func TestHintsComeOneTierAtATimeAndFollowTheLastPuzzle(t *testing.T) {
	t.Parallel()
	conn := puzzler(t, puzzleServer(t, nil), "stuck")
	send(t, conn, "HINT")
	expect(t, conn, "Hint 1 of 3 for The Hall Clocks: The clocks can be turned.")
	send(t, conn, "HINT") // a double tap shows the same one again
	expect(t, conn, "Hint 1 of 3")
	time.Sleep(hintWait)
	send(t, conn, "HINT")
	expect(t, conn, "Hint 2 of 3 for The Hall Clocks")
	time.Sleep(hintWait)
	send(t, conn, "HINT clocks")
	if out := expect(t, conn, "Hint 3 of 3 for The Hall Clocks: Turn each clock until it says 3."); strings.Contains(out, "HINT again") {
		t.Errorf("the last hint offers another: %q", out)
	}
	time.Sleep(hintWait)
	send(t, conn, "HINT")
	expect(t, conn, "Hint 3 of 3") // it stays on the last

	// Solving the clocks moves HINT on to the puzzle just opened.
	turns(t, conn, "TURN first clock", 3)
	turns(t, conn, "TURN second clock", 3)
	expect(t, conn, "Both clocks strike three at once.")
	send(t, conn, "HINT")
	expect(t, conn, "Hint 1 of 2 for The Echo: Something answers if you speak.")
	send(t, conn, "HINT clocks") // solved already
	expect(t, conn, "You solved The Hall Clocks already.")
	send(t, conn, "HINT nonsense")
	expect(t, conn, "Which puzzle?")
}

func TestHintWithNothingBegunSaysSo(t *testing.T) {
	t.Parallel()
	// A world with no puzzles at all: the plain sample world.
	conn := passwordPlayer(t, startTestServer(t), "plain")
	send(t, conn, "HINT")
	expect(t, conn, "You have no puzzle underway.")
	send(t, conn, "JOURNAL")
	expect(t, conn, "Nothing yet.")
	send(t, conn, "MAP")
	expect(t, conn, "Discovered 0 of 0 rooms.")
}

func TestMapNamesOnlyRoomsTheyHaveBeenIn(t *testing.T) {
	t.Parallel()
	conn := puzzler(t, puzzleServer(t, nil), "walker")
	send(t, conn, "MAP")
	out := expect(t, conn, "Discovered 1 of 6 rooms.")
	if !strings.Contains(out, "You are in 'Hall' (Clock Hall).") || !strings.Contains(out, "Clock Hall (1 of 1): Hall") {
		t.Errorf("map = %q", out)
	}
	if strings.Contains(out, "Gallery") || strings.Contains(out, "Upper Works") {
		t.Errorf("map = %q names a place the player has not been", out)
	}
	send(t, conn, "MOVE N")
	expect(t, conn, "You are in 'Gallery'")
	send(t, conn, "MAP")
	out = expect(t, conn, "Discovered 2 of 6 rooms.")
	if !strings.Contains(out, "Upper Works (1 of 3): Gallery") || strings.Contains(out, "Tower") {
		t.Errorf("map = %q", out)
	}
}

func TestDiscoveryAndHintsSurviveARestart(t *testing.T) {
	t.Parallel()
	url := testDatabaseURL(t)
	resetDatabase(t, url)
	wsA, serverA := serveTestServer(t, url, func(c *mudserver.Configuration) { c.World.ContentDir = puzzleDir })
	conn := puzzler(t, wsA, "veteran")
	send(t, conn, "MOVE N")
	expect(t, conn, "You are in 'Gallery'")
	send(t, conn, "HINT")
	expect(t, conn, "Hint 1 of 3")
	send(t, conn, "MOVE S") // back in the Hall, where a new session picks the character up
	expect(t, conn, "You are in 'Hall'")
	eventually(t, "state saved", "SELECT count(*)::text FROM player_state WHERE kind IN ('visited','hint')", func(v string) bool { return v == "3" })
	conn.Close()
	serverA.Shutdown(t.Context())

	wsB, _ := serveTestServer(t, url, func(c *mudserver.Configuration) { c.World.ContentDir = puzzleDir })
	again := loginAgain(t, wsB, "veteran")
	send(t, again, "MAP")
	expect(t, again, "Discovered 2 of 6 rooms.")
	send(t, again, "HINT")
	expect(t, again, "Hint 2 of 3") // the tier was kept
}
