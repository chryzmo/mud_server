package main

import (
	"fmt"
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
		Subprotocols: []string{mudserver.Subprotocol},
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
			ch <- flatten(msg)
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
			if hasText(msg, want) {
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
	t.Parallel()
	// Default queue size; a short write timeout so the stalled writer gives up quickly.
	wsURL, ms := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		c.Connection.WriteTimeoutMS = 500
		// This test deliberately floods broadcasts to exercise the outbound
		// queue, not the inbound limiter (covered separately).
		c.Connection.CommandsPerSecond = 100000
		c.Connection.CommandBurst = 10000
		c.Connection.SayPerSecond = 100000
		c.Connection.SayBurst = 10000
	})

	stalled := dialWithTinyReceiveBuffer(t, wsURL)
	expect(t, stalled, "Welcome to the MUD!")
	loginNew(t, stalled, "stalled")
	expect(t, stalled, "You are in 'Start Room'")
	// From here on the stalled client never reads again.

	active := passwordPlayer(t, wsURL, "active")
	activeIn := reader(active)

	// Every SAY is also sent to the stalled client: about 8 MB in all, far more
	// than socket buffers can absorb. Send in batches and wait for each batch's
	// echo, as a real client reading its own output would; flooding thousands
	// of commands unread would (correctly) get the active client dropped too.
	chatter := strings.Repeat("x", 2000)
	start := time.Now()
	for batch := 0; batch < 40; batch++ {
		for i := 0; i < 100; i++ {
			// Each line differs, or automatic moderation (rightly) mutes the repeater.
			send(t, active, fmt.Sprintf("SAY %s %d-%d", chatter, batch, i))
		}
		marker := fmt.Sprintf("batch %d", batch)
		send(t, active, "SAY "+marker)
		await(t, activeIn, `You say: "`+marker+`"`)
	}
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

	// The drop is counted with its reason: the queue filled, or the write timed out.
	deadline = time.Now().Add(5 * time.Second)
	for {
		counts := ms.SessionCloseCounts()
		if counts["queue_full"]+counts["write_error"] >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session closes = %v, want queue_full or write_error", counts)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestServerPingsClients(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	wsURL, ms := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		c.Connection.PingIntervalMS = 100
		c.Connection.PongTimeoutMS = 500
	})
	watcher := reader(passwordPlayer(t, wsURL, "watcher"))

	silent := dial(t, wsURL)
	silent.SetPingHandler(func(string) error { return nil }) // never answer
	expect(t, silent, "Welcome to the MUD!")
	loginNew(t, silent, "silent")
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
	waitForClose(t, ms, "read_timeout", 1)
}

// settle waits until the server has finished writing a new player's welcome. passwordPlayer returns
// at the first look, while stats and the like are still being written; closing then can make the
// server's write fail first and be counted as a write_error, a real thing a vanishing client does
// but not what the close-reason tests are about. The last frame of a command's reply means the server has gone quiet.
func settle(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	send(t, conn, "STATS")
	expect(t, conn, "Your stats:") // only the reply says this; a stats frame from the welcome could match STATUPDATE alone
	expect(t, conn, "STATUPDATE")  // the last thing the reply writes
}

// A client that says goodbye and one whose connection just vanishes are
// different reasons, and neither is a server-side drop.
func TestSessionCloseReasonsForClientDisconnects(t *testing.T) {
	t.Parallel()
	wsURL, ms := startTestServerWithConfig(t, func(*mudserver.Configuration) {})

	polite := passwordPlayer(t, wsURL, "polite")
	settle(t, polite)
	polite.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	// A polite client waits for the server's close in reply before it drops the connection; closing
	// at once can make the server's reply fail to write and be counted as a write_error instead.
	polite.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := polite.ReadMessage(); err != nil {
			break
		}
	}
	polite.Close()
	waitForClose(t, ms, "client_closed", 1)

	vanishing := passwordPlayer(t, wsURL, "vanishing")
	settle(t, vanishing)
	vanishing.UnderlyingConn().Close() // no close frame
	counts := waitForClose(t, ms, "read_error", 1)

	for _, serverSide := range []string{"queue_full", "write_error", "ping_error", "read_timeout", "read_limit"} {
		if counts[serverSide] != 0 {
			t.Errorf("%s = %d after ordinary client disconnects, want 0 (counts %v)", serverSide, counts[serverSide], counts)
		}
	}
}
