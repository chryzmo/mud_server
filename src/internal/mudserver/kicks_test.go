package mudserver

import (
	"testing"
	"time"
)

func TestKickListExpiresAndIgnoresCase(t *testing.T) {
	var k kickList
	now := time.Now()
	if k.remaining("Bob", now) != 0 {
		t.Errorf("nobody is kicked yet")
	}
	k.set("Bob", now.Add(10*time.Minute))
	for _, name := range []string{"Bob", "bob", "BOB"} {
		if d := k.remaining(name, now); d != 10*time.Minute {
			t.Errorf("remaining(%q) = %v, want 10m", name, d)
		}
	}
	if d := k.remaining("bob", now.Add(4*time.Minute)); d != 6*time.Minute {
		t.Errorf("after 4 minutes remaining = %v, want 6m", d)
	}
	if d := k.remaining("bob", now.Add(10*time.Minute)); d != 0 {
		t.Errorf("when the break ends remaining = %v, want 0", d)
	}
	if k.remaining("alice", now) != 0 {
		t.Errorf("someone else is on a break")
	}
	// Setting an entry drops the ones that have ended.
	k2 := kickList{until: map[string]time.Time{"old": time.Now().Add(-time.Minute)}}
	k2.set("new", time.Now().Add(time.Minute))
	if _, kept := k2.until["old"]; kept {
		t.Errorf("an ended entry was not pruned: %v", k2.until)
	}
}
