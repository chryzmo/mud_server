package mudbot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func testConfig(target string) Config {
	return Config{URL: target, Count: 3, Duration: 2500 * time.Millisecond, Interval: 250 * time.Millisecond,
		Timeout: time.Second, JoinTimeout: 3 * time.Second, LoginWorkers: 2, Prefix: "testbot", Password: "fixture"}
}

func peerFrame(t string, fields map[string]any) []byte {
	m := map[string]any{"t": t}
	for k, v := range fields {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return b
}

func peerText(text string) []byte {
	return peerFrame("text", map[string]any{"text": text})
}

// Fake protocol peer: no game state, database or sleeps. It can reject logins,
// withhold fences, or disconnect on the first action to exercise failure reports.
func protocolPeer(t *testing.T, mode string) string {
	t.Helper()
	upgrader := websocket.Upgrader{Subprotocols: []string{subprotocol}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, message, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var login struct {
			T        string `json:"t"`
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if json.Unmarshal(message, &login) != nil || (login.T != "login" && login.T != "register") {
			return
		}
		if mode == "stall-login" {
			conn.ReadMessage() // canceled admission must close the socket
			return
		}
		if mode == "reject" {
			conn.WriteMessage(websocket.TextMessage, peerFrame("auth", map[string]any{"ok": false, "code": "bad_password", "message": "Incorrect password."}))
			return
		}
		greeting := "Welcome back, " + login.Username + "!"
		if login.T == "register" {
			greeting = "Account created! Welcome, " + login.Username + "."
		}
		if conn.WriteMessage(websocket.TextMessage, peerFrame("auth", map[string]any{"ok": true, "message": greeting})) != nil {
			return
		}
		room := "You are in 'Arena'\n\x1b[1;31mTest Orc\x1b[0m\n"
		if conn.WriteMessage(websocket.TextMessage, peerText(room)) != nil {
			return
		}
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if mode == "disconnect" {
				return
			}
			var cmd struct {
				Line string `json:"line"`
			}
			if json.Unmarshal(message, &cmd) != nil {
				return
			}
			text := cmd.Line
			if mode == "server-error" {
				if conn.WriteMessage(websocket.TextMessage, peerFrame("error", map[string]any{"code": "internal", "message": "Something went wrong. Please try again."})) != nil {
					return
				}
			}
			if mode == "rate-limit" {
				if conn.WriteMessage(websocket.TextMessage, peerFrame("error", map[string]any{"code": "rate_limited", "message": "Slow down. Too many commands."})) != nil {
					return
				}
			}
			if strings.HasPrefix(text, "ATTACK ") {
				if conn.WriteMessage(websocket.TextMessage, peerText("You hit Test Orc for 5 points of damage!")) != nil {
					return
				}
			}
			if strings.HasPrefix(text, "WHISPER ") && mode != "stall" {
				fields := strings.SplitN(text, " ", 3)
				// A received whisper containing the same marker must not satisfy
				// the outgoing fence. Only the outgoing echo is a sample.
				if conn.WriteMessage(websocket.TextMessage, peerFrame("whisper", map[string]any{"who": "someone", "text": fields[2]})) != nil {
					return
				}
				if mode == "wrong-echo" {
					continue
				}
				if conn.WriteMessage(websocket.TextMessage, peerFrame("whisper", map[string]any{"who": fields[1], "text": fields[2], "out": true})) != nil {
					return
				}
			}
			if strings.HasPrefix(text, "MOVE ") {
				if conn.WriteMessage(websocket.TextMessage, peerText(room)) != nil {
					return
				}
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func TestRunExercisesProtocolAndReportsLatency(t *testing.T) {
	for _, register := range []bool{false, true} {
		c := testConfig(protocolPeer(t, "ok"))
		c.Register = register
		r, err := Run(context.Background(), c)
		if err != nil {
			t.Fatalf("run: %v; report: %+v", err, r)
		}
		if r.Joined != c.Count || r.ActiveAtStart != int64(c.Count) || r.ActiveAtEnd != int64(c.Count) || r.Disconnects != 0 || r.Timeouts != 0 {
			t.Fatalf("wrong connection accounting: %+v", r)
		}
		if r.MinBotSamples < 8 || r.Moves == 0 || r.Says == 0 || r.Whispers == 0 || r.TargetedAttacks == 0 || r.Hits == 0 || r.Messages == 0 || r.Bytes == 0 {
			t.Fatalf("missing activity: %+v", r)
		}
		if r.Samples <= 0 || r.P50MS > r.P95MS || r.P95MS > r.P99MS || r.Uncompleted != r.Actions-r.Samples {
			t.Fatalf("wrong sample accounting: %+v", r)
		}
	}
}

func TestRunReportsFailuresInsteadOfFastSuccesses(t *testing.T) {
	for _, mode := range []string{"reject", "stall", "wrong-echo", "disconnect"} {
		t.Run(mode, func(t *testing.T) {
			c := testConfig(protocolPeer(t, mode))
			c.Count, c.LoginWorkers = 1, 1
			c.Timeout = 100 * time.Millisecond
			r, err := Run(context.Background(), c)
			if err == nil {
				t.Fatalf("expected failure: %+v", r)
			}
			if r.Samples != 0 || r.P50MS != 0 || r.P95MS != 0 || r.P99MS != 0 {
				t.Fatalf("failure counted as latency success: %+v", r)
			}
			switch mode {
			case "reject":
				if r.JoinFailures != 1 || r.Joined != 0 {
					t.Fatalf("join failure not counted: %+v", r)
				}
			case "stall", "wrong-echo":
				if r.Timeouts != 1 {
					t.Fatalf("timeout not counted: %+v", r)
				}
			case "disconnect":
				if r.Disconnects != 1 {
					t.Fatalf("disconnect not counted: %+v", r)
				}
			}
		})
	}
}

func TestRunBoundsAdmission(t *testing.T) {
	c := testConfig(protocolPeer(t, "stall-login"))
	c.Count = 20
	c.JoinTimeout = 150 * time.Millisecond
	r, err := Run(context.Background(), c)
	if err == nil || r.JoinFailures != c.Count || r.Joined != 0 || r.AdmissionTime > 3*time.Second {
		t.Fatalf("admission was not bounded/accounted: %v; %+v", err, r)
	}
}

func TestRunRejectsServerProblemsEvenWithCompletedFences(t *testing.T) {
	for _, mode := range []string{"server-error", "rate-limit"} {
		c := testConfig(protocolPeer(t, mode))
		c.Count, c.LoginWorkers = 1, 1
		c.Duration = 350 * time.Millisecond
		r, err := Run(context.Background(), c)
		if err == nil || r.Samples == 0 {
			t.Fatalf("server problem not reported with samples: %v; %+v", err, r)
		}
		if mode == "server-error" && r.ServerErrors == 0 {
			t.Fatal("server error not counted")
		}
		if mode == "rate-limit" && r.RateLimited == 0 {
			t.Fatal("rate limit not counted")
		}
	}
}

func TestRunCancellationClosesClients(t *testing.T) {
	c := testConfig(protocolPeer(t, "ok"))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	r, err := Run(ctx, c)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel: %v, report: %+v", err, r)
	}
	if r.Disconnects != 0 {
		t.Fatalf("intentional cleanup counted as disconnect: %+v", r)
	}
}

func TestConfigSafetyAndValidation(t *testing.T) {
	c := testConfig("ws://127.0.0.1:1/ws")
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"ws://example.com/ws", "wss://magmud.crmoore.net/ws", "ws://user:pass@localhost/ws", "http://localhost/ws", "ws://localhost.evil/ws"} {
		bad := c
		bad.URL = target
		if bad.validate() == nil {
			t.Errorf("accepted %q", target)
		}
	}
	remote := c
	remote.URL = "wss://example.com/ws"
	remote.AllowRemote = true
	if err := remote.validate(); err != nil {
		t.Fatalf("explicit remote opt-in: %v", err)
	}
	for _, change := range []func(*Config){
		func(c *Config) { c.Count = 0 }, func(c *Config) { c.LoginWorkers = 0 },
		func(c *Config) { c.LoginWorkers = c.Count + 1 }, func(c *Config) { c.Interval = time.Millisecond },
		func(c *Config) { c.Timeout = time.Hour }, func(c *Config) { c.Duration = 0 },
		func(c *Config) { c.JoinTimeout = 0 }, func(c *Config) { c.Prefix = "bad\nname" }, func(c *Config) { c.Password = "" },
	} {
		bad := c
		change(&bad)
		if bad.validate() == nil {
			t.Errorf("accepted invalid config: %+v", bad)
		}
	}
}

func TestPercentilesAndBoundedSamples(t *testing.T) {
	m := &metrics{hist: make([]int, 11)}
	for i := 0; i < 100; i++ {
		m.sample(time.Duration(i) * time.Microsecond)
	}
	if m.r.Samples != 100 || percentile(m.hist, 100, 95) != 1 {
		t.Fatalf("sub-ms rounding: %+v", m.hist)
	}
	if percentile(m.hist, 0, 99) != 0 {
		t.Fatal("empty histogram must report zero")
	}
	m.sample(time.Hour)
	if len(m.hist) != 11 || m.hist[10] != 1 {
		t.Fatal("histogram must stay bounded")
	}
	hist := []int{0, 50, 45, 4, 1}
	if percentile(hist, 100, 50) != 1 || percentile(hist, 100, 95) != 2 || percentile(hist, 100, 99) != 3 {
		t.Fatal("incorrect nearest-rank percentiles")
	}
	for i := 0; i < 100; i++ {
		m.failure("join", i)
	}
	if m.r.JoinFailures != 100 || len(m.r.Errors) != 10 {
		t.Fatal("error examples must be bounded without losing counts")
	}
}

func TestSeedSafety(t *testing.T) {
	for _, target := range []string{"postgres://u:p@127.0.0.1/mud_load", "postgresql://localhost:5432/mud_load"} {
		if err := validateSeedURL(target); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []string{"postgres://localhost/mud", "postgres://localhost/mud_test", "postgres://example.com/mud_load", "postgres://localhost/mud_load?host=example.com", "postgres://localhost/mud_load?dbname=mud", "host=localhost dbname=mud_load", ""} {
		if validateSeedURL(target) == nil {
			t.Errorf("unsafe seed URL accepted: %q", target)
		}
	}
	if err := SeedPlayers(context.Background(), nil, 0, "bot", "fixture"); err == nil {
		t.Fatal("invalid seed inputs accepted")
	}
}
