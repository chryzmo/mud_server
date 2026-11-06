package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	mudserver "mud_server/src/internal/mudserver"
)

// readMessage reads the next frame and decodes it, failing on anything that
// isn't a JSON object with a type.
func readMessage(t *testing.T, conn *websocket.Conn) (typ string, data map[string]any) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("not a JSON object: %q (%v)", raw, err)
	}
	typ, _ = m["t"].(string)
	if typ == "" {
		t.Fatalf("no type: %q", raw)
	}
	return typ, m
}

// expectMessage reads frames until one has the given type and returns it.
func expectMessage(t *testing.T, conn *websocket.Conn, typ string) map[string]any {
	t.Helper()
	for {
		got, data := readMessage(t, conn)
		if got == typ {
			return data
		}
	}
}

func writeRaw(t *testing.T, conn *websocket.Conn, frame string) {
	t.Helper()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestMalformedFramesGetBadFrameAndConnectionSurvives(t *testing.T) {
	wsURL := startTestServer(t)
	for _, frame := range []string{
		"MOVE N",            // legacy plain text
		"{not json",         // unparseable
		`{"line":"LOOK"}`,   // no type
		`[1,2,3]`,           // not an object
		`{"t":"cmd","line"`, // truncated
		`{"t":""}`,          // empty type
	} {
		conn := dial(t, wsURL)
		expect(t, conn, "Welcome to the MUD!")
		writeRaw(t, conn, frame)
		data := expectMessage(t, conn, "error")
		if data["code"] != "bad_frame" {
			t.Errorf("frame %q: code = %v, want bad_frame", frame, data["code"])
		}
		// Still usable.
		send(t, conn, "/LOGIN nobody secret")
		expectMessage(t, conn, "auth")
	}
}

// The protocol version rides in the websocket subprotocol, once per connection.
func TestClientWithoutSubprotocolIsRefusedAndClosed(t *testing.T) {
	wsURL := startTestServer(t)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil) // offers no subprotocol
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if data := expectMessage(t, conn, "error"); data["code"] != "bad_version" {
		t.Errorf("code = %v, want bad_version", data["code"])
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return // closed, as designed
		}
	}
}

func TestSubprotocolIsNegotiated(t *testing.T) {
	wsURL := startTestServer(t)
	conn := dial(t, wsURL)
	if got := conn.Subprotocol(); got != mudserver.Subprotocol {
		t.Errorf("negotiated subprotocol %q, want %q", got, mudserver.Subprotocol)
	}
}

func TestUnknownTypeAndBadPayloads(t *testing.T) {
	wsURL := startTestServer(t)
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")

	for _, tc := range []struct{ frame, typ, code string }{
		{`{"t":"dance"}`, "error", "bad_request"},
		{`{"t":"cmd"}`, "error", "bad_request"},
		{`{"t":"cmd","line":5}`, "error", "bad_frame"},
		{`{"t":"login","username":"x"}`, "auth", "bad_request"},
		{`{"t":"login","username":"","password":"p"}`, "auth", "bad_request"},
		{`{"t":"register","username":"x","password":"p"}`, "error", "bad_request"}, // there is no register: players join as guests
		{`{"t":"login","username":"two words","password":"p"}`, "auth", "invalid_username"},
		{`{"t":"login","username":"tab\there","password":"p"}`, "auth", "invalid_username"},
		{`{"t":"cmd","line":"LOOK"}`, "auth", "not_logged_in"},
	} {
		writeRaw(t, conn, tc.frame)
		data := expectMessage(t, conn, tc.typ)
		if data["code"] != tc.code {
			t.Errorf("%s: code = %v, want %s", tc.frame, data["code"], tc.code)
		}
		if tc.typ == "auth" && data["ok"] != false {
			t.Errorf("%s: ok = %v, want false", tc.frame, data["ok"])
		}
	}
}

func TestAuthStatsAndRoomMessages(t *testing.T) {
	wsURL := startTestServer(t)
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")

	createAccount(t, "typed", "secret pass")
	writeRaw(t, conn, `{"t":"login","username":"typed","password":"secret pass"}`)
	auth := expectMessage(t, conn, "auth")
	if auth["ok"] != true || !strings.Contains(auth["message"].(string), "Welcome back, typed!") {
		t.Errorf("login auth = %v", auth)
	}
	text := expectMessage(t, conn, "text")
	if !strings.Contains(text["text"].(string), "You are in 'Start Room'") {
		t.Errorf("look text = %v", text)
	}
	room := expectMessage(t, conn, "room")
	if room["name"] != "Start Room" {
		t.Errorf("room name = %v", room["name"])
	}
	if exits, _ := room["exits"].([]any); len(exits) == 0 {
		t.Errorf("room exits = %v, want at least one", room["exits"])
	}
	stats := expectMessage(t, conn, "stats")
	if stats["hp"] != float64(20) || stats["maxhp"] != float64(20) {
		t.Errorf("stats = %v", stats)
	}

	// A second login on the same connection is refused.
	writeRaw(t, conn, `{"t":"login","username":"typed","password":"secret pass"}`)
	if data := expectMessage(t, conn, "error"); data["code"] != "already_logged_in" {
		t.Errorf("second login = %v", data)
	}
	other := dial(t, wsURL)
	expect(t, other, "Welcome to the MUD!")
	writeRaw(t, other, `{"t":"login","username":"typed","password":"wrong"}`)
	if data := expectMessage(t, other, "auth"); data["code"] != "name_online" {
		t.Errorf("login while online = %v", data)
	}
}

