package mudserver

import (
	"strings"
	"testing"
	"time"
)

func limiterAt(perSecond, burst int) (*joinLimiter, *time.Time) {
	now := time.Unix(5000, 0)
	l := newJoinLimiter(perSecond, burst)
	l.now = func() time.Time { return now }
	l.last = now
	l.jitter = func() float64 { return 0 }
	return l, &now
}

func TestJoinLimiterDefaultsAndDisabling(t *testing.T) {
	l := newJoinLimiter(0, 0)
	if l.rate != defaultJoinPerSecond || l.capacity != defaultJoinBurst {
		t.Errorf("defaults = %v/s burst %v, want %d/s burst %d", l.rate, l.capacity, defaultJoinPerSecond, defaultJoinBurst)
	}
	if newJoinLimiter(-1, 0) != nil {
		t.Error("a negative rate must mean no limit")
	}
	var none *joinLimiter
	for i := 0; i < 100000; i++ {
		if ok, _ := none.allow(); !ok {
			t.Fatal("a nil limiter refused")
		}
	}
}

func TestJoinLimiterAllowsABurstThenRefillsAtTheRate(t *testing.T) {
	l, now := limiterAt(10, 5)
	for i := 0; i < 5; i++ {
		if ok, _ := l.allow(); !ok {
			t.Fatalf("burst request %d refused", i+1)
		}
	}
	ok, retry := l.allow()
	if ok || retry != 100*time.Millisecond {
		t.Fatalf("beyond the burst: ok=%v retry=%v, want a refusal with a 100 ms hint (one token at 10/s)", ok, retry)
	}
	*now = now.Add(100 * time.Millisecond)
	if ok, _ := l.allow(); !ok {
		t.Error("a token should have refilled after 100 ms")
	}
	if ok, _ := l.allow(); ok {
		t.Error("only one token refilled")
	}
	// A long quiet spell refills to the burst and no further.
	*now = now.Add(time.Hour)
	for i := 0; i < 5; i++ {
		if ok, _ := l.allow(); !ok {
			t.Fatalf("after a long quiet spell, request %d refused", i+1)
		}
	}
	if ok, _ := l.allow(); ok {
		t.Error("idle time must not bank more than the burst")
	}
}

func TestJoinLimiterRetryHintsSpreadAndNeverUnderstate(t *testing.T) {
	l, _ := limiterAt(1000, 1)
	l.allow()
	for _, jitter := range []float64{0, 0.25, 0.999} {
		l.jitter = func() float64 { return jitter }
		ok, retry := l.allow()
		wait := time.Millisecond // one token at 1000/s
		if ok || retry < wait || retry > wait+maxRetryJitter {
			t.Errorf("jitter %v: ok=%v retry=%v, want within [%v, %v]", jitter, ok, retry, wait, wait+maxRetryJitter)
		}
	}
	// Refusals never spend tokens, so a crowd of refused callers cannot starve the bucket.
	l.jitter = func() float64 { return 0 }
	for i := 0; i < 1000; i++ {
		l.allow()
	}
	if l.tokens < 0 {
		t.Errorf("tokens = %v; refusals must not go negative", l.tokens)
	}
}

func TestAllowJoinRepliesWithARetryHint(t *testing.T) {
	ms := &MudServer{joinLimit: newJoinLimiter(1, 1)}
	session := &Session{out: make(chan []byte, 4), closed: make(chan struct{})}
	if !ms.allowJoin(session) {
		t.Fatal("the first request was refused")
	}
	if len(session.out) != 0 {
		t.Error("an allowed request must not send anything")
	}
	if ms.allowJoin(session) {
		t.Fatal("the second request should be refused")
	}
	frame := string(<-session.out)
	for _, want := range []string{`"t":"error"`, `"code":"rate_limited"`, `"retry_ms":`, "Please wait"} {
		if !strings.Contains(frame, want) {
			t.Errorf("refusal %s lacks %s", frame, want)
		}
	}
	var unlimited MudServer
	if !unlimited.allowJoin(session) {
		t.Error("a server with no limiter must allow")
	}
}
