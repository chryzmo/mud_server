package mudserver

import (
	"testing"
	"time"
)

func idleServer(timeoutMS int) *MudServer {
	ms := &MudServer{players: make(map[string]*MudPlayer)}
	ms.Configuration.Connection.IdleTimeoutMS = timeoutMS
	return ms
}

func idlePlayer(name string, guest bool, lastActive time.Time) *MudPlayer {
	return &MudPlayer{Username: name, guest: guest, lastActive: lastActive,
		Session: &Session{out: make(chan []byte, 8), closed: make(chan struct{})}}
}

func TestIdleTimeoutSetting(t *testing.T) {
	for _, tc := range []struct {
		configured int
		want       time.Duration
	}{{0, 30 * time.Minute}, {-1, 0}, {-500, 0}, {250, 250 * time.Millisecond}, {1800000, 30 * time.Minute}} {
		if got := idleServer(tc.configured).idleTimeout(); got != tc.want {
			t.Errorf("IdleTimeoutMS %d = %v, want %v", tc.configured, got, tc.want)
		}
	}
}

func TestReclaimIdleKicksOnlyIdleLiveGuests(t *testing.T) {
	now := time.Unix(10000, 0)
	ms := idleServer(60000) // one minute
	idle := idlePlayer("idle", true, now.Add(-2*time.Minute))
	active := idlePlayer("active", true, now.Add(-10*time.Second))
	exactly := idlePlayer("exactly", true, now.Add(-time.Minute)) // not more than the limit yet
	gm := idlePlayer("gm", false, now.Add(-10*time.Hour))
	dormant := idlePlayer("dormant", true, now.Add(-10*time.Hour))
	dormant.dormant = true
	closed := idlePlayer("closed", true, now.Add(-10*time.Hour))
	closed.Session.Close()
	for _, p := range []*MudPlayer{idle, active, exactly, gm, dormant, closed} {
		ms.players[p.Username] = p
	}

	ms.reclaimIdle(now)

	if !idle.Session.isClosed() || idle.Session.reason != closeIdle {
		t.Errorf("the idle guest was not kicked (closed=%v reason=%q)", idle.Session.isClosed(), idle.Session.reason)
	}
	for _, p := range []*MudPlayer{active, exactly, gm, dormant} {
		if p.Session.isClosed() {
			t.Errorf("%s was kicked", p.Username)
		}
	}
	if closed.Session.reason != closeServer {
		t.Errorf("an already-closed session's reason was overwritten: %q", closed.Session.reason)
	}
	// The kicked guest was told why before the close.
	select {
	case frame := <-idle.Session.out:
		if got := string(frame); got != `{"t":"error","code":"idle","message":"You were away for a while, so you were disconnected. Tap to rejoin."}` {
			t.Errorf("idle notice = %s", got)
		}
	default:
		t.Error("the idle guest was closed without being told")
	}
	// A second pass does not message them again.
	ms.reclaimIdle(now.Add(time.Second))
	if len(idle.Session.out) != 0 {
		t.Error("an already-kicked guest was messaged again")
	}
}

func TestReclaimIdleDisabled(t *testing.T) {
	ms := idleServer(-1)
	p := idlePlayer("old", true, time.Unix(0, 0))
	ms.players["old"] = p
	ms.reclaimIdle(time.Unix(1<<31, 0))
	if p.Session.isClosed() {
		t.Error("a guest was kicked with idle reclaim disabled")
	}
}
