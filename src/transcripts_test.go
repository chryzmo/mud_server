package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"mud_server/src/internal/mudserver"
)

/*
Wire transcripts (plans/mobile-client.md, C1). A recording reference client plays
scenarios against the real server and writes down every frame it sends and
receives, per connection, in testdata/transcripts.json. The file is committed;
this test fails if the server's behaviour no longer produces it. The browser
client carries a copy and replays it through a scripted socket, so the two
repositories cannot drift apart without a test failing on one side
(scripts/check-transcripts.sh compares the copies).

Single frames are not enough: the rules that matter are sequence rules, such as
how frames are counted and what `received` a client sends after a gap. So the
recorder is a client that follows the rules (count every frame except hello,
auth and error; after a resume set the count to the reply's base), and the
`received` it sends is part of the recording.

Refresh after an intended protocol change:

	UPDATE_TRANSCRIPTS=1 scripts/test.sh   (or go test ./src -run TestWireTranscripts)

Normalised so the file is stable: tokens and recovery codes become TOKEN-n and
RECOVERY-n, and a retry_ms hint (random jitter, elapsed time) becomes a fixed
figure after checking it is plausible.
*/

const transcriptFile = "testdata/transcripts.json"

type tevent struct {
	Send  map[string]any `json:"send,omitempty"` // a frame the client sent
	Recv  map[string]any `json:"recv,omitempty"` // a frame the client received
	Note  string         `json:"note,omitempty"` // what the client did that is not a frame
	Ended string         `json:"end,omitempty"`  // how the connection ended: "cut" (client dropped it), "server_closed", "open"
}

type tconnection struct {
	Events []tevent `json:"events"`
}

type transcript struct {
	Scenario    string        `json:"scenario"`
	About       string        `json:"about"`
	Connections []tconnection `json:"connections"`
}

type transcriptFileBody struct {
	Format    int          `json:"format"`
	Notes     []string     `json:"notes"`
	Scenarios []transcript `json:"scenarios"`
}

// recorder plays one scenario as the subject client.
type recorder struct {
	t        *testing.T
	wsURL    string
	script   transcript
	secrets  map[string]string // real value -> placeholder
	token    string
	recovery string
	count    uint64 // counted frames processed, as a client keeps it
}

type recConn struct {
	r    *recorder
	conn *websocket.Conn
	idx  int
}

func newRecorder(t *testing.T, name, about, wsURL string) *recorder {
	return &recorder{t: t, wsURL: wsURL, secrets: map[string]string{}, script: transcript{Scenario: name, About: about}}
}

func (r *recorder) open() *recConn {
	r.t.Helper()
	return r.attach(dial(r.t, r.wsURL))
}

func (r *recorder) attach(conn *websocket.Conn) *recConn {
	r.script.Connections = append(r.script.Connections, tconnection{})
	return &recConn{r: r, conn: conn, idx: len(r.script.Connections) - 1}
}

func (c *recConn) add(e tevent) {
	c.r.script.Connections[c.idx].Events = append(c.r.script.Connections[c.idx].Events, e)
}

// normalise copies a frame, replacing known secrets with their placeholders.
func (r *recorder) normalise(f map[string]any) map[string]any {
	out := make(map[string]any, len(f))
	for k, v := range f {
		if s, ok := v.(string); ok {
			if p, known := r.secrets[s]; known {
				v = p
			}
		}
		out[k] = v
	}
	return out
}

func (c *recConn) send(frame map[string]any) {
	c.r.t.Helper()
	raw, _ := json.Marshal(frame)
	if err := c.conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		c.r.t.Fatalf("%s: send %v: %v", c.r.script.Scenario, frame, err)
	}
	c.add(tevent{Send: c.r.normalise(frame)})
}

func (c *recConn) cmd(line string) { c.send(map[string]any{"t": "cmd", "line": line}) }

