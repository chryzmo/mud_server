package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	mudserver "mud_server/src/internal/mudserver"
)

// Rules, gates and puzzle state (plans/interactive-world.md), played in testdata/puzzle-world.

const puzzleDir = "testdata/puzzle-world"

func puzzleServer(t *testing.T, configure func(*mudserver.Configuration)) string {
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		c.World.ContentDir = puzzleDir
		if configure != nil {
			configure(c)
		}
	})
	return wsURL
}

// puzzler logs in a new password player and waits until they are in the Hall, past the first-arrival text.
func puzzler(t *testing.T, wsURL, name string) *websocket.Conn {
	t.Helper()
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")
	loginNew(t, conn, name)
	expect(t, conn, "Welcome back, "+name+"!")
	expect(t, conn, "You are in 'Hall'")
	expect(t, conn, "The Hall hums as you arrive.")
	return conn
}

// untilExits reads frames until a room frame lists dir among its exits.
func untilExits(t *testing.T, conn *websocket.Conn, dir string) {
	t.Helper()
	for i := 0; i < 40; i++ {
		typ, m := readMessage(t, conn)
		if typ != "room" {
			continue
		}
		for _, e := range m["exits"].([]any) {
			if e == dir {
				return
			}
		}
	}
	t.Fatalf("no room frame offered the %s exit", dir)
}

func turns(t *testing.T, conn *websocket.Conn, line string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		send(t, conn, line)
	}
}

func TestCountersWrapAndAppearInText(t *testing.T) {
	conn := puzzler(t, puzzleServer(t, nil), "clockwise")
	send(t, conn, "X first clock")
	expect(t, conn, "The first clock says 0.") // a counter nobody touched reads as 0
	send(t, conn, "TURN first clock")
	expect(t, conn, "creak round to 1 o'clock.")
	turns(t, conn, "TURN first clock", 11) // 12
	send(t, conn, "TURN first clock")      // wraps to 1
	expect(t, conn, "creak round to 12 o'clock.")
	expect(t, conn, "creak round to 1 o'clock.")
	send(t, conn, "X first clock")
	expect(t, conn, "The first clock says 1.")
	send(t, conn, "X second clock")
	expect(t, conn, "The second clock says 0.") // its own counter
}

func TestSolvingAPuzzleOpensAGateForThatPlayerOnly(t *testing.T) {
	wsURL := puzzleServer(t, nil)
	solver := puzzler(t, wsURL, "solver")
	other := puzzler(t, wsURL, "other")
	expect(t, solver, "other") // arrival of the second player

	send(t, solver, "MOVE U")
	expect(t, solver, "You cannot move there.") // hidden: it is not there yet
	send(t, solver, "LOOK")
	if out := expect(t, solver, "Sealed: [D]"); strings.Contains(out, "[U]") {
		t.Errorf("a hidden gate is shown: %q", out)
	}

	turns(t, solver, "TURN first clock", 3)
	turns(t, solver, "TURN second clock", 3)
	expect(t, solver, "Both clocks strike three at once.")
	untilExits(t, solver, "U")
	send(t, solver, "MOVE U")
	expect(t, solver, "You are in 'Tower'")
	send(t, solver, "MOVE D")
	expect(t, solver, "You are in 'Hall'")

	// The other player has solved nothing: still no stair, and the puzzle starts from zero for them.
	send(t, other, "MOVE U")
	expect(t, other, "You cannot move there.")
	send(t, other, "X first clock")
	expect(t, other, "The first clock says 0.")
}

func TestALockedGateSaysWhyAndARiddleByWordsOpensIt(t *testing.T) {
	conn := puzzler(t, puzzleServer(t, nil), "riddler")
	send(t, conn, "LOOK")
	expect(t, conn, "Sealed: [D]")
	send(t, conn, "MOVE D")
	expect(t, conn, "The trapdoor is shut tight.")
	send(t, conn, "SAY something else entirely")
	expect(t, conn, `You say: "something else entirely"`)
	send(t, conn, "MOVE D")
	expect(t, conn, "The trapdoor is shut tight.")
	send(t, conn, "SAY An Echo!") // case and punctuation do not matter
	expect(t, conn, "A bookcase swings aside, revealing a trapdoor.")
	send(t, conn, "MOVE D")
	expect(t, conn, "You are in 'Cellar'")
	send(t, conn, "LOOK")
	if out := expect(t, conn, "You are in 'Cellar'"); strings.Contains(out, "Sealed") {
		t.Errorf("Sealed listed in a room with no sealed gate: %q", out)
	}
}

