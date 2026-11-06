package main

import (
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	mudserver "mud_server/src/internal/mudserver"
)

// EXAMINE and features (plans/interactive-world.md): scenery from content files, items, players.

const clockWorld = `
start_room: hall
rooms:
  hall:
    name: Hall
    description: A long hall.
    exits: {N: yard}
    items: {key: 1}
    features:
      clock-1:
        names: [first clock, clock]
        examine: "The first clock says three."
      clock-2:
        names: [second clock, clock]
        examine: "The second clock says nine."
      lever:
        names: [brass lever, lever]
        unlisted: true
        examine: "A brass lever, half hidden behind a banner."
  yard:
    name: Yard
    description: A yard.
    exits: {S: hall}
items:
  key:
    name: KEY
    description: A generic looking key.
`

// hallPlayer logs in a new password player and waits until they are in this world's start room.
func hallPlayer(t *testing.T, wsURL, name string) *websocket.Conn {
	t.Helper()
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")
	loginNew(t, conn, name)
	expect(t, conn, "Welcome back, "+name+"!")
	expect(t, conn, "You are in 'Hall'")
	return conn
}

func clockServer(t *testing.T, configure func(*mudserver.Configuration)) string {
	dir := contentDir(t, map[string]string{"w.yaml": clockWorld})
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		c.World.ContentDir = dir
		if configure != nil {
			configure(c)
		}
	})
	return wsURL
}

func TestLookListsTheThingsToExamineButNotTheUnlisted(t *testing.T) {
	wsURL := clockServer(t, nil)
	conn := hallPlayer(t, wsURL, "viewer")
	send(t, conn, "LOOK")
	out := expect(t, conn, "You notice: first clock, second clock.")
	if strings.Contains(out, "lever") {
		t.Errorf("an unlisted feature is in LOOK: %q", out)
	}
	send(t, conn, "MOVE N")
	if out := expect(t, conn, "You are in 'Yard'"); strings.Contains(out, "You notice") {
		t.Errorf("a room with no features has a notice line: %q", out)
	}
}

func TestExamineFindsThingsByNameAliasAndPrefix(t *testing.T) {
	wsURL := clockServer(t, nil)
	conn := hallPlayer(t, wsURL, "viewer")
	for _, tc := range []struct{ line, want string }{
		{"EXAMINE first clock", "The first clock says three."},
		{"examine SECOND CLOCK", "The second clock says nine."},
		{"X brass lever", "A brass lever, half hidden behind a banner."}, // examinable though unlisted
		{"X lever", "A brass lever"},                                     // an alias
		{"X bra", "A brass lever"},                                       // a unique prefix of three letters or more
		{"X fir", "The first clock says three."},
		{"EXAMINE the first clock", "The first clock says three."},
		{"LOOK AT lever", "A brass lever"},
		{"LOOK second clock", "The second clock says nine."},
		{"X key", "KEY"}, // an item on the floor, with its description
	} {
		send(t, conn, tc.line)
		expect(t, conn, tc.want)
	}
	send(t, conn, "X key")
	expect(t, conn, "A generic looking key.")
}

func TestExamineAsksWhichWhenSeveralFit(t *testing.T) {
	wsURL := clockServer(t, nil)
	conn := hallPlayer(t, wsURL, "viewer")
	send(t, conn, "X clock") // both clocks have the alias "clock"
	expect(t, conn, "Which do you mean: first clock, second clock?")
	send(t, conn, "X clo") // and a prefix does not break the tie
	expect(t, conn, "Which do you mean: first clock, second clock?")
	send(t, conn, "X cl") // too short for a prefix
	expect(t, conn, "You do not see that here.")
}

func TestExamineRefusals(t *testing.T) {
	wsURL := clockServer(t, nil)
	conn := hallPlayer(t, wsURL, "viewer")
	send(t, conn, "EXAMINE")
	expect(t, conn, "Examine what?")
	send(t, conn, "X dragon")
	expect(t, conn, "You do not see that here.")
	send(t, conn, "X my dragon")
	expect(t, conn, "You are not carrying that.")
	send(t, conn, "MOVE N")
	expect(t, conn, "You are in 'Yard'")
	send(t, conn, "X first clock") // features belong to their room
	expect(t, conn, "You do not see that here.")
}

func TestExamineItemsInThePackPlayersAndYourself(t *testing.T) {
	wsURL := clockServer(t, nil)
	conn := hallPlayer(t, wsURL, "viewer")
	other := hallPlayer(t, wsURL, "other")
	expect(t, conn, "other")
	send(t, conn, "GET KEY")
	expect(t, conn, "You pick up KEY.")
	send(t, conn, "X MY KEY")
	expect(t, conn, "A generic looking key.")
	send(t, conn, "X other")
	expect(t, conn, "other")
	send(t, conn, "X viewer")
	expect(t, conn, "That is you.")
	_ = other
	// Once picked up the key is no longer on the floor, but is still in the pack.
	send(t, conn, "X key")
	expect(t, conn, "A generic looking key.")
	send(t, other, "X key")
	expect(t, other, "You do not see that here.")
}

func TestExamineIsListedInHelp(t *testing.T) {
	wsURL := clockServer(t, nil)
	conn := hallPlayer(t, wsURL, "viewer")
	send(t, conn, "HELP")
	expect(t, conn, "EXAMINE")
}

func TestACopyOfARoomKeepsItsFeatures(t *testing.T) {
	dir := contentDir(t, map[string]string{"w.yaml": clockWorld + "areas:\n  main: {rooms: [hall, yard]}\n"})
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		c.World.ContentDir = dir
		c.Crowd = mudserver.CrowdConfig{SoftCap: 1, MaxCopies: 5}
	})
	hallPlayer(t, wsURL, "first")
	second := dial(t, wsURL)
	expect(t, second, "Welcome to the MUD!")
	loginNew(t, second, "second")
	expect(t, second, "You are in 'Hall'") // the second copy
	send(t, second, "X brass lever")
	expect(t, second, "A brass lever")
	send(t, second, "LOOK")
	expect(t, second, "You notice: first clock, second clock.")
}

func TestApplyingContentAgainUpdatesFeatures(t *testing.T) {
	url := testDatabaseURL(t)
	resetDatabase(t, url)
	dir := contentDir(t, map[string]string{"w.yaml": clockWorld})
	_, first := serveTestServer(t, url, withContent(dir))
	if got := queryString(t, "SELECT jsonb_array_length(featuresjson::jsonb)::text FROM room WHERE contentkey='hall'"); got != "3" {
		t.Errorf("features stored = %s, want 3", got)
	}
	first.Shutdown(t.Context())

	edited := contentDir(t, map[string]string{"w.yaml": strings.Replace(clockWorld, "The first clock says three.", "The first clock says four.", 1)})
	wsURL, _ := serveTestServer(t, url, withContent(edited))
	conn := hallPlayer(t, wsURL, "viewer")
	send(t, conn, "X first clock")
	expect(t, conn, "The first clock says four.")
}

func TestAWorldWithoutContentHasNoFeaturesAndStillExamines(t *testing.T) {
	wsURL := startTestServer(t) // the SQL seed: no features column data
	conn := passwordPlayer(t, wsURL, "legacy")
	send(t, conn, "LOOK")
	if out := expect(t, conn, "You are in 'Start Room'"); strings.Contains(out, "You notice") {
		t.Errorf("seeded room has a notice line: %q", out)
	}
	send(t, conn, "X key")
	expect(t, conn, "A generic looking key.")
}
