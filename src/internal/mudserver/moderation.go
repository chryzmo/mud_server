package mudserver

import (
	"strings"
	"time"
	"unicode/utf8"
)

/*
Automatic moderation and quiet hours (plans/event-systems.md tracks E2 and E3). The owner is the only
GM and sleeps, so the server handles what is cheap and repetitive: a player who keeps sending the
same message, or keeps hammering the rate limit, is muted for a short while, longer each time, with a
clear message to them and a note to any GM, all logged. Never a kick or a ban; a GM can lift it.
At night (01:00 to 08:00 Eastern) global chat is slower and new visitors wait longer, unless a GM
says otherwise with GM CHAT ON or GM SLOW.
*/

const (
	repeatLimit     = 4 // the same message this many times within repeatWindow is spam
	repeatWindow    = time.Minute
	repeatMinLength = 3 // shorter lines ("ok") are not counted
	floodLimit      = 8 // refusals by the rate limit within floodWindow is flooding
	floodWindow     = 30 * time.Second
	strikeForget    = time.Hour // a quiet hour wipes a player's record
	quietFrom       = 1         // hours, Eastern
	quietTo         = 8
	quietSlow       = 30 * time.Second
	quietGuestWait  = 5 * time.Minute
)

// strikeMutes is how long each successive automatic mute lasts (the last repeats).
var strikeMutes = []time.Duration{2 * time.Minute, 10 * time.Minute, 30 * time.Minute}

type sentLine struct {
	text string
	at   time.Time
}

// modState is one player's record. Loop only.
type modState struct {
	sent       []sentLine
	refused    []time.Time
	strikes    int
	lastStrike time.Time
}

// normalizeLine reads a message the way a spammer varies it away: case, spacing and trailing punctuation do not matter.
func normalizeLine(s string) string {
	return strings.TrimRight(strings.ToLower(strings.Join(strings.Fields(s), " ")), "!?.,;:~ ")
}

// strike mutes a player for the next step of the ladder. Loop only.
func (ms *MudServer) strike(p *MudPlayer, why string, now time.Time) {
	if p.gm {
		return
	}
	d := p.mod.nextMute(now)
	ms.autoMute(p, d, why, now)
}

// nextMute takes a strike and returns how long that one mutes: 2, 10, then 30 minutes, and a quiet
// hour wipes the record.
func (m *modState) nextMute(now time.Time) time.Duration {
	if now.Sub(m.lastStrike) > strikeForget {
		m.strikes = 0
	}
	d := strikeMutes[min(m.strikes, len(strikeMutes)-1)]
	m.strikes++
	m.lastStrike = now
	m.sent, m.refused = nil, nil
	return d
}

// checkRepeat is called with each message a player is about to send (SAY or GLOBAL). It returns true if
// the message must be dropped because it just earned a mute. Loop only.
func (ms *MudServer) checkRepeat(p *MudPlayer, text string, now time.Time) bool {
	if p.gm {
		return false
	}
	norm := normalizeLine(text)
	if utf8.RuneCountInString(norm) < repeatMinLength {
		return false
	}
	m := &p.mod
	kept := m.sent[:0]
	same := 1
	for _, l := range m.sent {
		if now.Sub(l.at) > repeatWindow {
			continue
		}
		kept = append(kept, l)
		if l.text == norm {
			same++
		}
	}
	m.sent = append(kept, sentLine{text: norm, at: now})
	if same >= repeatLimit {
		ms.strike(p, "repeating the same message", now)
		return true
	}
	return false
}

// noteRefused is called each time the rate limit refuses a player's SAY or GLOBAL. Loop only.
func (ms *MudServer) noteRefused(p *MudPlayer, now time.Time) {
	if p.gm {
		return
	}
	m := &p.mod
	kept := m.refused[:0]
	for _, t := range m.refused {
		if now.Sub(t) <= floodWindow {
			kept = append(kept, t)
		}
	}
	m.refused = append(kept, now)
	if len(m.refused) >= floodLimit {
		ms.strike(p, "flooding the chat", now)
	}
}

var easternZone = func() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.FixedZone("EST", -5*3600)
	}
	return loc
}()

// quietHours reports whether it is night at the con (01:00 to 08:00 Eastern).
func quietHours(now time.Time) bool {
	h := now.In(easternZone).Hour()
	return h >= quietFrom && h < quietTo
}

// quietEnd is the next 08:00 Eastern after now.
func quietEnd(now time.Time) time.Time {
	local := now.In(easternZone)
	end := time.Date(local.Year(), local.Month(), local.Day(), quietTo, 0, 0, 0, easternZone)
	if !end.After(now) {
		end = end.AddDate(0, 0, 1)
	}
	return end
}

// gmOverridesQuiet is called when a GM sets chat or slow mode by hand: for the rest of this night the
// quiet-hours defaults step aside.
func (ms *MudServer) gmOverridesQuiet(now time.Time) { ms.chat.quietOverrideUntil = quietEnd(now) }

// quietActive reports whether the night defaults apply right now.
func (ms *MudServer) quietActive(now time.Time) bool {
	return quietHours(now) && !now.Before(ms.chat.quietOverrideUntil)
}

// effectiveSlow is the least gap between one player's global messages: the GM's slow mode, or at night at least the default.
func (ms *MudServer) effectiveSlow(now time.Time) time.Duration {
	if ms.quietActive(now) {
		return max(ms.chat.slow, quietSlow)
	}
	return ms.chat.slow
}

// guestWait is how long a new guest must have played before using global chat.
func (ms *MudServer) guestWait(base time.Duration, now time.Time) time.Duration {
	if ms.quietActive(now) {
		return max(base, quietGuestWait)
	}
	return base
}
