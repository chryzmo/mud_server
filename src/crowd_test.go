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

func crowdConfig(softCap, maxCopies int) func(*mudserver.Configuration) {
	return crowdConfigCaps(softCap, 0, maxCopies)
}

func crowdConfigCaps(softCap, hardCap, maxCopies int) func(*mudserver.Configuration) {
	return func(c *mudserver.Configuration) {
		c.Crowd = mudserver.CrowdConfig{
			SoftCap: softCap, HardCap: hardCap, MaxCopies: maxCopies,
			Areas: []mudserver.AreaConfig{{Name: "start", Rooms: []int32{1, 2, 3}}},
		}
	}
}

// lookNames returns the LOOK text for a connection.
func lookText(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	send(t, conn, "LOOK")
	return expect(t, conn, "You are in 'Start Room'")
}

func TestCrowdSpreadsPlayersAcrossCopies(t *testing.T) {
	t.Parallel()
	wsURL, _ := startTestServerWithConfig(t, crowdConfig(2, 10))
	ann := passwordPlayer(t, wsURL, "ann")
	bob := passwordPlayer(t, wsURL, "bob")
	cyd := passwordPlayer(t, wsURL, "cyd") // copy 0 is at the soft cap of 2

	// ann and bob share a room; cyd is alone in another copy of the same room.
	if look := lookText(t, ann); !strings.Contains(look, "bob") || strings.Contains(look, "cyd") {
		t.Errorf("ann sees:\n%s\nwant bob but not cyd", look)
	}
	if look := lookText(t, cyd); strings.Contains(look, "ann") || strings.Contains(look, "bob") {
		t.Errorf("cyd sees:\n%s\nwant nobody else", look)
	}

	// Room talk stays in the copy; whispers reach everyone. The loop handles
	// commands in order, so a SAY that reached ann would arrive before the whisper.
	annIn := reader(ann)
	send(t, cyd, "SAY anyone?")
	send(t, cyd, "WHISPER ann across the copies")
	for {
		select {
		case m := <-annIn:
			if strings.Contains(m, "anyone?") {
				t.Fatal("ann heard a SAY from another copy")
			}
			if strings.Contains(m, "cyd whispers") {
				goto whispered
			}
		case <-time.After(3 * time.Second):
			t.Fatal("the whisper never reached ann")
		}
	}
whispered:

	// Fill first: the next arrival joins cyd's copy (1 of 2), not a fresh one.
	dee := passwordPlayer(t, wsURL, "dee")
	if look := lookText(t, dee); !strings.Contains(look, "cyd") || strings.Contains(look, "ann") {
		t.Errorf("dee sees:\n%s\nwant cyd's copy", look)
	}
	_ = bob
}

func TestCopiesAreNeverSavedAndPlayersSaveTheirTemplate(t *testing.T) {
	t.Parallel()
	wsURL, _ := startTestServerWithConfig(t, crowdConfig(1, 10))
	passwordPlayer(t, wsURL, "first")
	second := passwordPlayer(t, wsURL, "second") // in a clone
	send(t, second, "MOVE N")
	expect(t, second, "You are in 'Balcony'")
	second.Close()

	eventually(t, "second's saved room is the template Balcony", "SELECT roomid::text FROM player WHERE username='second'", is("2"))
	eventually(t, "second's spawn room is the template Start Room", "SELECT spawnlocationid::text FROM player WHERE username='second'", is("1"))
	if n := queryString(t, "SELECT count(*)::text FROM room"); n != "3" {
		t.Errorf("room table has %s rows, want the 3 seeded rooms and no clones", n)
	}

	// Logging back in places them in a copy of the Balcony, not a vanished clone.
	again, greeting := loginWhenFree(t, wsURL, "second", "secret")
	if !strings.Contains(greeting, "Welcome back, second!") {
		t.Fatalf("login = %q", greeting)
	}
	expect(t, again, "You are in 'Balcony'")
}

func TestAtCapacityRefusesNewcomersUntilSomeoneLeaves(t *testing.T) {
	t.Parallel()
	wsURL, _ := startTestServerWithConfig(t, crowdConfig(1, 1))
	ann := passwordPlayer(t, wsURL, "ann")

	createAccount(t, "late", "secret")
	late := dial(t, wsURL)
	expect(t, late, "Welcome to the MUD!")

	// With the world full, logging in is refused.
	writeRaw(t, late, `{"t":"login","username":"late","password":"secret"}`)
	if again := expectMessage(t, late, "auth"); again["code"] != "at_capacity" {
		t.Errorf("login at capacity = %v", again)
	}

	ann.Close()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		writeRaw(t, late, `{"t":"login","username":"late","password":"secret"}`)
		got := expectMessage(t, late, "auth")
		if got["ok"] == true {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("still refused after the only player left: %v", got)
		}
	}
}

// Arrivals open copies on the game loop while other logins are in flight on
// connection readers; the race detector checks that no reader touches the map.
func TestConcurrentArrivalsOpenCopiesSafely(t *testing.T) {
	t.Parallel()
	wsURL, _ := startTestServerWithConfig(t, crowdConfig(3, 100))
	const players = 30
	for i := 0; i < players; i++ {
		createAccount(t, fmt.Sprintf("crowd%02d", i), "secret")
	}
	var wg sync.WaitGroup
	errs := make(chan error, players)
	for i := 0; i < players; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conn, _, err := dialer.Dial(wsURL, nil)
			if err != nil {
				errs <- err
				return
			}
			defer conn.Close()
			conn.WriteMessage(websocket.TextMessage, wireLine(fmt.Sprintf("/LOGIN crowd%02d secret", i)))
			deadline := time.Now().Add(10 * time.Second)
			for {
				conn.SetReadDeadline(deadline)
				_, msg, err := conn.ReadMessage()
				if err != nil {
					errs <- fmt.Errorf("crowd%02d: %v", i, err)
					return
				}
				if strings.Contains(flatten(msg), "You are in 'Start Room'") {
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestCrowdedRoomRefusesMovesOverTheWire(t *testing.T) {
	t.Parallel()
	wsURL, _ := startTestServerWithConfig(t, crowdConfigCaps(5, 1, 10))
	ann := passwordPlayer(t, wsURL, "ann")
	bob := passwordPlayer(t, wsURL, "bob") // placement fills copy 0 up to the soft cap of 5

	send(t, ann, "MOVE N")
	expect(t, ann, "You are in 'Balcony'")

	send(t, bob, "MOVE N") // the Balcony is at the hard cap of 1
	expect(t, bob, "too crowded")
	if look := lookText(t, bob); !strings.Contains(look, "Start Room") {
		t.Errorf("bob after a refused move:\n%s", look)
	}

	// The cap applies to every room, including the one ann wants to return to.
	send(t, ann, "MOVE S")
	expect(t, ann, "too crowded")

	// Once ann moves on, the Balcony is free.
	send(t, ann, "MOVE E")
	expect(t, ann, "You are in 'Arena'")
	send(t, bob, "MOVE N")
	expect(t, bob, "You are in 'Balcony'")
}
