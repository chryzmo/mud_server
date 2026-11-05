package main

import (
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	mudserver "mud_server/src/internal/mudserver"
)

// dialWithTinyReceiveBuffer connects with a very small socket receive buffer,
// so a client that stops reading backs up the server's writes quickly.
func dialWithTinyReceiveBuffer(t *testing.T, wsURL string) *websocket.Conn {
	t.Helper()
	d := websocket.Dialer{
		NetDialContext: (&net.Dialer{
			Control: func(_, _ string, c syscall.RawConn) error {
				return c.Control(func(fd uintptr) {
					syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, 4096)
				})
			},
		}).DialContext,
	}
	conn, _, err := d.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// reader reads a connection continuously, like a browser tab does, so the
// client keeps answering pings and its server-side queue never backs up.
// Messages arrive on the returned channel, which closes when the connection does.
func reader(conn *websocket.Conn) <-chan string {
	ch := make(chan string, 10000)
	go func() {
		defer close(ch)
		conn.SetReadDeadline(time.Time{})
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			ch <- string(msg)
		}
	}()
	return ch
}

// await waits for a message containing want on a reader channel.
func await(t *testing.T, ch <-chan string, want string) string {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				t.Fatalf("connection closed while waiting for %q", want)
			}
			if strings.Contains(msg, want) {
				return msg
			}
		case <-timeout:
			t.Fatalf("no %q within 10s", want)
		}
	}
}

// Before sessions had their own queues, a client that stopped reading blocked
// the shared command loop on a websocket write, freezing the game for everyone.
func TestStalledClientDoesNotBlockOthers(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		c.Connection.OutboundQueueSize = 32
		c.Connection.WriteTimeoutMS = 500
	})

	stalled := dialWithTinyReceiveBuffer(t, wsURL)
	expect(t, stalled, "Welcome to the MUD!")
	send(t, stalled, "/REGISTER stalled secret")
	expect(t, stalled, "You are in 'Start Room'")
	// From here on the stalled client never reads again.

	active := register(t, wsURL, "active")
	activeIn := reader(active)

	// Every SAY is also sent to the stalled client: about 8 MB in all, far more
	// than socket buffers can absorb.
	chatter := strings.Repeat("x", 2000)
	start := time.Now()
	for i := 0; i < 4000; i++ {
		send(t, active, "SAY "+chatter)
	}
	send(t, active, "SAY done")
	await(t, activeIn, `You say: "done"`)
	t.Logf("4000 broadcasts handled in %v", time.Since(start).Round(time.Millisecond))

	// The stalled client is dropped, and the game carries on without it.
	deadline := time.Now().Add(10 * time.Second)
	for {
		send(t, active, "LOOK")
		look := await(t, activeIn, "You are in 'Start Room'")
		if !strings.Contains(look, "stalled") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stalled client still in the room after 10s:\n%s", look)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestServerPingsClients(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		c.Connection.PingIntervalMS = 100
	})
	conn := dial(t, wsURL)

	pings := 0
	conn.SetPingHandler(func(data string) error {
		pings++
		return conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
	})
	// Control frames are handled while reading; nothing else arrives after the welcome.
	expectWithin(conn, "never sent", 600*time.Millisecond)
	if pings < 2 {
		t.Errorf("got %d pings in 600ms, want at least 2 at a 100ms interval", pings)
	}
}

func TestUnresponsiveClientIsDisconnected(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		c.Connection.PingIntervalMS = 100
		c.Connection.PongTimeoutMS = 500
	})
	watcher := reader(register(t, wsURL, "watcher"))

	silent := dial(t, wsURL)
	silent.SetPingHandler(func(string) error { return nil }) // never answer
	expect(t, silent, "Welcome to the MUD!")
	send(t, silent, "/REGISTER silent secret")
	expect(t, silent, "You are in 'Start Room'")
	await(t, watcher, "silent\x1b[0m has appeared in the room.")

	start := time.Now()
	silent.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := silent.ReadMessage(); err != nil {
			break
		}
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("disconnected after %v, want about 0.5s", elapsed)
	}
	await(t, watcher, "silent\x1b[0m has poofed away.")
}
