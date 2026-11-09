package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mudserver "mud_server/src/internal/mudserver"
)

// World content files (plans/event-systems.md): the server validates them at startup,
// applies them to the database by stable key, and builds the world from them.

const sampleWorld = "../world-sample"

func contentDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func withContent(dir string) func(*mudserver.Configuration) {
	return func(c *mudserver.Configuration) { c.World.ContentDir = dir }
}

// lobbyWorld is a world that shares no room name with the SQL seed.
const lobbyWorld = `
start_room: lobby
rooms:
  lobby:
    name: Lobby
    description: A bright lobby.
    exits: {N: garden}
    items: {key: 1}
  garden:
    name: Garden
    description: A quiet garden.
    exits: {S: lobby}
items:
  key:
    name: KEY
    description: A generic looking key.
`

func TestContentAdoptsTheSeededWorldWithoutMovingAnything(t *testing.T) {
	t.Parallel()
	startTestServerWithConfig(t, withContent(sampleWorld))

	for _, tc := range []struct{ sql, want string }{
		{"SELECT count(*)::text FROM room", "3"},
		{"SELECT count(*)::text FROM item", "10"},
		{"SELECT count(*)::text FROM equipment", "8"},
		{"SELECT count(*)::text FROM baddienpc_generic", "1"},
		{"SELECT count(*)::text FROM baddiespawner", "1"},
		{"SELECT id::text FROM room WHERE contentkey='start-room'", "1"},
		{"SELECT id::text FROM room WHERE contentkey='balcony'", "2"},
		{"SELECT id::text FROM room WHERE contentkey='arena'", "3"},
		{"SELECT id::text FROM item WHERE contentkey='key'", "1"},
		{"SELECT contentkey FROM baddiespawner", "arena/orc/0"},
		{"SELECT exitjson::jsonb::text FROM room WHERE id=2", `{"E": 3, "S": 1}`},
		{"SELECT count(*)::text FROM room WHERE contentkey IS NULL", "0"},
	} {
		if got := queryString(t, tc.sql); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.sql, got, tc.want)
		}
	}
}

func TestContentWorldPlaysLikeTheSeededOne(t *testing.T) {
	t.Parallel()
	wsURL, _ := startTestServerWithConfig(t, withContent(sampleWorld))
	conn := passwordPlayer(t, wsURL, "viewer")
	send(t, conn, "GET KEY")
	expect(t, conn, "You pick up KEY.")
	send(t, conn, "MOVE N")
	expect(t, conn, "You are in 'Balcony'")
}

func TestApplyingTwiceChangesNothingAndKeepsWhatPlayersDid(t *testing.T) {
	t.Parallel()
	url := testDatabaseURL(t)
	resetDatabase(t, url)
	_, first := serveTestServer(t, url, withContent(sampleWorld))
	first.Shutdown(context.Background()) // the server saves its rooms on the way out
	execSQL(t, url, `UPDATE room SET inventoryidslice='{"i2": 5}' WHERE contentkey='start-room'`) // someone dropped gold

	serveTestServer(t, url, withContent(sampleWorld)) // a deploy: the same content again
	for _, tc := range []struct{ sql, want string }{
		{"SELECT count(*)::text FROM room", "3"},
		{"SELECT count(*)::text FROM item", "10"},
		{"SELECT count(*)::text FROM baddiespawner", "1"},
		{"SELECT inventoryidslice::jsonb::text FROM room WHERE contentkey='start-room'", `{"i2": 5}`},
	} {
		if got := queryString(t, tc.sql); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.sql, got, tc.want)
		}
	}
}

