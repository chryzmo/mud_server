package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	mudserver "mud_server/src/internal/mudserver"
)

// guest is a reference client for the resume protocol: it counts every frame it
// processes except hello, auth and error, and reports that count on resume.
type guest struct {
	t        *testing.T
	wsURL    string
	name     string
	token    string
	recovery string
	conn     *websocket.Conn
	count    uint64
}

func (g *guest) counted(typ string) bool { return typ != "hello" && typ != "auth" && typ != "error" }

// next reads one frame, counting it if it is a counted type.
func (g *guest) next(timeout time.Duration) (typ string, m map[string]any, err error) {
	g.conn.SetReadDeadline(time.Now().Add(timeout))
	_, raw, err := g.conn.ReadMessage()
	if err != nil {
		return "", nil, err
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		g.t.Fatalf("%s: not JSON: %q", g.name, raw)
	}
	typ, _ = m["t"].(string)
	if g.counted(typ) {
		g.count++
	}
	return typ, m, nil
}

// until reads frames until stop returns true for one, and returns every frame read.
func (g *guest) until(stop func(typ string, m map[string]any) bool) []map[string]any {
	g.t.Helper()
	var seen []map[string]any
	for {
		typ, m, err := g.next(3 * time.Second)
		if err != nil {
			g.t.Fatalf("%s: waiting: %v (saw %d frames: %v)", g.name, err, len(seen), seen)
		}
		seen = append(seen, m)
		if stop(typ, m) {
			return seen
		}
	}
}

// settle reads the frames that follow admission, up to the stats frame.
func (g *guest) settle() { g.until(func(typ string, _ map[string]any) bool { return typ == "stats" }) }

func joinGuest(t *testing.T, wsURL, name string) *guest {
	t.Helper()
	g := &guest{t: t, wsURL: wsURL, name: name}
	g.conn = dial(t, wsURL)
	if typ, _, err := g.next(3 * time.Second); err != nil || typ != "hello" {
		t.Fatalf("%s: first frame %q, %v; want hello", name, typ, err)
	}
	g.conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"t":"join","name":%q}`, name)))
	_, auth, err := g.next(3 * time.Second)
	if err != nil || auth["ok"] != true {
		t.Fatalf("%s: join = %v, %v", name, auth, err)
	}
	g.token, _ = auth["token"].(string)
	g.recovery, _ = auth["recovery"].(string)
	g.settle()
	return g
}

// drop breaks the connection without a goodbye, like a phone losing signal.
func (g *guest) drop() { g.conn.UnderlyingConn().Close() }

// reconnect opens a new connection and sends resume with the current count. It
// returns the auth reply with the count and replayed frames applied as a real
// client would, and any frames that followed the reply up to `replayed`.
func (g *guest) reconnect() (auth map[string]any, replayed []map[string]any) {
	g.t.Helper()
	g.conn = dial(g.t, g.wsURL)
	if typ, _, err := g.next(3 * time.Second); err != nil || typ != "hello" {
		g.t.Fatalf("%s: first frame after reconnect %q, %v", g.name, typ, err)
	}
	g.conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"t":"resume","token":%q,"received":%d}`, g.token, g.count)))
	_, auth, err := g.next(3 * time.Second)
	if err != nil {
		g.t.Fatalf("%s: resume: %v", g.name, err)
	}
	if auth["ok"] == true {
		base, _ := auth["base"].(float64)
		g.count = uint64(base) // what the client's count was before any replayed frames
		n, _ := auth["replayed"].(float64)
		for i := 0; i < int(n); i++ {
			_, m, err := g.next(3 * time.Second)
			if err != nil {
				g.t.Fatalf("%s: replayed frame %d of %d: %v", g.name, i+1, int(n), err)
			}
			replayed = append(replayed, m)
		}
	}
	return auth, replayed
}

func graceConfig(graceMS, replay int) func(*mudserver.Configuration) {
	return func(c *mudserver.Configuration) {
		c.Connection.ResumeGraceMS = graceMS
		c.Connection.ReplayFrames = replay
	}
}

func sayLines(replayed []map[string]any) []string {
	var out []string
	for _, m := range replayed {
		if m["t"] == "say" {
			out = append(out, m["text"].(string))
		}
	}
	return out
}

