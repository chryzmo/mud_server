package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"

	mudserver "mud_server/src/internal/mudserver"
)

/*
	Integration tests: run a real MudServer against the test database and drive it
	over a websocket, the same way the browser client does.

	They need MUD_TEST_DATABASE_URL (scripts/test.sh sets it after starting the
	local Postgres container) and are skipped without it.  The database is dropped
	and reseeded from src/sql before each test.
*/

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("MUD_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("MUD_TEST_DATABASE_URL not set; run scripts/test.sh to start the test database")
	}
	return url
}

// resetDatabase drops and reseeds every table using the scripts in src/sql, in load_order.txt order.
func resetDatabase(t *testing.T, url string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	defer conn.Close(ctx)

	order, err := os.ReadFile("sql/load_order.txt")
	if err != nil {
		t.Fatalf("read load order: %v", err)
	}
	for _, line := range strings.Split(string(order), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || strings.HasPrefix(name, "#") {
			continue
		}
		script, err := os.ReadFile("sql/" + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		// Exec without arguments uses the simple protocol, which allows multiple statements.
		if _, err := conn.Exec(ctx, string(script)); err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
	}
}

func startTestServer(t *testing.T) string {
	t.Helper()
	wsURL, _ := startTestServerWith(t)
	return wsURL
}

// startTestServerWith reseeds the test database, runs any setup SQL on top of
// the seed (to place items, speed up spawners, ...), and starts a server. The
// server is shut down when the test ends.
func startTestServerWith(t *testing.T, setupSQL ...string) (string, *mudserver.MudServer) {
	t.Helper()
	return startTestServerWithConfig(t, nil, setupSQL...)
}

// startTestServerWithConfig is startTestServerWith plus a hook to adjust the
// server's Configuration (connection timeouts and so on) before it starts.
func startTestServerWithConfig(t *testing.T, configure func(*mudserver.Configuration), setupSQL ...string) (string, *mudserver.MudServer) {
	t.Helper()
	url := testDatabaseURL(t)
	resetDatabase(t, url)
	execSQL(t, url, setupSQL...)

	c := mudserver.Configuration{}
	c.Database.DatabaseURL = url
	c.Game.RoomTickDurationInMS = 250
	c.Game.PlayerStartHP = 20
	c.Game.PlayerStartMaxHP = 20
	c.Game.PlayerStartMP = 10
	c.Game.PlayerStartMaxMP = 10
	c.Game.PlayerStartAttack = 5
	c.Game.PlayerStartDefense = 10
	if configure != nil {
		configure(&c)
	}

	si, err := mudserver.NewMudServer(c)
	if err != nil {
		t.Fatalf("NewMudServer: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) { handleConnections(si, w, r) })
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	// Registered after ts.Close, so it runs first: save everyone and stop the
	// server's goroutines before the next test reseeds the database.
	t.Cleanup(func() { si.Shutdown(context.Background()) })

	return "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws", si
}

func execSQL(t *testing.T, url string, statements ...string) {
	t.Helper()
	if len(statements) == 0 {
		return
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	defer conn.Close(ctx)
	for _, stmt := range statements {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup SQL %q: %v", stmt, err)
		}
	}
}

func dial(t *testing.T, wsURL string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", wsURL, err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func send(t *testing.T, conn *websocket.Conn, line string) {
	t.Helper()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(line)); err != nil {
		t.Fatalf("send %q: %v", line, err)
	}
}

// expect reads messages until one contains want, failing after a short timeout.
func expect(t *testing.T, conn *websocket.Conn, want string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var seen []string
	for {
		conn.SetReadDeadline(deadline)
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for %q: %v\nmessages seen:\n%s", want, err, strings.Join(seen, "\n---\n"))
		}
		if strings.Contains(string(msg), want) {
			return string(msg)
		}
		seen = append(seen, string(msg))
	}
}

func TestRegisterLookAndMove(t *testing.T) {
	wsURL := startTestServer(t)
	conn := dial(t, wsURL)

	expect(t, conn, "Welcome to the MUD!")
	send(t, conn, "/REGISTER tester secret")
	expect(t, conn, "Account created! Welcome, tester.")
	expect(t, conn, "You are in 'Start Room'")
	expect(t, conn, "STATUPDATE:hp=20,maxhp=20")

	send(t, conn, "MOVE N")
	expect(t, conn, "You are in 'Balcony'")

	send(t, conn, "SAY hello")
	expect(t, conn, `You say: "hello"`)

	send(t, conn, "DANCE")
	expect(t, conn, "Unknown command.")
}

func TestCommandsRequireLogin(t *testing.T) {
	wsURL := startTestServer(t)
	conn := dial(t, wsURL)

	expect(t, conn, "Welcome to the MUD!")
	send(t, conn, "LOOK")
	expect(t, conn, "Please /LOGIN or /REGISTER first.")
}

func TestLoginRestoresSavedLocation(t *testing.T) {
	wsURL := startTestServer(t)

	first := dial(t, wsURL)
	expect(t, first, "Welcome to the MUD!")
	send(t, first, "/REGISTER walker secret")
	expect(t, first, "You are in 'Start Room'")
	send(t, first, "MOVE N")
	expect(t, first, "You are in 'Balcony'")
	first.Close()

	conn, msg := loginWhenFree(t, wsURL, "walker", "secret")
	if !strings.Contains(msg, "Welcome back, walker!") {
		t.Fatalf("login: got %q", msg)
	}
	expect(t, conn, "You are in 'Balcony'")
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	wsURL := startTestServer(t)

	first := dial(t, wsURL)
	expect(t, first, "Welcome to the MUD!")
	send(t, first, "/REGISTER guarded secret")
	expect(t, first, "Account created!")
	first.Close()

	_, msg := loginWhenFree(t, wsURL, "guarded", "wrong")
	if !strings.Contains(msg, "Incorrect password.") {
		t.Fatalf("login with wrong password: got %q", msg)
	}
}

// loginWhenFree logs in, retrying while the server still has the player's
// previous session (it saves and removes the player when it notices the
// disconnect). It returns the connection and the server's reply to /LOGIN.
func loginWhenFree(t *testing.T, wsURL, name, password string) (*websocket.Conn, string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		conn := dial(t, wsURL)
		expect(t, conn, "Welcome to the MUD!")
		send(t, conn, "/LOGIN "+name+" "+password)
		msg := expect(t, conn, "")
		if strings.Contains(msg, "already logged in") && time.Now().Before(deadline) {
			conn.Close()
			time.Sleep(50 * time.Millisecond)
			continue
		}
		return conn, msg
	}
}

// register connects a new player and waits until they're standing in the start room.
func register(t *testing.T, wsURL, name string) *websocket.Conn {
	t.Helper()
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")
	send(t, conn, "/REGISTER "+name+" secret")
	expect(t, conn, "Account created! Welcome, "+name+".")
	expect(t, conn, "You are in 'Start Room'")
	return conn
}
