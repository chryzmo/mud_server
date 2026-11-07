package mudserver

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"mud_server/src/internal/muddata"
)

/*
Global chat (plans/event-systems.md section 3): one channel everybody hears. It is the most
expensive thing a player can do (one frame to every player), so it is limited three ways: per
player (a message every few seconds with a small burst), overall across the server, and a wait
before a new guest may speak. Blocked words are masked rather than refused, muted players are
silent, and the GM has a slow mode and an off switch. Everything runs on the game loop.
*/

const (
	defaultChatSeconds = 5
	defaultChatBurst   = 3
	defaultChatOverall = 30
	defaultChatMinAge  = 300 * time.Second
	defaultChatLength  = 200
	channelGlobal      = "global"
)

// chatState is the server-wide side of global chat. Loop only.
type chatState struct {
	off     bool           // the GM switched global chat off
	slow    time.Duration  // the GM's slow mode: the least gap between one player's messages
	overall commandLimiter // every message from every player spends from this
}

func (ms *MudServer) chatSettings() (perPlayer commandLimiter, minAge time.Duration, maxLen int, overall int) {
	c := ms.Configuration.Chat
	seconds, burst, overall, maxLen := c.PerPlayerSeconds, c.PlayerBurst, c.OverallPerSecond, c.MaxLength
	if seconds <= 0 {
		seconds = defaultChatSeconds
	}
	if burst <= 0 {
		burst = defaultChatBurst
	}
	if overall <= 0 {
		overall = defaultChatOverall
	}
	if maxLen <= 0 {
		maxLen = defaultChatLength
	}
	minAge = defaultChatMinAge
	if c.GuestMinAgeSeconds != 0 {
		minAge = time.Duration(max(c.GuestMinAgeSeconds, 0)) * time.Second
	}
	perPlayer = commandLimiter{rate: 1 / float64(seconds), capacity: float64(burst), tokens: float64(burst)}
	return perPlayer, minAge, maxLen, overall
}

// mask replaces each word that contains a blocked word with asterisks. A word is a run of letters
// and digits, read as in name checks (look-alike digits count as letters, "_" and "-" are ignored).
func (r *nameRules) mask(text string) string {
	var out strings.Builder
	start := -1
	flush := func(end int) {
		word := text[start:end]
		norm := normalise(word)
		hit := false
		for _, p := range r.contains {
			if p.MatchString(norm) {
				hit = true
			}
		}
		for _, p := range r.parts {
			if p.MatchString(norm) {
				hit = true
			}
		}
		if hit {
			out.WriteString(strings.Repeat("*", utf8.RuneCountInString(word)))
		} else {
			out.WriteString(word)
		}
		start = -1
	}
	isWordRune := func(c rune) bool { return unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' || c == '-' }
	for i, c := range text {
		if isWordRune(c) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			flush(i)
		}
		out.WriteRune(c)
	}
	if start >= 0 {
		flush(len(text))
	}
	return out.String()
}

// globalChat is GLOBAL <text>. Loop only.
func (ms *MudServer) globalChat(p *MudPlayer, text string, now time.Time) {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		p.SendMessage("Usage: GLOBAL <message>  (everyone in the world hears it; SAY is for the room)")
		return
	}
	perPlayer, minAge, maxLen, overall := ms.chatSettings()
	if ms.chat.off && !p.gm {
		p.SendMessage("Global chat is switched off for now. SAY still works.")
		return
	}
	if p.refuseIfMuted(now) {
		return
	}
	if utf8.RuneCountInString(text) > maxLen {
		p.SendMessage(fmt.Sprintf("That is too long for global chat (at most %d characters).", maxLen))
		return
	}
	if !p.gm {
		if p.guest && now.Sub(p.TimeConnected) < minAge {
			wait := minAge - now.Sub(p.TimeConnected)
			p.SendMessage(fmt.Sprintf("Global chat opens for new visitors after a few minutes of play (about %s more). SAY works in the meantime.", roundUp(wait)))
			return
		}
		if ms.chat.slow > 0 && now.Sub(p.lastChat) < ms.chat.slow {
			p.SendMessage(fmt.Sprintf("Slow mode: one message every %d seconds.", int(ms.chat.slow/time.Second)))
			return
		}
		if p.chatLimiter.capacity == 0 {
			p.chatLimiter = perPlayer
			p.chatLimiter.last = now
		}
		if ok, notify := p.chatLimiter.allow(now); !ok {
			if notify {
				p.SendError(codeRateLimited, "You are chatting too fast. Give it a few seconds.")
			}
			return
		}
	}
	if ms.chat.overall.capacity == 0 {
		ms.chat.overall = newLimiter(overall, overall, now)
	}
	if ok, notify := ms.chat.overall.allow(now); !ok {
		if notify {
			p.SendMessage("Global chat is busy. Try again in a moment.")
		}
		return
	}
	p.lastChat = now
	masked := ms.names.mask(text)
	ms.noteLine("global", p.Username, masked)
	color, badge := p.look()
	frame := encode(chatMsg{T: msgChat, Who: p.Username, Text: masked, Ch: channelGlobal, GM: p.gm, Color: color, Badge: badge})
	for _, target := range ms.players {
		target.sendFrame(frame)
	}
}

func registerChat(ms *MudServer, cList map[string]*MudCommand) {
	run := func(player *MudPlayer, arguments string) int {
		ms.globalChat(player, arguments, time.Now())
		return muddata.MUD_OK
	}
	cList["GLOBAL"] = &MudCommand{description: "Say something to everyone in the world.  Usage: GLOBAL <message>", action: run}
	cList["G"] = &MudCommand{description: "Short for GLOBAL.", action: run}
}