func TestResumeWithinGraceKeepsTheCharacterAndAnnouncesNothing(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, graceConfig(5000, 50))
	watcher := joinGuest(t, wsURL, "Watcher")
	dropper := joinGuest(t, wsURL, "Dropper")
	watcher.until(func(typ string, m map[string]any) bool { return typ == "arrive" && m["who"] == "Dropper" })

	dropper.drop()
	// Say three things while Dropper is away. Dropper is dormant: still in the room.
	for _, line := range []string{"one", "two", "three"} {
		send(t, watcher.conn, "SAY "+line)
		watcher.until(func(typ string, m map[string]any) bool {
			return typ == "text" && strings.Contains(m["text"].(string), `You say: "`+line+`"`)
		})
	}
	send(t, watcher.conn, "LOOK")
	look := watcher.until(func(typ string, m map[string]any) bool {
		return typ == "text" && strings.Contains(m["text"].(string), "You are in 'Start Room'")
	})
	if text := look[len(look)-1]["text"].(string); !strings.Contains(text, "Dropper") {
		t.Errorf("a dormant guest should still be in the room, but LOOK shows:\n%s", text)
	}

	auth, replayed := dropper.reconnect()
	if auth["ok"] != true || auth["resumed"] != true || auth["gap"] != nil {
		t.Fatalf("resume auth = %v, want resumed without a gap", auth)
	}
	if got := sayLines(replayed); fmt.Sprint(got) != "[one two three]" {
		t.Errorf("replayed says = %v, want [one two three] in order", got)
	}
	if n := auth["replayed"]; n != float64(len(replayed)) || len(replayed) != 3 {
		t.Errorf("replayed %v frames (%d read), want 3", n, len(replayed))
	}

	// Dropper is back in the same room and can act; Watcher never saw them leave or arrive.
	send(t, dropper.conn, "SAY back")
	frames := watcher.until(func(typ string, m map[string]any) bool { return typ == "say" && m["text"] == "back" })
	for _, m := range frames {
		if (m["t"] == "arrive" || m["t"] == "leave") && m["who"] == "Dropper" {
			t.Errorf("Watcher was told about a quick reconnect: %v", m)
		}
	}
}

func TestResumeAfterGraceIsAnOrdinaryArrival(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, graceConfig(300, 50))
	watcher := joinGuest(t, wsURL, "Watcher")
	leaver := joinGuest(t, wsURL, "Leaver")
	watcher.until(func(typ string, m map[string]any) bool { return typ == "arrive" && m["who"] == "Leaver" })
	send(t, leaver.conn, "MOVE N")
	leaver.until(func(typ string, m map[string]any) bool { return typ == "room" && m["name"] == "Balcony" })
	watcher.until(func(typ string, m map[string]any) bool { return typ == "leave" && m["who"] == "Leaver" })

	leaver.drop()
	// The grace period runs out, so Watcher is not in Leaver's room, but a third guest is.
	third := joinGuest(t, wsURL, "Third")
	send(t, third.conn, "MOVE N")
	third.until(func(typ string, m map[string]any) bool { return typ == "room" && m["name"] == "Balcony" })
	third.until(func(typ string, m map[string]any) bool { return typ == "leave" && m["who"] == "Leaver" })

	auth, replayed := leaver.reconnect()
	if auth["ok"] != true || auth["resumed"] != true || auth["gap"] != true || auth["base"] != nil {
		t.Fatalf("resume after grace = %v, want resumed with a gap and base 0", auth)
	}
	if len(replayed) != 0 {
		t.Errorf("replayed %d frames after the grace period, want none", len(replayed))
	}
	// Placed in their saved room (the Balcony) as a normal arrival, which Third sees.
	leaver.until(func(typ string, m map[string]any) bool { return typ == "room" && m["name"] == "Balcony" })
	third.until(func(typ string, m map[string]any) bool { return typ == "arrive" && m["who"] == "Leaver" })
}

func TestResumeReplacesALiveConnection(t *testing.T) {
	wsURL, ms := startTestServerWithConfig(t, graceConfig(400, 50))
	watcher := joinGuest(t, wsURL, "Watcher")
	phone := joinGuest(t, wsURL, "Phone")
	watcher.until(func(typ string, m map[string]any) bool { return typ == "arrive" && m["who"] == "Phone" })

	old := phone.conn
	auth, _ := phone.reconnect() // a second device resumes while the first is still connected
	if auth["ok"] != true || auth["resumed"] != true {
		t.Fatalf("resume = %v", auth)
	}
	waitForClose(t, ms, "replaced", 1)

	// The old connection is closed by the server.
	old.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := old.ReadMessage(); err != nil {
			break
		}
	}

	// The replaced connection's cleanup must not leave a live player dormant: wait
	// well past the grace period, then they are still here and still in control.
	time.Sleep(1200 * time.Millisecond)
	send(t, phone.conn, "SAY from the new device")
	frames := watcher.until(func(typ string, m map[string]any) bool { return typ == "say" && m["who"] == "Phone" })
	for _, m := range frames {
		if m["t"] == "leave" || (m["t"] == "arrive" && m["who"] == "Phone") {
			t.Errorf("a takeover was announced: %v", m)
		}
	}
	// Only one Phone is in the room.
	send(t, watcher.conn, "LOOK")
	look := watcher.until(func(typ string, m map[string]any) bool {
		return typ == "text" && strings.Contains(m["text"].(string), "You are in 'Start Room'")
	})
	if text := look[len(look)-1]["text"].(string); strings.Count(text, "Phone") != 1 {
		t.Errorf("Phone appears %d times in the room:\n%s", strings.Count(text, "Phone"), text)
	}
	if counts := ms.SessionCloseCounts(); counts["read_error"]+counts["client_closed"] > 1 {
		t.Logf("close counts: %v", counts)
	}
}

