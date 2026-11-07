package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	mudserver "mud_server/src/internal/mudserver"
)

// paddedCmd is an unknown-command message of exactly size bytes.
func paddedCmd(size int) string {
	prefix, suffix := `{"t":"cmd","line":"FROBNICATE `, `"}`
	return prefix + strings.Repeat("x", size-len(prefix)-len(suffix)) + suffix
}

func TestReadLimitDisconnectsOversizedMessages(t *testing.T) {
	for _, tc := range []struct {
		name       string
		loggedIn   bool
		fragmented bool
		kind       int
	}{
		{"before login", false, false, websocket.TextMessage},
		{"after login", true, false, websocket.TextMessage},
		{"fragmented text", true, true, websocket.TextMessage},
		{"fragmented binary", false, true, websocket.BinaryMessage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wsURL, ms := startTestServerWithConfig(t, func(*mudserver.Configuration) {})
			watcher := passwordPlayer(t, wsURL, "watcher")
			conn := dial(t, wsURL)
			expect(t, conn, "Welcome to the MUD!")
			if tc.loggedIn {
				loginNew(t, conn, "oversized")
				expect(t, conn, "You are in 'Start Room'")
			}

			// Exactly 4 KB is allowed, including before login.
			message := paddedCmd(4096)
			if err := conn.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
				t.Fatal(err)
			}
			if tc.loggedIn {
				expect(t, conn, "Unknown command.")
			} else {
				expect(t, conn, "Please join first.")
			}
			message += "x"
			if tc.fragmented {
				w, err := conn.NextWriter(tc.kind)
				if err != nil {
					t.Fatal(err)
				}
				// Gorilla's default write buffer forces continuation frames.
				for start := 0; start < len(message); start += 1024 {
					if _, err := w.Write([]byte(message[start:min(start+1024, len(message))])); err != nil {
						t.Fatal(err)
					}
				}
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := conn.WriteMessage(tc.kind, []byte(message)); err != nil {
					t.Fatal(err)
				}
			}
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					var closeError *websocket.CloseError
					if !errors.As(err, &closeError) || closeError.Code != websocket.CloseMessageTooBig {
						t.Fatalf("oversized message: got %v, want close 1009", err)
					}
					break
				}
			}
			waitForClose(t, ms, "read_limit", 1)
			// Another session still plays normally.
			send(t, watcher, "MOVE N")
			expect(t, watcher, "You are in 'Balcony'")
			if tc.loggedIn {
				resumed, greeting := loginWhenFree(t, wsURL, "oversized", "secret")
				if !strings.Contains(greeting, "Welcome back, oversized!") {
					t.Fatalf("login after oversized disconnect: %q", greeting)
				}
				expect(t, resumed, "You are in 'Start Room'")
			}
		})
	}
}

func TestCommandFloodIsLimitedBeforeAndAfterLogin(t *testing.T) {
	for _, loggedIn := range []bool{false, true} {
		name := "before login"
		if loggedIn {
			name = "after login"
		}
		t.Run(name, func(t *testing.T) {
			wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
				c.Connection.CommandsPerSecond = 1
				c.Connection.CommandBurst = 2
			})
			conn := dial(t, wsURL)
			expect(t, conn, "Welcome to the MUD!")
			if loggedIn {
				loginNew(t, conn, "flooder")
				expect(t, conn, "You are in 'Start Room'")
			}
			incoming := reader(conn)
			for i := 0; i < 200; i++ {
				// Missing parameters take the cheap validation path if allowed;
				// the limiter must run even before authentication.
				send(t, conn, "/LOGIN")
			}
			// Count permitted replies as well as notices: sending a notice
			// without actually dropping commands must fail this test. After
			// the first notice, allow queued command replies to drain briefly.
			replies, notices := 0, 0
			deadline := time.NewTimer(10 * time.Second)
			defer deadline.Stop()
		collect:
			for {
				select {
				case message, ok := <-incoming:
					if !ok {
						t.Fatal("flooded session closed instead of dropping excess commands")
					}
					switch {
					case strings.Contains(message, "Slow down."):
						notices++
						if notices == 1 {
							deadline.Reset(200 * time.Millisecond)
						}
					case strings.Contains(message, "needs a username and a password"), strings.Contains(message, "Unknown command."):
						replies++
					}
				case <-deadline.C:
					break collect
				}
			}
			if notices != 1 || replies < 1 || replies > 2 {
				t.Fatalf("flood: %d permitted replies, %d notices; want 1–2 replies and one notice", replies, notices)
			}
			// Limits are per session, not global. A flood must not stop a new
			// session from logging in or executing a command.
			other := passwordPlayer(t, wsURL, "other")
			send(t, other, "MOVE N")
			expect(t, other, "You are in 'Balcony'")
		})
	}
}