// Fan-out and chatter are typed events, not coloured text: the client owns the
// wording and colour.
func TestRoomEventsAreTypedAndCompact(t *testing.T) {
	wsURL := startTestServer(t)
	alice := passwordPlayer(t, wsURL, "alice")
	bob := passwordPlayer(t, wsURL, "bob")

	data := expectMessage(t, alice, "arrive")
	if data["who"] != "bob" || data["from"] != nil {
		t.Errorf("arrive = %v, want who=bob and no from", data)
	}

	send(t, bob, "SAY hi <&> there")
	_, raw, err := alice.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"t":"say","who":"bob","text":"hi <&> there"}`; string(raw) != want {
		t.Errorf("heard SAY = %s, want %s", raw, want)
	}

	send(t, bob, "WHISPER alice psst")
	whisper := expectMessage(t, alice, "whisper")
	if whisper["who"] != "bob" || whisper["text"] != "psst" || whisper["out"] != nil {
		t.Errorf("whisper received = %v", whisper)
	}
	echo := expectMessage(t, bob, "whisper")
	if echo["who"] != "alice" || echo["text"] != "psst" || echo["out"] != true {
		t.Errorf("whisper echo = %v", echo)
	}

	send(t, bob, "MOVE N")
	if data := expectMessage(t, alice, "leave"); data["who"] != "bob" || data["to"] != "N" {
		t.Errorf("leave = %v, want who=bob to=N", data)
	}
	send(t, bob, "MOVE S")
	if data := expectMessage(t, alice, "arrive"); data["who"] != "bob" || data["from"] == nil || data["from"] == "" {
		t.Errorf("arrive after move = %v, want who=bob and a from direction", data)
	}

	bob.Close()
	if data := expectMessage(t, alice, "leave"); data["who"] != "bob" || data["to"] != nil {
		t.Errorf("disconnect leave = %v, want who=bob and no to", data)
	}
}

func TestInventoryAndErrorTypes(t *testing.T) {
	wsURL := startTestServer(t)
	conn := passwordPlayer(t, wsURL, "pack")
	send(t, conn, "INVENTORY")
	if data := expectMessage(t, conn, "inventory"); !strings.Contains(data["text"].(string), "Your inventory is empty.") {
		t.Errorf("inventory = %v", data)
	}
	send(t, conn, "DANCE")
	if data := expectMessage(t, conn, "error"); data["code"] != "unknown_command" {
		t.Errorf("unknown command = %v", data)
	}
}

func TestLoginRestoresAndWrongPasswordCodes(t *testing.T) {
	wsURL := startTestServer(t)
	first := passwordPlayer(t, wsURL, "again")
	first.Close()

	conn, _ := loginWhenFree(t, wsURL, "again", "secret")
	expect(t, conn, "You are in 'Start Room'")
	conn.Close()

	bad := dial(t, wsURL)
	expect(t, bad, "Welcome to the MUD!")
	writeRaw(t, bad, `{"t":"login","username":"nobody","password":"x"}`)
	if data := expectMessage(t, bad, "auth"); data["code"] != "unknown_user" {
		t.Errorf("unknown user = %v", data)
	}
	writeRaw(t, bad, `{"t":"login","username":"again","password":"nope"}`)
	for {
		data := expectMessage(t, bad, "auth")
		if data["code"] == "bad_password" {
			break
		}
		if data["code"] != "name_online" { // the first session may not have left yet
			t.Fatalf("login with wrong password = %v", data)
		}
		time.Sleep(50 * time.Millisecond)
		writeRaw(t, bad, `{"t":"login","username":"again","password":"nope"}`)
	}
}

// Malformed frames still cost a limiter token: a flood of garbage is dropped.
func TestGarbageFramesAreRateLimited(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		c.Connection.CommandsPerSecond = 1
		c.Connection.CommandBurst = 2
	})
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")
	incoming := reader(conn)
	for i := 0; i < 100; i++ {
		writeRaw(t, conn, "garbage")
	}
	badFrames, notices := 0, 0
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for notices == 0 {
		select {
		case m, ok := <-incoming:
			if !ok {
				t.Fatal("flooded session closed")
			}
			switch {
			case strings.Contains(m, "Slow down."):
				notices++
			case strings.Contains(m, "must be JSON objects"):
				badFrames++
			}
		case <-deadline.C:
			t.Fatal("no rate-limit notice")
		}
	}
	if badFrames > 3 {
		t.Errorf("%d garbage frames were processed, want about the burst of 2", badFrames)
	}
}