func TestResumeRefusesBadTokens(t *testing.T) {
	wsURL := startTestServer(t)
	alice := joinGuest(t, wsURL, "Alice")
	bob := joinGuest(t, wsURL, "Bob")
	alice.drop()
	bob.drop()
	time.Sleep(100 * time.Millisecond)

	try := func(frame string) map[string]any {
		conn := dial(t, wsURL)
		expect(t, conn, "Welcome to the MUD!")
		writeRaw(t, conn, frame)
		return expectMessage(t, conn, "auth")
	}
	for name, frame := range map[string]string{
		"empty":           `{"t":"resume","token":"","received":0}`,
		"missing":         `{"t":"resume","received":0}`,
		"too short":       `{"t":"resume","token":"abc","received":0}`,
		"too long":        `{"t":"resume","token":"` + strings.Repeat("a", 60) + `","received":0}`,
		"wrong token":     `{"t":"resume","token":"` + strings.Repeat("A", 43) + `","received":0}`,
		"a recovery code": `{"t":"resume","token":"` + alice.recovery + `","received":0}`,
	} {
		if got := try(frame); got["ok"] != false || got["code"] != "bad_token" {
			t.Errorf("%s: %v, want bad_token", name, got)
		}
	}

	// A token belongs to its own guest only: Bob's token never yields Alice.
	g := &guest{t: t, wsURL: wsURL, name: "Bob", token: bob.token, count: bob.count}
	auth, _ := g.reconnect()
	if auth["ok"] != true {
		t.Fatalf("Bob's own token: %v", auth)
	}
	send(t, g.conn, "WHISPER Alice hi") // Alice is still dormant, so she exists
	g.until(func(typ string, m map[string]any) bool {
		return typ == "whisper" && m["out"] == true && m["who"] == "Alice"
	})

	// Already in the game: resume is refused without disturbing anything.
	writeRaw(t, g.conn, fmt.Sprintf(`{"t":"resume","token":%q,"received":0}`, alice.token))
	if data := expectMessage(t, g.conn, "error"); data["code"] != "already_logged_in" {
		t.Errorf("resume while in game = %v", data)
	}
}

// Counts stay consistent through gaps and repeated reconnects: a client that
// applies base and processes what it is sent never replays or misses a frame.
func TestReplayGapResyncsAndCountsStayConsistent(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		graceConfig(5000, 5)(c)
		c.Connection.SayPerSecond, c.Connection.SayBurst = 1000, 1000 // this test talks a lot
		c.Connection.CommandsPerSecond, c.Connection.CommandBurst = 1000, 1000
	})
	watcher := joinGuest(t, wsURL, "Watcher")
	flaky := joinGuest(t, wsURL, "Flaky")
	watcher.until(func(typ string, m map[string]any) bool { return typ == "arrive" && m["who"] == "Flaky" })

	says := func(n int, prefix string) {
		for i := 0; i < n; i++ {
			line := fmt.Sprintf("%s%d", prefix, i)
			send(t, watcher.conn, "SAY "+line)
			watcher.until(func(typ string, m map[string]any) bool {
				return typ == "text" && strings.Contains(m["text"].(string), `"`+line+`"`)
			})
		}
	}

	// 1. Miss far more than the ring holds: a gap, then a fresh look and stats.
	flaky.drop()
	says(20, "a")
	auth, replayed := flaky.reconnect()
	if auth["gap"] != true || len(replayed) != 0 {
		t.Fatalf("after missing 20 frames with a ring of 5: %v, replayed %d; want a gap and nothing replayed", auth, len(replayed))
	}
	if base, _ := auth["base"].(float64); base < 20 {
		t.Errorf("gap base = %v, want the server's frame count (at least 20)", auth["base"])
	}
	flaky.until(func(typ string, m map[string]any) bool { return typ == "room" })
	flaky.until(func(typ string, m map[string]any) bool { return typ == "stats" })

	// 2. Reconnect straight away: nothing was missed, so nothing is replayed and no gap.
	flaky.drop()
	time.Sleep(50 * time.Millisecond)
	auth, replayed = flaky.reconnect()
	if auth["gap"] != nil || len(replayed) != 0 || auth["ok"] != true {
		t.Fatalf("immediate reconnect after a resync: %v, replayed %d; want a clean resume (the counts drifted)", auth, len(replayed))
	}

	// 3. Miss three, within the ring: exactly those three, in order.
	flaky.drop()
	says(3, "b")
	auth, replayed = flaky.reconnect()
	if auth["gap"] != nil || fmt.Sprint(sayLines(replayed)) != "[b0 b1 b2]" {
		t.Fatalf("missing three: %v, replayed %v; want [b0 b1 b2]", auth, sayLines(replayed))
	}
}

