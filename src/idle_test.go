package main

import (
	"testing"
	"time"

	mudserver "mud_server/src/internal/mudserver"
)

func idleConfig(idleMS, graceMS int) func(*mudserver.Configuration) {
	return func(c *mudserver.Configuration) {
		c.Connection.IdleTimeoutMS = idleMS
		c.Connection.ResumeGraceMS = graceMS
	}
}

// awaitIdleError reads until the idle notice arrives and returns when it did.
func (g *guest) awaitIdleError() time.Time {
	g.t.Helper()
	g.until(func(typ string, m map[string]any) bool { return typ == "error" && m["code"] == "idle" })
	return time.Now()
}

func TestIdleGuestIsToldThenGoesDormantThenLeaves(t *testing.T) {
	wsURL, ms := startTestServerWithConfig(t, idleConfig(500, 700))
	watcher := reader(passwordPlayer(t, wsURL, "Watcher")) // a password account is never idle-kicked
	idler := joinGuest(t, wsURL, "Idler")
	await(t, watcher, "Idler\x1b[0m has appeared in the room.")

	told := idler.awaitIdleError()
	waitForClose(t, ms, "idle", 1)
	idler.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := idler.conn.ReadMessage(); err != nil {
			break // the server closed the connection after the notice
		}
	}

	// Kicked guests go through the usual grace period: nobody is told at once...
	await(t, watcher, "Idler\x1b[0m has poofed away.")
	if gone := time.Since(told); gone < 400*time.Millisecond {
		t.Errorf("the guest left the room %v after the notice, want after the grace period (700 ms)", gone)
	}
}

func TestActiveGuestsAreNotKicked(t *testing.T) {
	wsURL, ms := startTestServerWithConfig(t, idleConfig(700, 5000))
	busy := joinGuest(t, wsURL, "Busy")
	sleepy := joinGuest(t, wsURL, "Sleepy")

	deadline := time.Now().Add(2200 * time.Millisecond)
	for time.Now().Before(deadline) {
		send(t, busy.conn, "LOOK")
		time.Sleep(200 * time.Millisecond)
	}
	waitForClose(t, ms, "idle", 1)
	if counts := ms.SessionCloseCounts(); counts["idle"] != 1 {
		t.Errorf("idle closes = %d, want only the sleepy guest", counts["idle"])
	}
	// Busy kept talking to the server throughout and was told nothing: the frames
	// queued for it so far (replies to its LOOKs) hold no error. Reading until the
	// last room frame avoids a read timeout, which would fail the connection.
	send(t, busy.conn, "LOOK")
	seen := busy.until(func(typ string, m map[string]any) bool { return typ == "room" })
	for _, m := range seen {
		if m["t"] == "error" {
			t.Errorf("an active guest got an error: %v", m)
		}
	}
	_ = sleepy
}

// Coming back counts as activity: after an idle kick, a resume within the grace
// period works, and the clock restarts instead of kicking them again at once.
func TestResumingAfterAnIdleKickRestartsTheClock(t *testing.T) {
	wsURL, ms := startTestServerWithConfig(t, idleConfig(600, 10000))
	g := joinGuest(t, wsURL, "Dozer")
	g.awaitIdleError()
	g.drop()
	time.Sleep(50 * time.Millisecond)

	auth, _ := g.reconnect()
	if auth["ok"] != true || auth["gap"] != nil {
		t.Fatalf("resume after an idle kick = %v, want a clean resume while still dormant", auth)
	}
	// The clock restarted on the resume, so the next kick comes a full timeout later,
	// not at once.
	resumed := time.Now()
	g.awaitIdleError()
	if waited := time.Since(resumed); waited < 400*time.Millisecond {
		t.Errorf("kicked %v after resuming, want about the 600 ms timeout (the clock did not restart)", waited)
	}
	waitForClose(t, ms, "idle", 2)
}

func TestPasswordPlayersAreNeverIdleKicked(t *testing.T) {
	wsURL, ms := startTestServerWithConfig(t, idleConfig(300, 5000))
	gm := passwordPlayer(t, wsURL, "Gamemaster")
	time.Sleep(1500 * time.Millisecond)
	send(t, gm, "LOOK")
	expect(t, gm, "You are in 'Start Room'")
	if counts := ms.SessionCloseCounts(); counts["idle"] != 0 {
		t.Errorf("idle closes = %d, want none for a password account", counts["idle"])
	}
}

func TestIdleReclaimCanBeDisabled(t *testing.T) {
	wsURL, ms := startTestServerWithConfig(t, idleConfig(-1, 5000))
	g := joinGuest(t, wsURL, "Forever")
	time.Sleep(1500 * time.Millisecond)
	send(t, g.conn, "LOOK")
	g.until(func(typ string, m map[string]any) bool { return typ == "room" })
	if counts := ms.SessionCloseCounts(); counts["idle"] != 0 {
		t.Errorf("idle closes = %d with reclaim disabled", counts["idle"])
	}
}
