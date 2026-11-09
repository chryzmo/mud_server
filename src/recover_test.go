package main

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	mudserver "mud_server/src/internal/mudserver"
)

// recoverAs connects and sends recover, returning the connection and the reply
// (an auth frame, or whatever the server answered with).
func recoverAs(t *testing.T, wsURL, name, code string) (*websocket.Conn, map[string]any) {
	t.Helper()
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")
	writeRaw(t, conn, fmt.Sprintf(`{"t":"recover","name":%q,"code":%q}`, name, code))
	return conn, expectMessage(t, conn, "auth")
}

func recoveryConfig(graceMS, lockMS int) func(*mudserver.Configuration) {
	return func(c *mudserver.Configuration) {
		c.Connection.ResumeGraceMS = graceMS
		c.Connection.RecoveryLockMS = lockMS
	}
}

func TestRecoverTakesBackADormantCharacterAndRotatesCredentials(t *testing.T) {
	t.Parallel()
	wsURL, _ := startTestServerWithConfig(t, recoveryConfig(10000, 0))
	watcher := joinGuest(t, wsURL, "Watcher")
	lost := joinGuest(t, wsURL, "Lost")
	watcher.until(func(typ string, m map[string]any) bool { return typ == "arrive" && m["who"] == "Lost" })
	oldToken, oldCode := lost.token, lost.recovery
	lost.drop() // the phone is gone, and so is the token

	typed := strings.ToLower(strings.ReplaceAll(oldCode, "-", "")) // any case, no dashes
	conn, auth := recoverAs(t, wsURL, "Lost", typed)
	if auth["ok"] != true || auth["resumed"] != true || auth["gap"] != true {
		t.Fatalf("recover = %v, want ok, resumed, with a gap (the new device remembers nothing)", auth)
	}
	newToken, _ := auth["token"].(string)
	newCode, _ := auth["recovery"].(string)
	if len(newToken) != 43 || newToken == oldToken || newCode == "" || newCode == oldCode {
		t.Fatalf("recover must issue a new token and a new code; got token %q code %q", newToken, newCode)
	}
	// A fresh look and stats follow, in the same room, and nobody saw them leave or arrive.
	g := &guest{t: t, wsURL: wsURL, name: "Lost", conn: conn, token: newToken, recovery: newCode}
	g.until(func(typ string, m map[string]any) bool { return typ == "stats" })
	send(t, conn, "SAY recovered")
	for _, m := range watcher.until(func(typ string, m map[string]any) bool { return typ == "say" && m["text"] == "recovered" }) {
		if (m["t"] == "arrive" || m["t"] == "leave") && m["who"] == "Lost" {
			t.Errorf("a recovery in the grace period was announced: %v", m)
		}
	}

	// The database holds only hashes of the new credentials.
	q := "SELECT %s FROM player WHERE username='Lost'"
	if got := queryString(t, fmt.Sprintf(q, "resumetokenhash")); got != sha(newToken) {
		t.Errorf("resumetokenhash = %q, want the hash of the new token", got)
	}
	if got := queryString(t, fmt.Sprintf(q, "recoveryhash")); got != sha(strings.ReplaceAll(newCode, "-", "")) {
		t.Errorf("recoveryhash = %q, want the hash of the new code", got)
	}

	// The old credentials are dead; the new ones work.
	stale := &guest{t: t, wsURL: wsURL, name: "Lost", token: oldToken}
	if auth, _ := stale.reconnect(); auth["code"] != "bad_token" {
		t.Errorf("the old token = %v, want bad_token", auth)
	}
	if _, auth := recoverAs(t, wsURL, "Lost", oldCode); auth["code"] != "bad_recovery" {
		t.Errorf("the old code = %v, want bad_recovery", auth)
	}
	g.drop()
	time.Sleep(50 * time.Millisecond)
	if auth, _ := g.reconnect(); auth["ok"] != true || auth["gap"] != nil {
		t.Errorf("resume with the new token = %v, want a clean in-memory resume", auth)
	}
}