func TestSequencesGiveABoundTokenAndAWrongRunResets(t *testing.T) {
	conn := puzzler(t, puzzleServer(t, nil), "ringer")
	send(t, conn, "RING red bell")
	expect(t, conn, "A bright note.")
	send(t, conn, "RING blue bell")
	expect(t, conn, "A sleepy note.")
	expect(t, conn, "You receive \x1b[1;32mHONEYCOMB\x1b[0m.")
	expect(t, conn, "The bees leave a honeycomb.")

	send(t, conn, "DROP honeycomb")
	expect(t, conn, "The HONEYCOMB is bound to you; you cannot drop it.")
	send(t, conn, "X my honeycomb")
	expect(t, conn, "A dripping honeycomb.")
	send(t, conn, "PULL rope") // a rule that needs the token
	expect(t, conn, "The honeycomb hums.")

	// Three wrong notes make a long sequence; another rule forgets it, so a later right run still works.
	turns(t, conn, "RING blue bell", 3)
	send(t, conn, "RING fountain")
	send(t, conn, "RING red bell")
	send(t, conn, "RING blue bell")
	expect(t, conn, "The bees leave a honeycomb.") // the sequence was reset in between
	send(t, conn, "INVENTORY")
	if out := expect(t, conn, "HONEYCOMB"); !strings.Contains(out, "1x HONEYCOMB") {
		t.Errorf("a bound token must not stack: %q", out)
	}
}

func TestEnteringARoomRunsItsRuleOnce(t *testing.T) {
	conn := puzzler(t, puzzleServer(t, nil), "visitor") // the first arrival already showed the text
	send(t, conn, "MOVE E")
	expect(t, conn, "You are in 'Study'")
	send(t, conn, "MOVE W")
	expect(t, conn, "You are in 'Hall'")
	if text, again := expectWithin(conn, "hums as you arrive", 300*time.Millisecond); again {
		t.Errorf("the arrival rule ran twice: %q", text)
	}
}

func TestGivingAnItemToAPerson(t *testing.T) {
	conn := puzzler(t, puzzleServer(t, nil), "giver")
	send(t, conn, "GIVE ticket TO porter")
	expect(t, conn, "You are not carrying that.")
	send(t, conn, "MOVE N")
	expect(t, conn, "You are in 'Gallery'")
	send(t, conn, "SEARCH booth")
	expect(t, conn, "You receive \x1b[1;32mTICKET\x1b[0m.")
	send(t, conn, "SEARCH booth") // the rule needs that you do not already have it
	expect(t, conn, "Nothing happens.")
	send(t, conn, "MOVE S")
	expect(t, conn, "You are in 'Hall'")

	send(t, conn, "GIVE TICKET")
	expect(t, conn, "Give an item to someone.")
	send(t, conn, "GIVE ticket TO fountain") // no rule for that
	expect(t, conn, "fountain does not want that.")
	send(t, conn, "GIVE ticket to the Porter")
	expect(t, conn, "Porter Bellweather beams and hands you a brass key.")
	send(t, conn, "INVENTORY")
	if out := expect(t, conn, "BRASS KEY"); strings.Contains(out, "TICKET") {
		t.Errorf("the ticket should be gone: %q", out)
	}
	send(t, conn, "GIVE brass key TO porter")
	expect(t, conn, "Porter does not want that.")
}