// read takes the next frame, applies the client's counting rules and records it.
func (c *recConn) read() map[string]any {
	c.r.t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, raw, err := c.conn.ReadMessage()
	if err != nil {
		c.r.t.Fatalf("%s: read: %v", c.r.script.Scenario, err)
	}
	var f map[string]any
	if err := json.Unmarshal(raw, &f); err != nil {
		c.r.t.Fatalf("%s: not JSON: %q", c.r.script.Scenario, raw)
	}
	r := c.r
	typ, _ := f["t"].(string)
	switch typ {
	case "hello", "error":
	case "auth":
		if f["ok"] == true {
			if tok, _ := f["token"].(string); tok != "" {
				r.token = tok
				r.secrets[tok] = fmt.Sprintf("TOKEN-%d", len(r.secrets)/2+1)
			}
			if rec, _ := f["recovery"].(string); rec != "" {
				r.recovery = rec
				r.secrets[rec] = fmt.Sprintf("RECOVERY-%d", len(r.secrets)/2+1)
			}
			base, _ := f["base"].(float64) // absent means 0
			r.count = uint64(base)
		}
	default:
		r.count++
	}
	rec := r.normalise(f)
	if text, ok := rec["text"].(string); ok {
		rec["text"] = sortPlayerList(text)
	}
	if hint, ok := f["retry_ms"].(float64); ok {
		fixed := map[string]float64{"rate_limited": 1000, "recovery_locked": 900000}[f["code"].(string)]
		if hint <= 0 || hint > fixed+1000 {
			r.t.Fatalf("%s: retry_ms %v is not plausible for %v", r.script.Scenario, hint, f["code"])
		}
		rec["retry_ms"] = fixed
	}
	c.add(tevent{Recv: rec})
	return f
}

// sortPlayerList puts the players of a look in alphabetical order. The server lists
// them in map order, which would make a recording differ from run to run.
func sortPlayerList(text string) string {
	const header = "The players here are:\n"
	i := strings.Index(text, header)
	if i < 0 {
		return text
	}
	start := i + len(header)
	end := start + strings.Index(text[start:]+"\n\n", "\n\n")
	lines := strings.Split(text[start:end], "\n")
	sort.Strings(lines)
	return text[:start] + strings.Join(lines, "\n") + text[end:]
}

func isT(typ string) func(map[string]any) bool {
	return func(f map[string]any) bool { return f["t"] == typ }
}

func textHas(sub string) func(map[string]any) bool {
	return func(f map[string]any) bool {
		s, _ := f["text"].(string)
		return f["t"] == "text" && strings.Contains(s, sub)
	}
}

// until reads frames until one matches, and returns it.
func (c *recConn) until(match func(map[string]any) bool) map[string]any {
	c.r.t.Helper()
	for {
		if f := c.read(); match(f) {
			return f
		}
	}
}

func (c *recConn) hello() { c.r.t.Helper(); c.until(isT("hello")) }

// join sends join and reads through the first stats frame.
func (c *recConn) join(name string) {
	c.r.t.Helper()
	c.send(map[string]any{"t": "join", "name": name})
	if auth := c.read(); auth["ok"] != true {
		c.r.t.Fatalf("join %s = %v", name, auth)
	}
	c.until(isT("stats"))
}

// resume sends resume with the client's count (or none) and reads the reply and
// whatever the reply promises: the replayed frames, or after a gap the fresh look.
func (c *recConn) resume(withCount bool) map[string]any {
	c.r.t.Helper()
	frame := map[string]any{"t": "resume", "token": c.r.token}
	if withCount {
		frame["received"] = c.r.count
	}
	c.send(frame)
	return c.afterResume()
}

func (c *recConn) afterResume() map[string]any {
	c.r.t.Helper()
	auth := c.read()
	if auth["t"] != "auth" || auth["ok"] != true {
		c.r.t.Fatalf("%s: resume reply = %v", c.r.script.Scenario, auth)
	}
	if auth["gap"] == true {
		c.until(isT("stats"))
	} else {
		n, _ := auth["replayed"].(float64)
		for i := 0; i < int(n); i++ {
			c.read()
		}
	}
	return auth
}

