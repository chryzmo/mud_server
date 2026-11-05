package mudserver

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sync"
	"time"
)

/*
The join limit (plans/guest-sessions.md): one token bucket shared by every
connection, spent by join, resume, recover, login and register before any
database work. It is abuse protection, not a fairness rule: it does not look at
who is asking (venue WiFi and carrier NAT put many players behind one address),
and its burst is sized so a whole event's reconnect storm passes at once.

Defaults come from measurement (plans/capacity-fixes-support/load-results-storms):
5,000 guests joined in 2.6 s (about 1,900 a second) and 5,000 resumes finished
in 0.3 to 2 s, so 1,000 a second sustained with a burst of 5,000 lets a real
storm through untouched and caps a flood at what the database handles.
*/

const (
	defaultJoinPerSecond = 1000
	defaultJoinBurst     = 5000
)

// joinLimiter is a token bucket with a retry hint for refused callers. A nil
// limiter allows everything.
type joinLimiter struct {
	mu       sync.Mutex
	rate     float64 // tokens per second
	capacity float64
	tokens   float64
	last     time.Time
	now      func() time.Time
	jitter   func() float64 // uniform [0,1), spreads the retries of a refused crowd

	refused     int64 // since the last log line
	lastLogTime time.Time
}

// newJoinLimiter returns nil (no limit) if perSecond is negative; zero values
// take the defaults.
func newJoinLimiter(perSecond, burst int) *joinLimiter {
	if perSecond < 0 {
		return nil
	}
	if perSecond == 0 {
		perSecond = defaultJoinPerSecond
	}
	if burst <= 0 {
		burst = defaultJoinBurst
	}
	l := &joinLimiter{rate: float64(perSecond), capacity: float64(burst), tokens: float64(burst), now: time.Now, jitter: rand.Float64}
	l.last = l.now()
	return l
}

// maxRetryJitter spreads out the retries of callers refused at the same moment,
// so they do not all come back in the same instant.
const maxRetryJitter = 500 * time.Millisecond

// allow spends a token, or says how long to wait before trying again.
func (l *joinLimiter) allow() (ok bool, retryAfter time.Duration) {
	if l == nil {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.After(l.last) {
		l.tokens = math.Min(l.capacity, l.tokens+now.Sub(l.last).Seconds()*l.rate)
		l.last = now
	}
	if l.tokens >= 1 {
		l.tokens--
		return true, 0
	}
	wait := time.Duration((1 - l.tokens) / l.rate * float64(time.Second))
	retryAfter = wait + time.Duration(l.jitter()*float64(maxRetryJitter))
	if retryAfter < time.Millisecond {
		retryAfter = time.Millisecond
	}
	l.refused++
	if now.Sub(l.lastLogTime) > 10*time.Second {
		fmt.Printf("Join limit: refused %d since the last report (limit %.0f/s, burst %.0f)\n", l.refused, l.rate, l.capacity)
		l.refused, l.lastLogTime = 0, now
	}
	return false, retryAfter
}

// allowJoin is called by a connection's reader before it does any work for a
// join, resume, recover, login or register. If the limit is spent it tells the
// client when to come back and returns false.
func (ms *MudServer) allowJoin(session *Session) bool {
	ok, retry := ms.joinLimit.allow()
	if !ok {
		session.Send(encode(errorMsg{T: msgError, Code: codeRateLimited, RetryMS: int(retry.Milliseconds()) + 1,
			Message: "Too many people are joining right now. Please wait a moment."}))
	}
	return ok
}