func TestRecoverWhileTheOldDeviceIsStillConnected(t *testing.T) {
	t.Parallel()
	wsURL, ms := startTestServerWithConfig(t, recoveryConfig(400, 0))
	watcher := joinGuest(t, wsURL, "Watcher")
	phone := joinGuest(t, wsURL, "Phone")
	watcher.until(func(typ string, m map[string]any) bool { return typ == "arrive" && m["who"] == "Phone" })

	old := phone.conn
	conn, auth := recoverAs(t, wsURL, "Phone", phone.recovery) // the old phone never dropped
	if auth["ok"] != true {
		t.Fatalf("recover = %v", auth)
	}
	waitForClose(t, ms, "replaced", 1)
	old.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := old.ReadMessage(); err != nil {
			break
		}
	}
	// Outlast the grace period: the replaced connection must not expire a live character.
	time.Sleep(1200 * time.Millisecond)
	send(t, conn, "SAY still here")
	for _, m := range watcher.until(func(typ string, m map[string]any) bool { return typ == "say" && m["who"] == "Phone" }) {
		if m["t"] == "leave" && m["who"] == "Phone" {
			t.Errorf("the recovered character was removed: %v", m)
		}
	}
	send(t, watcher.conn, "LOOK")
	look := watcher.until(func(typ string, m map[string]any) bool {
		return typ == "text" && strings.Contains(m["text"].(string), "You are in 'Start Room'")
	})
	if text := look[len(look)-1]["text"].(string); strings.Count(text, "Phone") != 1 {
		t.Errorf("Phone appears %d times:\n%s", strings.Count(text, "Phone"), text)
	}
}

func TestRecoverAfterTheGracePeriodIsAnOrdinaryArrival(t *testing.T) {
	t.Parallel()
	wsURL, _ := startTestServerWithConfig(t, recoveryConfig(250, 0))
	watcher := joinGuest(t, wsURL, "Watcher")
	lost := joinGuest(t, wsURL, "Lost")
	send(t, lost.conn, "MOVE N")
	lost.until(func(typ string, m map[string]any) bool { return typ == "room" && m["name"] == "Balcony" })
	lost.drop()
	watcher.until(func(typ string, m map[string]any) bool { return typ == "leave" && m["who"] == "Lost" }) // removed after the grace period

	conn, auth := recoverAs(t, wsURL, "Lost", lost.recovery)
	if auth["ok"] != true || auth["resumed"] != true || auth["token"] == nil || auth["recovery"] == nil {
		t.Fatalf("recover after grace = %v", auth)
	}
	g := &guest{t: t, wsURL: wsURL, name: "Lost", conn: conn}
	frames := g.until(func(typ string, m map[string]any) bool { return typ == "stats" })
	back := false
	for _, m := range frames {
		back = back || (m["t"] == "room" && m["name"] == "Balcony")
	}
	if !back {
		t.Errorf("a recovered character should return to their saved room (the Balcony): %v", frames)
	}
}

func TestWrongRecoveryCodesLockTheName(t *testing.T) {
	t.Parallel()
	wsURL, _ := startTestServerWithConfig(t, recoveryConfig(5000, 900))
	victim := joinGuest(t, wsURL, "Victim")
	victim.drop()

	var first map[string]any
	for i := 0; i < 5; i++ {
		_, auth := recoverAs(t, wsURL, "Victim", "AAAA-BBBB-CCCC")
		if auth["ok"] != false || auth["code"] != "bad_recovery" {
			t.Fatalf("wrong code %d = %v", i+1, auth)
		}
		if first == nil {
			first = auth
		}
	}
	// The name is now locked: even the right code is refused, with a retry hint.
	_, auth := recoverAs(t, wsURL, "Victim", victim.recovery)
	retry, _ := auth["retry_ms"].(float64)
	if auth["ok"] != false || auth["code"] != "recovery_locked" || retry <= 0 || retry > 900 {
		t.Fatalf("right code while locked = %v, want recovery_locked with retry_ms in (0, 900]", auth)
	}
	// After the lock expires the right code works.
	time.Sleep(1000 * time.Millisecond)
	if _, auth := recoverAs(t, wsURL, "Victim", victim.recovery); auth["ok"] != true {
		t.Errorf("after the lock = %v, want success", auth)
	}
}

