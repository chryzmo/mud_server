package main

import (
	"strings"
	"testing"

	mudserver "mud_server/src/internal/mudserver"
)

// Global chat (plans/event-systems.md section 3).

func noWait(c *mudserver.Configuration) { c.Chat.GuestMinAgeSeconds = -1 }

// guestIn joins a guest and waits for their first look.
func guestIn(t *testing.T, wsURL, name string) *guest {
	t.Helper()
	return joinGuest(t, wsURL, name)
}

func chatLine(t *testing.T, g *guest) map[string]any {
	t.Helper()
	var got map[string]any
	g.until(func(typ string, m map[string]any) bool {
		if typ == "chat" {
			got = m
		}
		return typ == "chat"
	})
	return got
}

func TestGlobalChatReachesEveryoneInTheWorldIncludingTheSender(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, noWait)
	alice := guestIn(t, wsURL, "Alice")
	bob := guestIn(t, wsURL, "Bob")
	send(t, bob.conn, "MOVE N") // a different room: SAY would not reach, GLOBAL does
	bob.until(func(typ string, m map[string]any) bool { return typ == "room" && m["name"] == "Balcony" })

	send(t, alice.conn, "GLOBAL hello  everyone")
	for _, g := range []*guest{alice, bob} {
		if m := chatLine(t, g); m["who"] != "Alice" || m["text"] != "hello everyone" || m["ch"] != "global" || m["gm"] != nil {
			t.Errorf("%s heard %v", g.name, m)
		}
	}
	send(t, bob.conn, "G and the short form")
	if m := chatLine(t, alice); m["who"] != "Bob" || m["text"] != "and the short form" {
		t.Errorf("short form: %v", m)
	}
	send(t, alice.conn, "GLOBAL")
	alice.until(func(typ string, m map[string]any) bool {
		return typ == "text" && strings.Contains(m["text"].(string), "Usage: GLOBAL")
	})
}

func TestANewGuestWaitsBeforeUsingGlobalChat(t *testing.T) {
	wsURL := startTestServer(t) // the default wait
	alice := guestIn(t, wsURL, "Alice")
	send(t, alice.conn, "GLOBAL hi")
	alice.until(func(typ string, m map[string]any) bool {
		return typ == "text" && strings.Contains(m["text"].(string), "Global chat opens for new visitors")
	})
	// A password account (a regular, or the GM) is not made to wait.
	acct := passwordPlayer(t, wsURL, "regular")
	send(t, acct, "GLOBAL hi")
	if m := expectMessage(t, acct, "chat"); m["who"] != "regular" {
		t.Errorf("chat = %v", m)
	}
}

func TestChatIsRateLimitedPerPlayerAndOverall(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		noWait(c)
		c.Chat.PerPlayerSeconds, c.Chat.PlayerBurst = 600, 2
		c.Chat.OverallPerSecond = 3
	})
	alice := guestIn(t, wsURL, "Alice")
	for _, word := range []string{"one", "two"} {
		send(t, alice.conn, "GLOBAL "+word)
		if m := chatLine(t, alice); m["text"] != word {
			t.Fatalf("heard %v", m)
		}
	}
	send(t, alice.conn, "GLOBAL three")
	if e := expectMessage(t, alice.conn, "error"); e["code"] != "rate_limited" {
		t.Errorf("third message = %v, want rate_limited", e)
	}

	// The overall bucket (3 a second) is shared: a third speaker in the same second is told it is busy.
	bob := guestIn(t, wsURL, "Bob")
	send(t, bob.conn, "GLOBAL from bob")
	chatLine(t, bob) // the third message of the second
	cy := guestIn(t, wsURL, "Cyrus")
	send(t, cy.conn, "GLOBAL from cyrus")
	cy.until(func(typ string, m map[string]any) bool {
		return typ == "text" && strings.Contains(m["text"].(string), "Global chat is busy")
	})
}

func TestBlockedWordsAreMaskedNotRefused(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, noWait)
	alice := guestIn(t, wsURL, "Alice")
	send(t, alice.conn, "GLOBAL what the f.u.c.k, Class! sh1t")
	m := chatLine(t, alice)
	// The words are masked in place; punctuation, spacing and innocent words are untouched.
	if m["text"] != "what the f.u.c.k, Class! ****" {
		t.Errorf("text = %q", m["text"])
	}
	send(t, alice.conn, "GLOBAL FUCK this classic")
	if m := chatLine(t, alice); m["text"] != "**** this classic" {
		t.Errorf("text = %q", m["text"])
	}
}

func TestMutedPlayersAndLongLinesCannotUseGlobalChat(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, noWait)
	gm := gmPlayer(t, wsURL, "boss")
	alice := guestIn(t, wsURL, "Alice")
	expect(t, gm, "Alice")
	send(t, gm, "GM MUTE Alice 30")
	alice.until(func(typ string, m map[string]any) bool { // the mute is in force once she is told of it
		return typ == "text" && strings.Contains(m["text"].(string), "A GM has muted you")
	})
	send(t, alice.conn, "GLOBAL can anyone hear me")
	alice.until(func(typ string, m map[string]any) bool {
		return typ == "text" && strings.Contains(m["text"].(string), "You are muted")
	})
	send(t, gm, "GM UNMUTE Alice")
	alice.until(func(typ string, m map[string]any) bool {
		return typ == "text" && strings.Contains(m["text"].(string), "lifted your mute")
	})
	send(t, alice.conn, "GLOBAL "+strings.Repeat("x", 201))
	alice.until(func(typ string, m map[string]any) bool {
		return typ == "text" && strings.Contains(m["text"].(string), "too long for global chat")
	})
}

func TestTheGMCanSwitchChatOffAndSlowItDown(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, noWait)
	gm := gmPlayer(t, wsURL, "boss")
	alice := guestIn(t, wsURL, "Alice")

	send(t, gm, "GM CHAT OFF")
	expect(t, gm, "Global chat is off.")
	send(t, alice.conn, "GLOBAL anyone")
	alice.until(func(typ string, m map[string]any) bool {
		return typ == "text" && strings.Contains(m["text"].(string), "Global chat is switched off")
	})
	send(t, gm, "GLOBAL the GM may still speak") // to announce what is going on
	m := chatLine(t, alice)
	if m["who"] != "boss" || m["gm"] != true {
		t.Errorf("GM line = %v, want who=boss gm=true", m)
	}
	send(t, gm, "GM CHAT ON")
	expect(t, gm, "Global chat is on.")

	send(t, gm, "GM SLOW 600")
	expect(t, gm, "Slow mode: one global message per player every 600 seconds.")
	send(t, alice.conn, "GLOBAL first")
	chatLine(t, alice)
	send(t, alice.conn, "GLOBAL second")
	alice.until(func(typ string, m map[string]any) bool {
		return typ == "text" && strings.Contains(m["text"].(string), "Slow mode: one message every 600 seconds")
	})
	send(t, gm, "GM SLOW")
	expect(t, gm, "Slow mode is off.")
	send(t, gm, "GM SLOW lots")
	expect(t, gm, "Usage: GM SLOW")
	send(t, gm, "GM CHAT maybe")
	expect(t, gm, "Usage: GM CHAT")
}