func TestContentCreatesRoomsAndLeavesTheOldOnesOutOfTheWorld(t *testing.T) {
	t.Parallel()
	dir := contentDir(t, map[string]string{"w.yaml": lobbyWorld})
	wsURL, _ := startTestServerWithConfig(t, withContent(dir))

	// The seeded rooms are still in the database, but not part of the world.
	if got := queryString(t, "SELECT count(*)::text FROM room"); got != "5" {
		t.Errorf("rooms in the database = %s, want the 3 seeded and 2 new", got)
	}
	if got := queryString(t, "SELECT count(*)::text FROM room WHERE contentkey IS NOT NULL"); got != "2" {
		t.Errorf("content rooms = %s, want 2", got)
	}

	// A new guest starts in the content's start room, not at room id 1.
	conn, auth := joinAs(t, wsURL, "Zed_1")
	if auth["ok"] != true {
		t.Fatalf("join = %v", auth)
	}
	expect(t, conn, "You are in 'Lobby'")
	send(t, conn, "GET KEY") // the floor item was placed when the room was created, and KEY is the adopted seeded item
	expect(t, conn, "You pick up KEY.")
	send(t, conn, "MOVE N")
	expect(t, conn, "You are in 'Garden'")
	send(t, conn, "MOVE S")
	expect(t, conn, "You are in 'Lobby'")
}

func TestAPlayerSavedInARoomThatLeftTheWorldStartsAtTheStartRoom(t *testing.T) {
	t.Parallel()
	dir := contentDir(t, map[string]string{"w.yaml": lobbyWorld})
	wsURL, _ := startTestServerWithConfig(t, withContent(dir))
	// A password account is created in room 1, the seeded "Start Room", which the content dropped.
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")
	loginNew(t, conn, "stranded")
	expect(t, conn, "Welcome back, stranded!")
	expect(t, conn, "You are in 'Lobby'")
}

func TestContentAreasCopyRoomsForACrowd(t *testing.T) {
	t.Parallel()
	dir := contentDir(t, map[string]string{"w.yaml": lobbyWorld + "areas:\n  main: {rooms: [lobby, garden]}\n"})
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		c.World.ContentDir = dir
		c.Crowd = mudserver.CrowdConfig{SoftCap: 1, MaxCopies: 5} // the config's own Areas, if any, are ignored
	})
	ann := dial(t, wsURL)
	expect(t, ann, "Welcome to the MUD!")
	loginNew(t, ann, "ann")
	expect(t, ann, "You are in 'Lobby'")

	bob := dial(t, wsURL)
	expect(t, bob, "Welcome to the MUD!")
	loginNew(t, bob, "bob")
	expect(t, bob, "You are in 'Lobby'")

	// Bob is in the second copy: Ann does not hear him.
	send(t, bob, "SAY anyone there")
	expect(t, bob, `You say: "anyone there"`)
	if text, heard := expectWithin(ann, "anyone there", 400*time.Millisecond); heard {
		t.Errorf("ann heard bob across copies: %q", text)
	}
}

func TestBrokenContentStopsTheServerFromStarting(t *testing.T) {
	t.Parallel()
	url := testDatabaseURL(t)
	resetDatabase(t, url)
	broken := contentDir(t, map[string]string{"w.yaml": strings.Replace(lobbyWorld, "{N: garden}", "{N: nowhere}", 1)})

	c := mudserver.Configuration{}
	c.Database.DatabaseURL = url
	c.World.ContentDir = broken
	if _, err := mudserver.NewMudServer(c); err == nil || !strings.Contains(err.Error(), "not starting") {
		t.Fatalf("NewMudServer with broken content = %v, want a refusal", err)
	}
	// Nothing was applied.
	if got := queryString(t, "SELECT count(*)::text FROM room WHERE contentkey IS NOT NULL"); got != "0" {
		t.Errorf("content rooms after a refused start = %s, want 0", got)
	}

	c.World.ContentDir = filepath.Join(t.TempDir(), "missing")
	if _, err := mudserver.NewMudServer(c); err == nil {
		t.Errorf("a missing content directory should stop the server")
	}
}

func TestAWorldWithoutContentIsUnchanged(t *testing.T) {
	t.Parallel()
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {})
	conn := passwordPlayer(t, wsURL, "legacy")
	send(t, conn, "MOVE N")
	expect(t, conn, "You are in 'Balcony'")
	if got := queryString(t, "SELECT count(*)::text FROM room WHERE contentkey IS NOT NULL"); got != "0" {
		t.Errorf("content keys appeared without content: %s", got)
	}
}
