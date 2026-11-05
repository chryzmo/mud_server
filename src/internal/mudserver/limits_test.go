package mudserver

import (
	"testing"
	"time"
)

func TestCommandLimiterDefaultBurstAndRefill(t *testing.T) {
	now := time.Unix(100, 0)
	l := newCommandLimiter(SessionSettings{}, now)
	for i := 0; i < 20; i++ {
		if allowed, notify := l.allow(now); !allowed || notify {
			t.Fatalf("burst command %d: allowed=%v notify=%v", i, allowed, notify)
		}
	}
	if allowed, notify := l.allow(now); allowed || !notify {
		t.Fatalf("excess command: allowed=%v notify=%v", allowed, notify)
	}
	for i := 0; i < 10000; i++ {
		if allowed, notify := l.allow(now); allowed || notify {
			t.Fatalf("repeated excess: allowed=%v notify=%v", allowed, notify)
		}
	}
	// A fractional token doesn't permit a command, but isn't discarded.
	if allowed, _ := l.allow(now.Add(50 * time.Millisecond)); allowed {
		t.Fatal("half a token permitted a command")
	}
	if allowed, notify := l.allow(now.Add(100 * time.Millisecond)); !allowed || notify {
		t.Fatalf("refill at 10/s: allowed=%v notify=%v", allowed, notify)
	}
	if allowed, notify := l.allow(now.Add(100 * time.Millisecond)); allowed || notify {
		t.Fatalf("refill permitted more than one command: allowed=%v notify=%v", allowed, notify)
	}
	// Idle time must not accumulate more than a burst.
	later := now.Add(time.Hour)
	for i := 0; i < 20; i++ {
		if allowed, _ := l.allow(later); !allowed {
			t.Fatalf("idle burst command %d rejected", i)
		}
	}
	if allowed, notify := l.allow(later); allowed || !notify {
		t.Fatalf("idle burst overflow: allowed=%v notify=%v", allowed, notify)
	}
}

func TestCommandLimiterNoticeIntervalAndIndependentSessions(t *testing.T) {
	now := time.Unix(100, 0)
	settings := SessionSettings{CommandsPerSecond: 1, CommandBurst: 1}
	l := newCommandLimiter(settings, now)
	l.allow(now)
	if allowed, notify := l.allow(now); allowed || !notify {
		t.Fatal("first rejection should notify")
	}
	if allowed, notify := l.allow(now.Add(999 * time.Millisecond)); allowed || notify {
		t.Fatal("rejection within one second should be silent")
	}
	if allowed, _ := l.allow(now.Add(time.Second)); !allowed {
		t.Fatal("one-second refill should permit one command")
	}
	if allowed, notify := l.allow(now.Add(time.Second)); allowed || !notify {
		t.Fatal("rejection after one second should notify again")
	}
	other := newCommandLimiter(settings, now)
	if allowed, _ := other.allow(now); !allowed {
		t.Fatal("one session exhausted another session's bucket")
	}
}

func TestSessionLimitDefaultsAndConfig(t *testing.T) {
	s := (SessionSettings{}).withDefaults()
	if s.ReadLimitBytes != 4096 || s.CommandsPerSecond != 10 || s.CommandBurst != 20 {
		t.Fatalf("wrong default limits: %+v", s)
	}
	var c Configuration
	c.Connection.ReadLimitBytes = 1234
	c.Connection.CommandsPerSecond = 7
	c.Connection.CommandBurst = 9
	s = c.SessionSettings().withDefaults()
	if s.ReadLimitBytes != 1234 || s.CommandsPerSecond != 7 || s.CommandBurst != 9 {
		t.Fatalf("configured limits lost: %+v", s)
	}
}

func TestSayLimiterDefaultsAndPerPlayerBuckets(t *testing.T) {
	now := time.Unix(100, 0)
	settings := SessionSettings{}
	a := newSayLimiter(settings, now)
	for i := 0; i < 5; i++ {
		if allowed, notify := a.allow(now); !allowed || notify {
			t.Fatalf("burst SAY %d: allowed=%v notify=%v", i, allowed, notify)
		}
	}
	if allowed, notify := a.allow(now); allowed || !notify {
		t.Fatalf("excess SAY: allowed=%v notify=%v", allowed, notify)
	}
	if allowed, _ := a.allow(now.Add(999 * time.Millisecond)); allowed {
		t.Fatal("refill below one token permitted a SAY")
	}
	if allowed, _ := a.allow(now.Add(time.Second)); !allowed {
		t.Fatal("one second at 1/s should permit one SAY")
	}
	b := newSayLimiter(settings, now)
	if allowed, _ := b.allow(now); !allowed {
		t.Fatal("one player exhausted another player's bucket")
	}
	custom := newSayLimiter(SessionSettings{SayPerSecond: 2, SayBurst: 1}, now)
	custom.allow(now)
	if allowed, _ := custom.allow(now.Add(500 * time.Millisecond)); !allowed {
		t.Fatal("configured rate of 2/s should refill a token in 500ms")
	}
}
