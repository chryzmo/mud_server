package mudserver

import (
	"testing"
	"time"
)

func guardAt(lock time.Duration) (*recoveryGuard, *time.Time) {
	now := time.Unix(1000, 0)
	g := newRecoveryGuard(lock)
	g.now = func() time.Time { return now }
	return g, &now
}

func TestRecoveryGuardLocksAfterFiveFailuresForTheLockDuration(t *testing.T) {
	g, now := guardAt(15 * time.Minute)
	for i := 0; i < maxRecoveryFailures-1; i++ {
		g.fail("Ann")
		if _, locked := g.locked("Ann"); locked {
			t.Fatalf("locked after only %d failures", i+1)
		}
	}
	g.fail("ann") // the fifth, spelled differently: names are case-insensitive
	remaining, locked := g.locked("ANN")
	if !locked || remaining != 15*time.Minute {
		t.Fatalf("after five failures: locked=%v remaining=%v, want a 15-minute lock", locked, remaining)
	}
	if _, locked := g.locked("Bob"); locked {
		t.Error("one name's failures locked another")
	}

	*now = now.Add(14 * time.Minute)
	if remaining, locked := g.locked("Ann"); !locked || remaining != time.Minute {
		t.Errorf("14 minutes later: locked=%v remaining=%v", locked, remaining)
	}
	*now = now.Add(time.Minute + time.Second)
	if _, locked := g.locked("Ann"); locked {
		t.Error("still locked after the lock duration")
	}
}

func TestRecoveryGuardForgetsOldFailuresAndSuccessClears(t *testing.T) {
	g, now := guardAt(15 * time.Minute)
	for i := 0; i < maxRecoveryFailures-1; i++ {
		g.fail("Slow")
	}
	*now = now.Add(16 * time.Minute) // long enough that those failures no longer count
	g.fail("Slow")
	if _, locked := g.locked("Slow"); locked {
		t.Error("failures spread over more than the lock duration still locked the name")
	}

	for i := 0; i < maxRecoveryFailures-1; i++ {
		g.fail("Lucky")
	}
	g.succeed("Lucky")
	g.fail("Lucky")
	if _, locked := g.locked("Lucky"); locked {
		t.Error("a success must clear the failure count")
	}
	if len(g.entries) != 2 {
		t.Errorf("entries = %d, want the two names that failed (Slow, Lucky)", len(g.entries))
	}
}

func TestRecoveryGuardDefaultsAndUnknownNames(t *testing.T) {
	if g := newRecoveryGuard(0); g.lock != defaultRecoveryLock {
		t.Errorf("default lock = %v", g.lock)
	}
	g := newRecoveryGuard(time.Minute)
	if _, locked := g.locked("nobody"); locked {
		t.Error("an unknown name is not locked")
	}
	g.succeed("nobody") // must not panic or create an entry
	if len(g.entries) != 0 {
		t.Error("succeed created an entry")
	}
}

func TestNoFramesSeenIsAlwaysAGap(t *testing.T) {
	p := ringPlayer(5, 3)
	if _, ok := p.framesAfter(noFramesSeen); ok {
		t.Error("a client that remembers nothing must be given a gap, never a replay")
	}
	empty := ringPlayer(5, 0)
	if _, ok := empty.framesAfter(noFramesSeen); ok {
		t.Error("even with nothing sent, 'remembers nothing' must not look like caught up")
	}
}
