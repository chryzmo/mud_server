package main

import (
	"fmt"
	"strings"
	"testing"

	mudserver "mud_server/src/internal/mudserver"
)

// Automatic moderation (plans/event-systems.md track E2) through a real server.

func TestRepeatingTheSameMessageEarnsAnEscalatingMuteAndNeverAKick(t *testing.T) {
	t.Parallel()
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		withContent(progressWorld(t))(c)
		noWait(c)
		c.Chat.PerPlayerSeconds, c.Chat.PlayerBurst = 1, 100 // the rate limit is not what is under test
	})
	gm := eventGM(t, wsURL, "boss")
	spam := eventPlayer(t, wsURL, "spammer")
	watcher := eventPlayer(t, wsURL, "watcher")

	for i := 0; i < 3; i++ { // three is allowed, however it is written
		send(t, spam, fmt.Sprintf("GLOBAL Buy gold now%s", []string{"!", "!!", " ?"}[i]))
		expect(t, watcher, "Buy gold now")
	}
	send(t, spam, "GLOBAL BUY GOLD NOW")
	expect(t, spam, "You have been muted for 2 minutes (repeating the same message).")
	expect(t, gm, "[Auto] spammer muted for 2 minutes: repeating the same message.")
	send(t, spam, "GLOBAL anyone there")
	expect(t, spam, "You are muted for another")
	send(t, spam, "MOVE N") // never a kick: they can still play
	expect(t, spam, "You are in 'Vault'")

	send(t, gm, "GM UNMUTE spammer")
	expect(t, gm, "spammer can speak again.")
	for i := 0; i < 4; i++ {
		send(t, spam, "SAY same old line")
	}
	expect(t, spam, "You have been muted for 10 minutes (repeating the same message).")
	// Short lines and different lines are not spam.
	other := eventPlayer(t, wsURL, "chatty")
	for _, line := range []string{"ok", "ok", "ok", "ok", "ok"} {
		send(t, other, "SAY "+line)
		expect(t, other, "You say:")
	}
}

func TestHammeringTheRateLimitEarnsAMute(t *testing.T) {
	t.Parallel()
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		withContent(progressWorld(t))(c)
		noWait(c)
		c.Chat.PerPlayerSeconds, c.Chat.PlayerBurst = 600, 1
	})
	flood := eventPlayer(t, wsURL, "flooder")
	for i := 0; i < 12; i++ {
		send(t, flood, fmt.Sprintf("GLOBAL line number %d", i))
	}
	expect(t, flood, "You have been muted for 2 minutes (flooding the chat).")
}

func TestANewGuestWaitsAboutAMinuteByDefault(t *testing.T) {
	t.Parallel()
	wsURL := startTestServer(t)
	g := guestIn(t, wsURL, "Newcomer")
	send(t, g.conn, "GLOBAL hi")
	g.until(func(typ string, m map[string]any) bool {
		text, _ := m["text"].(string)
		return typ == "text" && text != "" && strings.Contains(text, "about a minute more")
	})
}