func TestUsingAnItemOnAThingTeleportsAndTellsTheRoom(t *testing.T) {
	wsURL := puzzleServer(t, nil)
	conn := puzzler(t, wsURL, "traveller")
	watcher := puzzler(t, wsURL, "watcher")
	expect(t, conn, "watcher")
	send(t, conn, "MOVE N")
	expect(t, conn, "You are in 'Gallery'")
	send(t, conn, "SEARCH booth")
	expect(t, conn, "You receive \x1b[1;32mTICKET\x1b[0m.")
	send(t, conn, "MOVE S")
	expect(t, conn, "You are in 'Hall'")
	send(t, conn, "GIVE ticket TO porter")
	expect(t, conn, "hands you a brass key")

	send(t, conn, "USE brass key ON fountain")
	expect(t, watcher, "traveller unlocks something in the fountain.")
	expect(t, conn, "You are in 'Tower'")
	send(t, conn, "USE fountain") // nothing to use it with: a quiet "nothing happens"
	expect(t, conn, "You do not see that here.")
}

func TestPressurePlatesNeedTwoPlayersOrAWeight(t *testing.T) {
	wsURL := puzzleServer(t, nil)
	a := puzzler(t, wsURL, "alpha")
	b := puzzler(t, wsURL, "bravo")
	elsewhere := puzzler(t, wsURL, "elsewhere") // stays in the hall
	for _, c := range []*websocket.Conn{a, b} {
		send(t, c, "MOVE E")
		expect(t, c, "You are in 'Study'")
	}

	// One plate alone does nothing.
	send(t, a, "PRESS left plate")
	expect(t, a, "The left plate sinks and holds.")
	send(t, a, "MOVE D")
	expect(t, a, "The great doors are shut.")

	// A second player on the other plate opens the doors for both of them.
	send(t, b, "PRESS right plate")
	expect(t, b, "The right plate sinks and holds.")
	expect(t, b, "Both plates hold. The great doors grind open.")
	expect(t, a, "Both plates hold. The great doors grind open.")
	send(t, a, "MOVE D")
	expect(t, a, "You are in 'Crypt'")
	send(t, b, "MOVE D")
	expect(t, b, "You are in 'Crypt'")

	// Someone who was not in the room has solved nothing.
	send(t, elsewhere, "MOVE E")
	expect(t, elsewhere, "You are in 'Study'")
	send(t, elsewhere, "MOVE D")
	expect(t, elsewhere, "The great doors are shut.")

	// The solo path: hold one plate, weigh down the other.
	send(t, elsewhere, "PRESS left plate")
	expect(t, elsewhere, "The left plate sinks and holds.")
	send(t, elsewhere, "SEARCH rubble")
	expect(t, elsewhere, "You receive \x1b[1;32mANVIL WEIGHT\x1b[0m.")
	send(t, elsewhere, "PUT anvil weight ON right plate")
	expect(t, elsewhere, "The weight holds the right plate down.")
	expect(t, elsewhere, "Both plates hold. The great doors grind open.")
	send(t, elsewhere, "INVENTORY")
	if out := expect(t, elsewhere, "inventory"); strings.Contains(out, "ANVIL WEIGHT") {
		t.Errorf("the weight should have been used up: %q", out)
	}
	send(t, elsewhere, "MOVE D")
	expect(t, elsewhere, "You are in 'Crypt'")
}

func TestVerbRefusals(t *testing.T) {
	conn := puzzler(t, puzzleServer(t, nil), "fumbler")
	for _, tc := range []struct{ line, want string }{
		{"TURN fountain", "Nothing happens."},
		{"TURN nothing", "You do not see that here."},
		{"TURN", "Turn something."},
		{"RING clock", "Which do you mean: first clock, second clock?"},
		{"SET fountain TO 7", "The fountain gurgles seven times."},
		{"SET fountain TO 8", "Nothing happens."},
		{"SET fountain", "Set something to a value."},
		{"USE brass key ON fountain", "You are not carrying that."},
		{"PUT", "Put an item somewhere."},
		{"THROW gold", "Throw an item at or into something."},
		{"PULL rope", "Nothing happens."}, // the rope's rule needs the honeycomb
		{"GIVE", "Give an item to someone."},
	} {
		send(t, conn, tc.line)
		expect(t, conn, tc.want)
	}
}

func TestPuzzleVerbsAreInHelp(t *testing.T) {
	conn := puzzler(t, puzzleServer(t, nil), "reader")
	send(t, conn, "HELP")
	out := expect(t, conn, "Available commands")
	for _, verb := range []string{"TURN", "PULL", "PUSH", "PRESS", "RING", "LIGHT", "OPEN", "SEARCH", "READ", "USE", "GIVE", "PUT", "THROW", "SET"} {
		if !strings.Contains(out, verb) {
			t.Errorf("HELP is missing %s", verb)
		}
	}
}