func TestASuccessfulRecoveryClearsTheFailureCount(t *testing.T) {
	t.Parallel()
	wsURL, _ := startTestServerWithConfig(t, recoveryConfig(5000, 0))
	g := joinGuest(t, wsURL, "Careful")
	g.drop()
	for i := 0; i < 4; i++ {
		recoverAs(t, wsURL, "Careful", "AAAA-BBBB-CCCC")
	}
	conn, auth := recoverAs(t, wsURL, "Careful", g.recovery)
	if auth["ok"] != true {
		t.Fatalf("recover after four wrong codes = %v", auth)
	}
	conn.Close()
	time.Sleep(50 * time.Millisecond)
	newCode, _ := auth["recovery"].(string)
	for i := 0; i < 4; i++ { // four more wrong codes must not lock: the count restarted
		recoverAs(t, wsURL, "Careful", "AAAA-BBBB-CCCC")
	}
	if _, auth := recoverAs(t, wsURL, "Careful", newCode); auth["ok"] != true {
		t.Errorf("after success cleared the count, four more wrong codes locked the name: %v", auth)
	}
}

func TestRecoveryAnswersLeakNothingAboutWhichNamesExist(t *testing.T) {
	t.Parallel()
	wsURL := startTestServer(t)
	joinGuest(t, wsURL, "Realguest")
	passwordPlayer(t, wsURL, "Passworded") // a password account has no recovery code

	_, want := recoverAs(t, wsURL, "Realguest", "AAAA-BBBB-CCCC")
	for name, who := range map[string]string{"unknown": "Nobodyatall", "password account": "Passworded", "seeded account": "c"} {
		_, got := recoverAs(t, wsURL, who, "AAAA-BBBB-CCCC")
		if got["code"] != want["code"] || got["message"] != want["message"] || got["ok"] != false {
			t.Errorf("%s: %v, want the same answer as a wrong code for a real guest: %v", name, got, want)
		}
	}
	// The name matches ignoring case.
	g := joinGuest(t, wsURL, "MixedCase")
	g.drop()
	if _, auth := recoverAs(t, wsURL, "mIxEdCaSe", g.recovery); auth["ok"] != true {
		t.Errorf("recover with a differently-cased name = %v", auth)
	}
}

func TestOnlyOneConcurrentRecoveryWins(t *testing.T) {
	t.Parallel()
	wsURL, _ := startTestServerWithConfig(t, recoveryConfig(5000, 0))
	g := joinGuest(t, wsURL, "Contested")
	g.drop()

	const devices = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	won, refused := 0, 0
	for i := 0; i < devices; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, _, err := dialer.Dial(wsURL, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"t":"recover","name":"Contested","code":%q}`, g.recovery)))
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			for {
				_, raw, err := conn.ReadMessage()
				if err != nil {
					return
				}
				s := string(raw)
				if strings.Contains(s, `"t":"auth"`) {
					mu.Lock()
					defer mu.Unlock()
					if strings.Contains(s, `"ok":true`) {
						won++
					} else if strings.Contains(s, `"code":"bad_recovery"`) || strings.Contains(s, `"code":"recovery_locked"`) {
						// Late arrivals present a code that has already been rotated, which counts
						// as a wrong code; enough of them lock the name, which is also a refusal.
						refused++
					}
					return
				}
			}
		}()
	}
	wg.Wait()
	if won != 1 || refused != devices-1 {
		t.Errorf("won=%d refused=%d, want exactly one winner and %d refusals", won, refused, devices-1)
	}
}

func TestRecoverRejectsMalformedRequestsAndLoggedInSessions(t *testing.T) {
	t.Parallel()
	wsURL := startTestServer(t)
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")
	for name, frame := range map[string]string{
		"no name":   `{"t":"recover","code":"AAAA-BBBB-CCCC"}`,
		"no code":   `{"t":"recover","name":"Someone"}`,
		"long name": `{"t":"recover","name":"` + strings.Repeat("a", 65) + `","code":"AAAA-BBBB-CCCC"}`,
		"long code": `{"t":"recover","name":"Someone","code":"` + strings.Repeat("A", 65) + `"}`,
	} {
		writeRaw(t, conn, frame)
		if auth := expectMessage(t, conn, "auth"); auth["code"] != "bad_request" {
			t.Errorf("%s: %v, want bad_request", name, auth)
		}
	}
	g := joinGuest(t, wsURL, "Inside")
	writeRaw(t, g.conn, fmt.Sprintf(`{"t":"recover","name":"Inside","code":%q}`, g.recovery))
	if data := expectMessage(t, g.conn, "error"); data["code"] != "already_logged_in" {
		t.Errorf("recover while in the game = %v", data)
	}
}
