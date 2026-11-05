package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	mudserver "mud_server/src/internal/mudserver"
)

// readEnvelope reads the next frame and decodes it, failing on anything that
// isn't a v1 envelope.
func readEnvelope(t *testing.T, conn *websocket.Conn) (typ string, data map[string]any) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var env struct {
		V int            `json:"v"`
		T string         `json:"t"`
		D map[string]any `json:"d"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.V != 1 || env.T == "" {
		t.Fatalf("not a v1 envelope: %q (%v)", raw, err)
	}
	return env.T, env.D
}

// expectEnvelope reads frames until one has the given type and returns its payload.
func expectEnvelope(t *testing.T, conn *websocket.Conn, typ string) map[string]any {
	t.Helper()
	for {
		got, data := readEnvelope(t, conn)
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
		"MOVE N",           // legacy plain text
		"{not json",        // unparseable
		`{"v":1}`,          // no type
		`[1,2,3]`,          // not an object
		`{"v":1,"t":"cmd"`, // truncated
	} {
		conn := dial(t, wsURL)
		expect(t, conn, "Welcome to the MUD!")
		writeRaw(t, conn, frame)
		data := expectEnvelope(t, conn, "error")
		if data["code"] != "bad_frame" {
			t.Errorf("frame %q: code = %v, want bad_frame", frame, data["code"])
		}
		// Still usable.
		send(t, conn, "/REGISTER survivor"+strings.Repeat("x", len(frame)%5)+" secret")
		expectEnvelope(t, conn, "auth")
	}
}

func TestUnsupportedVersionIsRejectedAndClosed(t *testing.T) {
	wsURL := startTestServer(t)
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")
	writeRaw(t, conn, `{"v":2,"t":"cmd","d":{"line":"LOOK"}}`)
	if data := expectEnvelope(t, conn, "error"); data["code"] != "bad_version" {
		t.Errorf("code = %v, want bad_version", data["code"])
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return // closed, as designed
		}
	}
}

func TestUnknownTypeAndBadPayloads(t *testing.T) {
	wsURL := startTestServer(t)
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")

	for _, tc := range []struct{ frame, typ, code string }{
		{`{"v":1,"t":"dance"}`, "error", "bad_request"},
		{`{"v":1,"t":"cmd","d":"LOOK"}`, "error", "bad_request"},
		{`{"v":1,"t":"login","d":{"username":"x"}}`, "auth", "bad_request"},
		{`{"v":1,"t":"register","d":{"username":"","password":"p"}}`, "auth", "bad_request"},
		{`{"v":1,"t":"register","d":{"username":"two words","password":"p"}}`, "auth", "invalid_username"},
		{`{"v":1,"t":"register","d":{"username":"tab\there","password":"p"}}`, "auth", "invalid_username"},
		{`{"v":1,"t":"cmd","d":{"line":"LOOK"}}`, "auth", "not_logged_in"},
	} {
		writeRaw(t, conn, tc.frame)
		data := expectEnvelope(t, conn, tc.typ)
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

	writeRaw(t, conn, `{"v":1,"t":"register","d":{"username":"typed","password":"secret pass"}}`)
	auth := expectEnvelope(t, conn, "auth")
	if auth["ok"] != true || !strings.Contains(auth["message"].(string), "Account created! Welcome, typed.") {
		t.Errorf("register auth = %v", auth)
	}
	text := expectEnvelope(t, conn, "text")
	if !strings.Contains(text["text"].(string), "You are in 'Start Room'") || text["kind"] != "plain" {
		t.Errorf("look text = %v", text)
	}
	room := expectEnvelope(t, conn, "room")
	if room["name"] != "Start Room" {
		t.Errorf("room name = %v", room["name"])
	}
	if exits, _ := room["exits"].([]any); len(exits) == 0 {
		t.Errorf("room exits = %v, want at least one", room["exits"])
	}
	stats := expectEnvelope(t, conn, "stats")
	if stats["hp"] != float64(20) || stats["maxhp"] != float64(20) {
		t.Errorf("stats = %v", stats)
	}

	// A password with a space survives, and a second login is refused.
	writeRaw(t, conn, `{"v":1,"t":"login","d":{"username":"typed","password":"secret pass"}}`)
	if data := expectEnvelope(t, conn, "error"); data["code"] != "already_logged_in" {
		t.Errorf("second login = %v", data)
	}
	other := dial(t, wsURL)
	expect(t, other, "Welcome to the MUD!")
	writeRaw(t, other, `{"v":1,"t":"login","d":{"username":"typed","password":"wrong"}}`)
	if data := expectEnvelope(t, other, "auth"); data["code"] != "name_online" {
		t.Errorf("login while online = %v", data)
	}
}

func TestInventoryAndErrorTypes(t *testing.T) {
	wsURL := startTestServer(t)
	conn := register(t, wsURL, "pack")
	send(t, conn, "INVENTORY")
	if data := expectEnvelope(t, conn, "inventory"); !strings.Contains(data["text"].(string), "Your inventory is empty.") {
		t.Errorf("inventory = %v", data)
	}
	send(t, conn, "DANCE")
	if data := expectEnvelope(t, conn, "error"); data["code"] != "unknown_command" {
		t.Errorf("unknown command = %v", data)
	}
}

func TestLoginAfterRegisterRestoresAndWrongPasswordCodes(t *testing.T) {
	wsURL := startTestServer(t)
	first := register(t, wsURL, "again")
	first.Close()

	conn, _ := loginWhenFree(t, wsURL, "again", "secret")
	expect(t, conn, "You are in 'Start Room'")
	conn.Close()

	bad := dial(t, wsURL)
	expect(t, bad, "Welcome to the MUD!")
	writeRaw(t, bad, `{"v":1,"t":"login","d":{"username":"nobody","password":"x"}}`)
	if data := expectEnvelope(t, bad, "auth"); data["code"] != "unknown_user" {
		t.Errorf("unknown user = %v", data)
	}
	writeRaw(t, bad, `{"v":1,"t":"login","d":{"username":"again","password":"nope"}}`)
	for {
		data := expectEnvelope(t, bad, "auth")
		if data["code"] == "bad_password" {
			break
		}
		if data["code"] != "name_online" { // the first session may not have left yet
			t.Fatalf("login with wrong password = %v", data)
		}
		time.Sleep(50 * time.Millisecond)
		writeRaw(t, bad, `{"v":1,"t":"login","d":{"username":"again","password":"nope"}}`)
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
			case strings.Contains(m, "must be JSON envelopes"):
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
