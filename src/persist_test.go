package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	mudserver "mud_server/src/internal/mudserver"
)

// queryString runs a one-value query against the test database.
func queryString(t *testing.T, sql string) string {
	t.Helper()
	db, err := pgx.Connect(context.Background(), testDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(context.Background())
	var v string
	if err := db.QueryRow(context.Background(), sql).Scan(&v); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return v
}

// eventually polls the database until check passes, failing after 3s.
func eventually(t *testing.T, what, sql string, check func(string) bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		v := queryString(t, sql)
		if check(v) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: still %q after 3s", what, v)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func saveEvery(ms int) func(*mudserver.Configuration) {
	return func(c *mudserver.Configuration) { c.Server.SaveIntervalMS = ms }
}

const (
	playerInventory = "SELECT inventoryidslice::text FROM player WHERE username = 'keeper'"
	playerRoom      = "SELECT roomid::text FROM player WHERE username = 'keeper'"
	startRoomItems  = "SELECT inventoryidslice::text FROM room WHERE id = 1"
)

func hasKey(v string) bool             { return strings.Contains(v, `"i1"`) }
func noKey(v string) bool              { return !hasKey(v) }
func is(want string) func(string) bool { return func(v string) bool { return v == want } }

// Changes reach the database within one save interval, while the player is
// still online: nothing waits for a disconnect or the hourly save any more.
func TestChangesSavedWithinInterval(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, saveEvery(100))
	conn := register(t, wsURL, "keeper")

	send(t, conn, "GET KEY")
	expect(t, conn, "You pick up KEY.")
	eventually(t, "player inventory", playerInventory, hasKey)
	eventually(t, "start room contents", startRoomItems, noKey)

	send(t, conn, "MOVE N")
	expect(t, conn, "You are in 'Balcony'")
	eventually(t, "player room", playerRoom, is("2"))
}

// DROP never marked anything for saving before, so a dropped item came back
// after a restart unless the player happened to disconnect.
func TestDropIsSaved(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, saveEvery(100))
	conn := register(t, wsURL, "keeper")

	send(t, conn, "GET KEY")
	expect(t, conn, "You pick up KEY.")
	eventually(t, "player inventory after GET", playerInventory, hasKey)

	send(t, conn, "DROP KEY")
	expect(t, conn, "You drop KEY.")
	eventually(t, "player inventory after DROP", playerInventory, noKey)
	eventually(t, "start room contents after DROP", startRoomItems, hasKey)
}

// Logging back in right after disconnecting loads what was saved at the
// disconnect. The periodic interval is far too long to be what saved it.
func TestReloginSeesStateSavedAtDisconnect(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, saveEvery(600000))
	conn := register(t, wsURL, "keeper")
	send(t, conn, "GET KEY")
	expect(t, conn, "You pick up KEY.")
	send(t, conn, "MOVE N")
	expect(t, conn, "You are in 'Balcony'")
	conn.Close()

	again, msg := loginWhenFree(t, wsURL, "keeper", "secret")
	if !strings.Contains(msg, "Welcome back, keeper!") {
		t.Fatalf("login: got %q", msg)
	}
	expect(t, again, "You are in 'Balcony'")
	send(t, again, "INVENTORY")
	expect(t, again, "1x KEY")
}

// Shutdown writes rooms as well as players, even when nothing has been
// flushed yet.
func TestShutdownSavesRooms(t *testing.T) {
	wsURL, si := startTestServerWithConfig(t, saveEvery(600000))
	conn := register(t, wsURL, "keeper")
	send(t, conn, "GET KEY")
	expect(t, conn, "You pick up KEY.")
	if hasKey(queryString(t, playerInventory)) {
		t.Fatal("saved before shutdown; this test needs the periodic save not to have run")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	si.Shutdown(ctx)

	if v := queryString(t, playerInventory); !hasKey(v) {
		t.Errorf("player inventory after shutdown = %s, want the KEY", v)
	}
	if v := queryString(t, startRoomItems); hasKey(v) {
		t.Errorf("start room after shutdown = %s, want no KEY", v)
	}
}