func TestResumeSurvivesARestartAsAnOrdinaryArrival(t *testing.T) {
	url := testDatabaseURL(t)
	resetDatabase(t, url)
	wsA, serverA := serveTestServer(t, url, graceConfig(5000, 50))

	g := joinGuest(t, wsA, "Survivor")
	send(t, g.conn, "MOVE N")
	g.until(func(typ string, m map[string]any) bool { return typ == "room" && m["name"] == "Balcony" })
	g.drop()
	serverA.Shutdown(context.Background())

	wsB, serverB := serveTestServer(t, url, graceConfig(5000, 50))
	g.wsURL = wsB
	auth, replayed := g.reconnect()
	if auth["ok"] != true || auth["resumed"] != true || auth["gap"] != true || len(replayed) != 0 {
		t.Fatalf("resume after restart = %v, replayed %d", auth, len(replayed))
	}
	frames := g.until(func(typ string, m map[string]any) bool { return typ == "stats" })
	sawBalcony := false
	for _, m := range frames {
		if m["t"] == "room" && m["name"] == "Balcony" {
			sawBalcony = true
		}
	}
	if !sawBalcony {
		t.Errorf("after the restart the guest should be back in the Balcony, frames: %v", frames)
	}

	// And the new process keeps counting consistently from zero.
	g.drop()
	time.Sleep(50 * time.Millisecond)
	if auth, _ := g.reconnect(); auth["gap"] != nil || auth["ok"] != true {
		t.Errorf("second resume on the new server = %v, want a clean in-memory resume", auth)
	}
	_ = serverB
}

// Several devices resuming at once: the race detector watches, exactly one
// connection is left in control, and the character is in the room once.
func TestConcurrentResumesOfOneToken(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, graceConfig(400, 50))
	watcher := joinGuest(t, wsURL, "Watcher")
	target := joinGuest(t, wsURL, "Target")
	watcher.until(func(typ string, m map[string]any) bool { return typ == "arrive" && m["who"] == "Target" })
	target.drop()

	const devices = 8
	var wg sync.WaitGroup
	conns := make([]*websocket.Conn, devices)
	results := make([]bool, devices)
	for i := 0; i < devices; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conn, _, err := dialer.Dial(wsURL, nil)
			if err != nil {
				return
			}
			conns[i] = conn
			conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"t":"resume","token":%q,"received":0}`, target.token)))
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			for {
				_, raw, err := conn.ReadMessage()
				if err != nil {
					return
				}
				if strings.Contains(string(raw), `"t":"auth"`) {
					results[i] = strings.Contains(string(raw), `"ok":true`)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	for _, c := range conns {
		if c != nil {
			defer c.Close()
		}
	}
	for i, ok := range results {
		if !ok {
			t.Errorf("device %d was not resumed", i)
		}
	}
	time.Sleep(1200 * time.Millisecond) // past the grace period: no replaced connection may expire the player
	send(t, watcher.conn, "LOOK")
	look := watcher.until(func(typ string, m map[string]any) bool {
		return typ == "text" && strings.Contains(m["text"].(string), "You are in 'Start Room'")
	})
	for _, m := range look[:len(look)-1] {
		if m["t"] == "leave" && m["who"] == "Target" {
			t.Errorf("Target was removed while connected: %v", m)
		}
	}
	if text := look[len(look)-1]["text"].(string); strings.Count(text, "Target") != 1 {
		t.Errorf("Target appears %d times:\n%s", strings.Count(text, "Target"), text)
	}
}

// Only guests get a grace period; a password account that drops leaves at once.
func TestPasswordPlayersStillLeaveImmediately(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, graceConfig(60000, 50))
	watcher := joinGuest(t, wsURL, "Watcher")
	pw := register(t, wsURL, "Passworded")
	watcher.until(func(typ string, m map[string]any) bool { return typ == "arrive" && m["who"] == "Passworded" })
	pw.UnderlyingConn().Close()
	watcher.until(func(typ string, m map[string]any) bool { return typ == "leave" && m["who"] == "Passworded" })
}