// cut drops the connection without a goodbye, like a lost signal.
func (c *recConn) cut() {
	c.conn.UnderlyingConn().Close()
	c.add(tevent{Ended: "cut"})
}

// serverCloses reads until the server ends the connection.
func (c *recConn) serverCloses() {
	c.r.t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
		c.r.t.Fatalf("%s: unexpected frame before close: %s", c.r.script.Scenario, raw)
	}
	c.add(tevent{Ended: "server_closed"})
}

func (c *recConn) left() { c.add(tevent{Ended: "open"}) }

func need(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(format, args...)
	}
}

func say(t *testing.T, g *guest, text string) {
	t.Helper()
	send(t, g.conn, "SAY "+text)
	g.until(func(typ string, m map[string]any) bool {
		return typ == "text" && strings.Contains(m["text"].(string), `You say: "`+text+`"`)
	})
}

func wire(t *testing.T) []transcript {
	var out []transcript
	add := func(r *recorder) { out = append(out, r.script) }
	bigLimits := func(c *mudserver.Configuration) {
		c.Connection.SayPerSecond, c.Connection.SayBurst = 1000, 1000
		c.Connection.CommandsPerSecond, c.Connection.CommandBurst = 1000, 1000
	}

	t.Run("join", func(t *testing.T) {
		wsURL := startTestServer(t)
		r := newRecorder(t, "join", "A new guest joins by name and gets a token, a recovery code and a first look.", wsURL)
		c := r.open()
		c.hello()
		c.join("Alice")
		need(t, r.token != "" && r.recovery != "", "no token or recovery code")
		c.left()
		add(r)
	})

	t.Run("join-refused", func(t *testing.T) {
		wsURL := startTestServer(t)
		bob := joinGuest(t, wsURL, "Bob")
		// Out of the start room: a look lists the other players in map order, which would make the recording unstable.
		send(t, bob.conn, "MOVE N")
		bob.until(func(typ string, m map[string]any) bool { return typ == "room" && m["name"] == "Balcony" })
		r := newRecorder(t, "join-refused", "Refused names answer on the same connection, which stays usable; the server's message is shown.", wsURL)
		c := r.open()
		c.hello()
		for _, name := range []string{"a", "bad name", "bob"} {
			c.send(map[string]any{"t": "join", "name": name})
			need(t, c.read()["ok"] == false, "%q was accepted", name)
		}
		c.join("Zed")
		c.left()
		add(r)
	})

	t.Run("play", func(t *testing.T) {
		wsURL := startTestServer(t)
		r := newRecorder(t, "play", "Room events (arrive, say, whisper, leave), movement, inventory and a stats refresh as another player acts.", wsURL)
		c := r.open()
		c.hello()
		c.join("Alice")
		bob := joinGuest(t, wsURL, "Bob")
		c.until(func(f map[string]any) bool { return f["t"] == "arrive" && f["who"] == "Bob" })
		c.cmd("SAY hello")
		c.until(textHas(`You say: "hello"`))
		send(t, bob.conn, "SAY hi alice")
		c.until(isT("say"))
		c.cmd("WHISPER Bob psst")
		c.until(func(f map[string]any) bool { return f["t"] == "whisper" && f["out"] == true })
		send(t, bob.conn, "WHISPER Alice back")
		c.until(func(f map[string]any) bool { return f["t"] == "whisper" && f["out"] != true })
		send(t, bob.conn, "MOVE N")
		c.until(isT("leave"))
		c.cmd("INVENTORY")
		c.until(isT("inventory"))
		c.cmd("MOVE N")
		c.until(func(f map[string]any) bool { return f["t"] == "room" && f["name"] == "Balcony" })
		c.left()
		add(r)
	})

	t.Run("resume-replays-missed-frames", func(t *testing.T) {
		wsURL, _ := startTestServerWithConfig(t, graceConfig(5000, 50))
		r := newRecorder(t, "resume-replays-missed-frames",
			"A dropped client resumes within the grace period and is replayed what it missed; a second drop and resume stays gap-free (the count did not drift).", wsURL)
		c1 := r.open()
		c1.hello()
		c1.join("Alice")
		bob := joinGuest(t, wsURL, "Bob")
		c1.until(func(f map[string]any) bool { return f["t"] == "arrive" && f["who"] == "Bob" })
		c1.cut()

		say(t, bob, "one")
		say(t, bob, "two")
		c2 := r.open()
		c2.hello()
		auth := c2.resume(true)
		need(t, auth["gap"] == nil && auth["replayed"] == float64(2), "resume = %v, want 2 replayed and no gap", auth)
		c2.cut()

		say(t, bob, "three")
		c3 := r.open()
		c3.hello()
		auth = c3.resume(true)
		need(t, auth["gap"] == nil && auth["replayed"] == float64(1), "second resume = %v, want 1 replayed and no gap", auth)
		c3.left()
		add(r)
	})

	t.Run("resume-gap-ring-overflow", func(t *testing.T) {
		wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) { graceConfig(5000, 5)(c); bigLimits(c) })
		r := newRecorder(t, "resume-gap-ring-overflow",
			"More frames were missed than the server keeps: the reply says gap with a base, and a fresh look follows. The count is reset to base, so the next resume replays cleanly.", wsURL)
		c1 := r.open()
		c1.hello()
		c1.join("Alice")
		bob := joinGuest(t, wsURL, "Bob")
		c1.until(func(f map[string]any) bool { return f["t"] == "arrive" && f["who"] == "Bob" })
		c1.cut()
		for i := 0; i < 8; i++ {
			say(t, bob, fmt.Sprintf("line%d", i))
		}
		c2 := r.open()
		c2.hello()
		auth := c2.resume(true)
		need(t, auth["gap"] == true && auth["base"] != nil, "resume = %v, want a gap with a base", auth)
		c2.cut()

		say(t, bob, "after")
		c3 := r.open()
		c3.hello()
		auth = c3.resume(true)
		need(t, auth["gap"] == nil && auth["replayed"] == float64(1), "resume after a gap = %v, want 1 replayed: the count drifted", auth)
		c3.left()
		add(r)
	})

	t.Run("resume-cold-page", func(t *testing.T) {
		wsURL, _ := startTestServerWithConfig(t, graceConfig(5000, 50))
		r := newRecorder(t, "resume-cold-page",
			"A freshly loaded page has a token but no count: it sends resume without `received` and gets a gap and a fresh look, not stale frames.", wsURL)
		c1 := r.open()
		c1.hello()
		c1.join("Alice")
		bob := joinGuest(t, wsURL, "Bob")
		c1.until(func(f map[string]any) bool { return f["t"] == "arrive" && f["who"] == "Bob" })
		c1.cut()
		say(t, bob, "stale")

		r.count = 0 // the page was reloaded: nothing remembered
		c2 := r.open()
		c2.hello()
		auth := c2.resume(false)
		need(t, auth["gap"] == true, "cold resume = %v, want a gap", auth)
		c2.left()
		add(r)
	})

	t.Run("resume-after-grace", func(t *testing.T) {
		wsURL, _ := startTestServerWithConfig(t, graceConfig(300, 50))
		r := newRecorder(t, "resume-after-grace",
			"The grace period ran out (or the server restarted): the resume works as an ordinary arrival, with a gap and no base.", wsURL)
		c1 := r.open()
		c1.hello()
		c1.join("Alice")
		c1.cmd("MOVE N")
		c1.until(func(f map[string]any) bool { return f["t"] == "room" && f["name"] == "Balcony" })
		c1.cut()
		time.Sleep(1200 * time.Millisecond)
		c2 := r.open()
		c2.hello()
		auth := c2.resume(true)
		need(t, auth["gap"] == true && auth["base"] == nil, "resume after grace = %v, want a gap with no base", auth)
		c2.left()
		add(r)
	})

	t.Run("resume-bad-token", func(t *testing.T) {
		wsURL := startTestServer(t)
		r := newRecorder(t, "resume-bad-token", "An unknown or malformed token is refused with auth bad_token; this is terminal for that token.", wsURL)
		c := r.open()
		c.hello()
		for _, token := range []string{"abc", strings.Repeat("A", 43)} {
			c.send(map[string]any{"t": "resume", "token": token, "received": 0})
			a := c.read()
			need(t, a["ok"] == false && a["code"] == "bad_token", "resume %q = %v", token, a)
		}
		c.left()
		add(r)
	})

	t.Run("replaced", func(t *testing.T) {
		wsURL, _ := startTestServerWithConfig(t, graceConfig(5000, 50))
		r := newRecorder(t, "replaced",
			"A second connection takes the character over: the first is told `replaced` and closed, and must not reconnect by itself.", wsURL)
		c1 := r.open()
		c1.hello()
		c1.join("Alice")
		count := r.count
		c2 := r.open()
		c2.hello()
		r.count = count
		c2.resume(true)
		c1.until(func(f map[string]any) bool { return f["t"] == "error" && f["code"] == "replaced" })
		c1.serverCloses()
		c2.left()
		add(r)
	})

	t.Run("recover", func(t *testing.T) {
		wsURL, _ := startTestServerWithConfig(t, graceConfig(5000, 50))
		r := newRecorder(t, "recover",
			"A guest who lost the token recovers with name and code (any case, no dashes): both secrets are replaced, the new token works, and the old one is bad_token.", wsURL)
		c1 := r.open()
		c1.hello()
		c1.join("Alice")
		oldToken, oldRecovery := r.token, r.recovery
		c1.cut()

		c2 := r.open()
		c2.hello()
		typed := strings.ToLower(strings.ReplaceAll(oldRecovery, "-", "")) // as a player might type it
		r.secrets[typed] = "recovery-1-typed-lowercase-without-dashes"
		c2.send(map[string]any{"t": "recover", "name": "alice", "code": typed})
		auth := c2.afterResume()
		need(t, auth["token"] != nil && auth["recovery"] != nil && auth["token"] != oldToken && auth["recovery"] != oldRecovery,
			"recover = %v, want a new token and code", auth)
		c2.cut()

		c3 := r.open()
		c3.hello()
		c3.resume(true)
		c3.cut()

		c4 := r.open()
		c4.hello()
		c4.send(map[string]any{"t": "resume", "token": oldToken, "received": 0})
		need(t, c4.read()["code"] == "bad_token", "old token still works")
		c4.left()
		add(r)
	})

	t.Run("recover-locked", func(t *testing.T) {
		wsURL := startTestServer(t)
		r := newRecorder(t, "recover-locked", "Five wrong codes lock a name for a while: the sixth try, even with the right code, gets recovery_locked with retry_ms.", wsURL)
		c1 := r.open()
		c1.hello()
		c1.join("Alice")
		real := r.recovery
		c1.cut()
		c2 := r.open()
		c2.hello()
		for i := 0; i < 5; i++ {
			c2.send(map[string]any{"t": "recover", "name": "Alice", "code": "WRONG-CODE-0000"})
			need(t, c2.read()["code"] == "bad_recovery", "wrong code %d", i)
		}
		c2.send(map[string]any{"t": "recover", "name": "Alice", "code": real})
		need(t, c2.read()["code"] == "recovery_locked", "sixth try was not locked")
		c2.left()
		add(r)
	})

	t.Run("idle", func(t *testing.T) {
		wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) { c.Connection.IdleTimeoutMS = 500 })
		r := newRecorder(t, "idle", "A guest with no command for the idle timeout gets error idle and is disconnected; the client must not reconnect by itself.", wsURL)
		c := r.open()
		c.hello()
		c.join("Alice")
		c.until(func(f map[string]any) bool { return f["t"] == "error" && f["code"] == "idle" })
		c.serverCloses()
		add(r)
	})

	t.Run("server-restarting", func(t *testing.T) {
		wsURL, ms := startTestServerWithConfig(t, nil)
		r := newRecorder(t, "server-restarting", "A shutting-down server says restarting and closes: the client reconnects with jitter (it is not terminal).", wsURL)
		c := r.open()
		c.hello()
		c.join("Alice")
		go ms.Shutdown(context.Background())
		c.until(func(f map[string]any) bool { return f["t"] == "error" && f["code"] == "restarting" })
		c.serverCloses()
		add(r)
	})

	t.Run("join-rate-limited", func(t *testing.T) {
		wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) { c.Connection.JoinPerSecond, c.Connection.JoinBurst = 1, 1 })
		joinGuest(t, wsURL, "Bob") // spends the only token
		r := newRecorder(t, "join-rate-limited",
			"The global join limit answers error rate_limited with retry_ms: wait exactly that (plus a little jitter), then ask again on the same connection.", wsURL)
		c := r.open()
		c.hello()
		c.send(map[string]any{"t": "join", "name": "Alice"})
		limited := c.read()
		need(t, limited["code"] == "rate_limited" && limited["retry_ms"] != nil, "join = %v, want rate_limited with retry_ms", limited)
		time.Sleep(time.Duration(limited["retry_ms"].(float64)) * time.Millisecond)
		c.join("Alice")
		c.left()
		add(r)
	})

	t.Run("command-rate-limited", func(t *testing.T) {
		wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
			c.Connection.CommandsPerSecond, c.Connection.CommandBurst = 1, 4
		})
		r := newRecorder(t, "command-rate-limited",
			"The per-command limiter also uses code rate_limited but has no retry_ms: it is a notice, not a reason to reconnect or wait.", wsURL)
		c := r.open()
		c.hello()
		c.join("Alice")
		for i := 0; i < 8; i++ { // burst 4, and the join and first frames already spent some
			c.send(map[string]any{"t": "cmd", "line": ""}) // blank commands do nothing but still spend tokens
		}
		f := c.until(func(f map[string]any) bool { return f["t"] == "error" && f["code"] == "rate_limited" })
		need(t, f["retry_ms"] == nil, "command limiter notice has retry_ms: %v", f)
		c.left()
		add(r)
	})

	t.Run("bad-version", func(t *testing.T) {
		wsURL := startTestServer(t)
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil) // no subprotocol
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		r := newRecorder(t, "bad-version", "A client that does not offer the mud.v1 subprotocol is refused with error bad_version and closed: show 'reload the page'.", wsURL)
		c := r.attach(conn)
		need(t, c.read()["code"] == "bad_version", "no bad_version")
		c.serverCloses()
		add(r)
	})
	return out
}

