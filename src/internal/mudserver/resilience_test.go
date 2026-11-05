package mudserver

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"
)

// A real game loop with no database/world content. The buffered save sink and
// completed saver let its normal shutdown path finish without database I/O.
func startBareLoop(t *testing.T) *MudServer {
	t.Helper()
	ms := &MudServer{
		players:   make(map[string]*MudPlayer),
		events:    make(chan func(), 10),
		World:     &World{},
		done:      make(chan struct{}),
		loopDone:  make(chan struct{}),
		saves:     make(chan saveBatch, 1),
		saverDone: make(chan struct{}),
	}
	close(ms.saverDone)
	go ms.run()
	t.Cleanup(func() {
		// Fake players have no persisted state. Remove them on the loop before
		// asking the normal shutdown path to snapshot the empty world.
		callLoop(t, ms, func() { clear(ms.players) })
		close(ms.done)
		select {
		case <-ms.loopDone:
		case <-time.After(3 * time.Second):
			t.Error("loop did not shut down")
		}
	})
	return ms
}

func callLoop(t *testing.T, ms *MudServer, f func()) {
	t.Helper()
	finished := make(chan bool, 1)
	go func() { finished <- ms.onLoop(f) }()
	select {
	case ran := <-finished:
		if !ran {
			t.Fatal("loop stopped before event ran")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("onLoop waiter stuck")
	}
}

func testOutput(t *testing.T, s *Session, want string) {
	t.Helper()
	select {
	case message := <-s.out:
		if !strings.Contains(string(message), want) {
			t.Fatalf("got %q, want %q", message, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("no %q from session", want)
	}
}

func TestPanickingCommandNotifiesPlayerAndLoopContinues(t *testing.T) {
	ms := startBareLoop(t)
	first := &MudPlayer{Username: "first", Session: &Session{out: make(chan []byte, 10), closed: make(chan struct{})}}
	other := &MudPlayer{Username: "other", Session: &Session{out: make(chan []byte, 10), closed: make(chan struct{})}}
	callLoop(t, ms, func() {
		ms.players[first.Username] = first
		ms.players[other.Username] = other
		ms.Commands = map[string]*MudCommand{
			"PANIC": {action: func(*MudPlayer, string) int { panic("injected command failure") }},
			"OK": {action: func(p *MudPlayer, _ string) int {
				p.SendMessage("still playing")
				return 0
			}},
		}
	})
	ms.enqueue(func() { ms.handleCommand(first, MudMessage{Command: "PANIC"}) })
	testOutput(t, first.Session, "Something went wrong")
	// The same player and another player can both keep issuing commands.
	ms.enqueue(func() { ms.handleCommand(first, MudMessage{Command: "OK"}) })
	ms.enqueue(func() { ms.handleCommand(other, MudMessage{Command: "OK"}) })
	testOutput(t, first.Session, "still playing")
	testOutput(t, other.Session, "still playing")
}

func TestPanickingQueuedEventReleasesWaiterAndLoopContinues(t *testing.T) {
	ms := startBareLoop(t)
	callLoop(t, ms, func() { panic("injected queued event failure") })
	// This would hang if recovery wrapped the entire loop rather than each event.
	callLoop(t, ms, func() {})
}

func TestEventRecoveryLogsStack(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	runEvent("test event", func() { panic("injected failure") })
	for _, want := range []string{"panic in test event", "injected failure", "resilience_test.go", "goroutine"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("log missing %q: %s", want, output.String())
		}
	}
}
