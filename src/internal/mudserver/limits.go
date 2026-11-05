package mudserver

import "time"

// commandLimiter is a reader-owned token bucket. Passing time explicitly keeps
// burst, refill and notice behavior testable without sleeps or a ticker.
type commandLimiter struct {
	rate       float64
	capacity   float64
	tokens     float64
	last       time.Time
	nextNotice time.Time
}

func newCommandLimiter(settings SessionSettings, now time.Time) commandLimiter {
	settings = settings.withDefaults()
	return newLimiter(settings.CommandsPerSecond, settings.CommandBurst, now)
}

// newSayLimiter limits one player's SAY, which costs a send to every occupant
// of the room, far more than a command that only answers its sender.
func newSayLimiter(settings SessionSettings, now time.Time) commandLimiter {
	settings = settings.withDefaults()
	return newLimiter(settings.SayPerSecond, settings.SayBurst, now)
}

func newLimiter(perSecond, burst int, now time.Time) commandLimiter {
	return commandLimiter{
		rate:     float64(perSecond),
		capacity: float64(burst),
		tokens:   float64(burst),
		last:     now,
	}
}

func (l *commandLimiter) allow(now time.Time) (allowed, notify bool) {
	if now.After(l.last) {
		l.tokens = min(l.capacity, l.tokens+now.Sub(l.last).Seconds()*l.rate)
		l.last = now
	}
	if l.tokens >= 1 {
		l.tokens--
		return true, false
	}
	if !now.Before(l.nextNotice) {
		l.nextNotice = now.Add(time.Second)
		return false, true
	}
	return false, false
}