func TestWireTranscripts(t *testing.T) {
	got := transcriptFileBody{
		Format: 1,
		Notes: []string{
			"Generated by TestWireTranscripts in mud_server (src/transcripts_test.go); do not edit by hand.",
			"Each scenario lists the connections of ONE client, in order. Within a connection, send/recv are in wire order.",
			"Counting: every received frame except hello, auth and error counts; after an ok auth the count becomes auth.base (absent = 0). A resume's `received` is that count.",
			"Secrets are placeholders (TOKEN-n, RECOVERY-n); retry_ms is normalised (rate_limited 1000, recovery_locked 900000); the player list in a look is sorted (the server's order is random).",
		},
		Scenarios: wire(t),
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(got); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("UPDATE_TRANSCRIPTS") != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(transcriptFile, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", transcriptFile)
		return
	}
	want, err := os.ReadFile(transcriptFile)
	if err != nil {
		t.Fatalf("%v\nrun with UPDATE_TRANSCRIPTS=1 to create it", err)
	}
	if !bytes.Equal(want, buf.Bytes()) {
		t.Fatalf("the server no longer produces %s.\nIf the protocol change is intended, regenerate it with UPDATE_TRANSCRIPTS=1 and update the frontend copy (scripts/check-transcripts.sh) and its parser in the same change.", transcriptFile)
	}
}
