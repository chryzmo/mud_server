package main

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	mudserver "mud_server/src/internal/mudserver"
)

// joinFrom joins as name over a connection that carries the given X-Client-IP
// header, the way Caddy forwards a client's address.
func joinFrom(t *testing.T, wsURL, name, ip string) map[string]any {
	t.Helper()
	header := http.Header{}
	if ip != "" {
		header.Set("X-Client-IP", ip)
	}
	conn, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	expect(t, conn, "Welcome to the MUD!")
	writeRaw(t, conn, fmt.Sprintf(`{"t":"join","name":%q}`, name))
	for {
		typ, m := readMessage(t, conn)
		if typ == "auth" || typ == "error" {
			return m
		}
	}
}

func perIP(limit int, trusted []string) func(*mudserver.Configuration) {
	return func(c *mudserver.Configuration) {
		c.Connection.MaxJoinsPerMinutePerIP = limit
		c.Server.TrustedProxies = trusted
	}
}

func TestPerIPCeilingUsesTheForwardedAddress(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, perIP(3, nil)) // default trust: loopback, which is where the test connects from
	for i := 0; i < 3; i++ {
		if got := joinFrom(t, wsURL, fmt.Sprintf("Venue%d", i), "203.0.113.50"); got["ok"] != true {
			t.Fatalf("join %d from the venue address: %v", i, got)
		}
	}
	got := joinFrom(t, wsURL, "Venue3", "203.0.113.50")
	if got["code"] != "rate_limited" || got["retry_ms"] == nil {
		t.Fatalf("a fourth join from one address = %v, want rate_limited with a hint", got)
	}
	if n := queryString(t, "SELECT count(*)::text FROM player WHERE username='Venue3'"); n != "0" {
		t.Errorf("a refused join created %s rows", n)
	}
	// Another address is untouched, and so is a connection with no header at all (the loopback peer itself).
	if got := joinFrom(t, wsURL, "Elsewhere", "203.0.113.51"); got["ok"] != true {
		t.Errorf("a different address was limited: %v", got)
	}
	if got := joinFrom(t, wsURL, "Direct", ""); got["ok"] != true {
		t.Errorf("a connection without the header was limited by someone else's flood: %v", got)
	}
}

// An address that is not a trusted proxy cannot choose its own identity: the header
// is ignored, so rotating it does not dodge the ceiling.
func TestPerIPCeilingIgnoresTheHeaderFromAnUntrustedPeer(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, perIP(3, []string{"10.0.0.0/8"})) // the test connects from 127.0.0.1, which is not trusted now
	refused := 0
	for i := 0; i < 8; i++ {
		got := joinFrom(t, wsURL, fmt.Sprintf("Spoof%d", i), fmt.Sprintf("203.0.113.%d", i)) // a new claimed address each time
		if got["code"] == "rate_limited" {
			refused++
		}
	}
	if refused < 4 {
		t.Errorf("only %d of 8 joins were refused; rotating a spoofed header dodged the ceiling", refused)
	}
}

func TestPerIPCeilingIsOffByDefault(t *testing.T) {
	wsURL := startTestServer(t)
	for i := 0; i < 20; i++ {
		if got := joinFrom(t, wsURL, fmt.Sprintf("Anyone%02d", i), "203.0.113.77"); got["ok"] != true {
			t.Fatalf("join %d from one address with no ceiling configured: %v", i, got)
		}
	}
}

func TestBadTrustedProxiesRefuseToStart(t *testing.T) {
	url := testDatabaseURL(t)
	resetDatabase(t, url)
	c := mudserver.Configuration{}
	c.Database.DatabaseURL = url
	c.Server.TrustedProxies = []string{"10.0.0.0/8", "not-a-network"}
	if _, err := mudserver.NewMudServer(c); err == nil || !strings.Contains(err.Error(), "TrustedProxies") {
		t.Errorf("NewMudServer with a bad proxy list = %v, want an error naming TrustedProxies", err)
	}
}

// logBuffer collects server log lines; the server writes from several goroutines.
type logBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// waitFor polls until the log holds want, since the server logs just after it replies.
func (l *logBuffer) waitFor(t *testing.T, want string) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if strings.Contains(l.String(), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("log lacks %q; got:\n%s", want, l.String())
		}
	}
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func TestJoinsAreLoggedWithTheClientAddress(t *testing.T) {
	var log logBuffer
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) { c.Server.LogWriter = &log })
	joinFrom(t, wsURL, "Logged", "203.0.113.7")
	joinFrom(t, wsURL, "NoHeader", "")
	log.waitFor(t, "Guest joined: Logged (ip 203.0.113.7)")
	log.waitFor(t, "Guest joined: NoHeader (ip 127.0.0.1)")
}

func TestRecoveryAttemptsAreLoggedWithTheClientAddress(t *testing.T) {
	var log logBuffer
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) { c.Server.LogWriter = &log })
	g := joinGuest(t, wsURL, "Forgetful")
	g.drop()
	recoverFromIP := func(code, ip string) {
		header := http.Header{}
		header.Set("X-Client-IP", ip)
		conn, _, err := dialer.Dial(wsURL, header)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		expect(t, conn, "Welcome to the MUD!")
		writeRaw(t, conn, fmt.Sprintf(`{"t":"recover","name":"Forgetful","code":%q}`, code))
		expectMessage(t, conn, "auth")
	}
	recoverFromIP("AAAA-BBBB-CCCC", "198.51.100.20")
	recoverFromIP(g.recovery, "198.51.100.21")
	log.waitFor(t, "Recovery failed (wrong code): Forgetful (ip 198.51.100.20)")
	log.waitFor(t, "Recovery succeeded: Forgetful (ip 198.51.100.21)")
}

func TestClientAddressLoggingCanBeSwitchedOff(t *testing.T) {
	var log logBuffer
	off := false
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) { c.Server.LogClientIPs = &off; c.Server.LogWriter = &log })
	joinFrom(t, wsURL, "Private", "203.0.113.8")
	time.Sleep(200 * time.Millisecond) // long enough for a line that was going to be written
	if out := log.String(); out != "" {
		t.Errorf("addresses or joins were logged with LogClientIPs off:\n%s", out)
	}
}