func TestPuzzleStateSurvivesARestart(t *testing.T) {
	url := testDatabaseURL(t)
	resetDatabase(t, url)
	wsA, serverA := serveTestServer(t, url, func(c *mudserver.Configuration) { c.World.ContentDir = puzzleDir })
	conn := puzzler(t, wsA, "keeper")
	turns(t, conn, "TURN first clock", 3)
	turns(t, conn, "TURN second clock", 3)
	expect(t, conn, "Both clocks strike three at once.")
	send(t, conn, "RING red bell") // half a sequence
	expect(t, conn, "A bright note.")
	send(t, conn, "SAY an echo")
	expect(t, conn, "A bookcase swings aside")
	// The writes are queued, not batched: wait for the last one rather than for a save interval.
	eventually(t, "state saved", "SELECT count(*)::text FROM player_state WHERE kind='gate'", func(v string) bool { return v == "2" })
	conn.Close()
	serverA.Shutdown(t.Context())

	wsB, _ := serveTestServer(t, url, func(c *mudserver.Configuration) { c.World.ContentDir = puzzleDir })
	again := dial(t, wsB)
	expect(t, again, "Welcome to the MUD!")
	send(t, again, "/LOGIN keeper secret")
	expect(t, again, "Welcome back, keeper!")
	expect(t, again, "You are in 'Hall'")
	if text, repeated := expectWithin(again, "hums as you arrive", 300*time.Millisecond); repeated {
		t.Errorf("the flag was lost: the first-arrival rule ran again: %q", text)
	}
	again.Close() // a second login works once the first session has gone

	conn3 := loginAgain(t, wsB, "keeper")
	send(t, conn3, "X first clock")
	expect(t, conn3, "The first clock says 3.") // the counter
	send(t, conn3, "MOVE U")
	expect(t, conn3, "You are in 'Tower'") // the gate
	send(t, conn3, "MOVE D")
	expect(t, conn3, "You are in 'Hall'")
	send(t, conn3, "MOVE D")
	expect(t, conn3, "You are in 'Cellar'") // the riddle's gate
	send(t, conn3, "MOVE U")
	expect(t, conn3, "You are in 'Hall'")
	send(t, conn3, "RING blue bell") // the half sequence from before the restart
	expect(t, conn3, "You receive \x1b[1;32mHONEYCOMB\x1b[0m.")
}

// loginAgain logs an existing password account in, waiting out the previous session being cleared.
func loginAgain(t *testing.T, wsURL, name string) *websocket.Conn {
	t.Helper()
	conn, msg := loginWhenFree(t, wsURL, name, "secret")
	if !strings.Contains(msg, "Welcome back") {
		t.Fatalf("login = %q", msg)
	}
	expect(t, conn, "You are in 'Hall'")
	return conn
}

func TestRulesAndStateWorkInACrowdCopy(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		c.World.ContentDir = "testdata/puzzle-world-areas"
		c.Crowd = mudserver.CrowdConfig{SoftCap: 1, MaxCopies: 5}
	})
	first := puzzler(t, wsURL, "first")
	second := puzzler(t, wsURL, "second") // the second copy of the Hall
	_ = first
	turns(t, second, "TURN first clock", 3)
	send(t, second, "X first clock")
	expect(t, second, "The first clock says 3.")
	send(t, second, "SAY an echo")
	expect(t, second, "A bookcase swings aside")
	send(t, second, "MOVE D")
	expect(t, second, "You are in 'Cellar'")
	send(t, second, "MOVE U")
	expect(t, second, "You are in 'Hall'")
}

func TestSayRulesIgnoreHowItIsWritten(t *testing.T) {
	wsURL := puzzleServer(t, nil)
	for i, phrase := range []string{"an echo", "AN ECHO", "  an   echo  ", "An echo.", `"an echo!"`} {
		conn := puzzler(t, wsURL, fmt.Sprintf("speaker%d", i))
		send(t, conn, "SAY "+phrase)
		expect(t, conn, "A bookcase swings aside")
		conn.Close()
	}
}
