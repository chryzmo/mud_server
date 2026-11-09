package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	mudserver "mud_server/src/internal/mudserver"
)

// Socials, EMOTE, WHO and IGNORE (plans/event-systems.md B1, B2, B4) through a real server.

func socialServer(t *testing.T) string {
	t.Helper()
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		withContent(progressWorld(t))(c)
		noWait(c)
		c.Connection.SayPerSecond, c.Connection.SayBurst = 1000, 1000
		c.Connection.CommandsPerSecond, c.Connection.CommandBurst = 1000, 1000
		c.Chat.PerPlayerSeconds, c.Chat.PlayerBurst = 1, 1000
	})
	return wsURL
}

// readUntil reads frames until one contains want, and returns every text it saw before that.
func readUntil(t *testing.T, conn *websocket.Conn, want string) []string {
	t.Helper()
	var seen []string
	for i := 0; i < 200; i++ {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for %q: %v\nseen: %v", want, err, seen)
		}
		text := flatten(msg)
		if hasText(text, want) {
			return seen
		}
		seen = append(seen, text)
	}
	t.Fatalf("never saw %q", want)
	return nil
}

func neverSaw(t *testing.T, seen []string, unwanted string) {
	t.Helper()
	for _, s := range seen {
		if strings.Contains(s, unwanted) {
			t.Errorf("saw %q, which should have been hidden: %q", unwanted, s)
		}
	}
}

func TestSocialsAloneAndAtSomeoneInTheRoom(t *testing.T) {
	wsURL := socialServer(t)
	ann, bob, cy := eventPlayer(t, wsURL, "ann"), eventPlayer(t, wsURL, "bob"), eventPlayer(t, wsURL, "cy")

	send(t, ann, "WAVE")
	expect(t, ann, "You wave.")
	expect(t, bob, "ann\x1b[0m waves.")
	expect(t, cy, "ann\x1b[0m waves.")

	send(t, ann, "hug BOB")
	expect(t, ann, "You hug bob.")
	expect(t, bob, "ann\x1b[0m hugs you.")
	expect(t, cy, "ann\x1b[0m hugs bob.")

	send(t, bob, "DANCE an") // a unique prefix of three letters would do; two does not
	expect(t, bob, "You do not see 'an' here.")
	send(t, bob, "DANCE ann")
	expect(t, bob, "You dance with ann.")
	send(t, bob, "BOW bob") // at yourself is just doing it
	expect(t, bob, "You bow.")
	send(t, bob, "HIGHFIVE")
	expect(t, bob, "You high-five the air.")
	send(t, bob, "NOD zed")
	expect(t, bob, "You do not see 'zed' here.")
}

func TestEveryAnnouncedSocialIsARealCommand(t *testing.T) {
	wsURL := socialServer(t)
	ann := eventPlayer(t, wsURL, "ann")
	for _, verb := range mudserver.SocialNames() {
		send(t, ann, verb)
		expect(t, ann, "You ")
	}
	if got := len(mudserver.SocialNames()); got != 12 {
		t.Errorf("%d socials", got)
	}
}

func TestEmoteIsSharedMaskedAndRefusedWhenMuted(t *testing.T) {
	wsURL := socialServer(t)
	gm := eventGM(t, wsURL, "boss")
	ann, bob := eventPlayer(t, wsURL, "ann"), eventPlayer(t, wsURL, "bob")
	send(t, ann, "EMOTE juggles three lemons")
	expect(t, ann, "juggles three lemons")
	expect(t, bob, "juggles three lemons")
	send(t, ann, "EMOTE")
	expect(t, ann, "Usage: EMOTE")
	send(t, ann, "EMOTE "+strings.Repeat("x", 101))
	expect(t, ann, "too long for an emote")

	send(t, gm, "GM MUTE ann 5")
	expect(t, gm, "ann is muted")
	send(t, ann, "EMOTE sneaks a word in")
	expect(t, ann, "You are muted for another")
	send(t, ann, "WAVE")
	expect(t, ann, "You are muted for another")
}

func TestIgnoreHidesSayWhispersEmotesSocialsAndGlobalChat(t *testing.T) {
	wsURL := socialServer(t)
	ann, bob, cy := eventPlayer(t, wsURL, "ann"), eventPlayer(t, wsURL, "bob"), eventPlayer(t, wsURL, "cy")

	send(t, bob, "IGNORE")
	expect(t, bob, "You are not ignoring anyone.")
	send(t, bob, "IGNORE ANN")
	expect(t, bob, "ann is ignored")
	send(t, bob, "IGNORE")
	expect(t, bob, "You are ignoring: ann.")

	send(t, ann, "SAY secret say")
	expect(t, cy, "secret say") // others still hear it
	send(t, ann, "EMOTE does a secret emote")
	expect(t, cy, "secret emote")
	send(t, ann, "WAVE bob")
	expect(t, ann, "You wave at bob.") // the ignored player is not told
	send(t, ann, "GLOBAL secret global")
	expect(t, cy, "secret global")
	send(t, ann, "WHISPER bob secret whisper")
	expect(t, ann, "You whisper to bob")

	send(t, bob, "UNIGNORE ann")
	expect(t, bob, "ann is no longer ignored.")
	send(t, ann, "SAY visible say")
	seen := readUntil(t, bob, "visible say")
	for _, hidden := range []string{"secret say", "secret emote", "waves at you", "secret global", "secret whisper"} {
		neverSaw(t, seen, hidden)
	}
	send(t, ann, "WHISPER bob visible whisper")
	readUntil(t, bob, "visible whisper")
	send(t, bob, "UNIGNORE ann")
	expect(t, bob, "You are not ignoring 'ann'.")
}

func TestIgnoreRefusesGMsYourselfAndTooMany(t *testing.T) {
	wsURL := socialServer(t)
	eventGM(t, wsURL, "boss")
	bob := eventPlayer(t, wsURL, "bob")
	send(t, bob, "IGNORE boss")
	expect(t, bob, "You cannot ignore a GM.")
	send(t, bob, "IGNORE bob")
	expect(t, bob, "You cannot ignore yourself.")
	for i := 0; i < 20; i++ {
		send(t, bob, fmt.Sprintf("IGNORE troll%d", i))
		expect(t, bob, "is ignored")
	}
	send(t, bob, "IGNORE troll20")
	expect(t, bob, "You can ignore at most 20 players.")
	send(t, bob, "IGNORE troll3") // one already on the list is fine
	expect(t, bob, "troll3 is ignored")
	send(t, bob, "UNIGNORE ALL")
	expect(t, bob, "You are not ignoring anyone now.")
}

func TestWhoGivesCountsAndTheNamesInYourRoomOnly(t *testing.T) {
	wsURL := socialServer(t)
	ann := eventPlayer(t, wsURL, "ann")
	send(t, ann, "WHO")
	expect(t, ann, "You are the only one online.")

	bob, cy := eventPlayer(t, wsURL, "bob"), eventPlayer(t, wsURL, "cy")
	send(t, cy, "MOVE N")
	expect(t, cy, "You are in 'Vault'")
	// WHO is cached for a few seconds; a fresh server state shows up within that.
	for i := 0; ; i++ {
		send(t, bob, "WHO")
		out := expect(t, bob, "Here with you:")
		if strings.Contains(out, "3 players are online.") {
			if !strings.Contains(out, "Here with you: ann.") || strings.Contains(out, "cy") {
				t.Errorf("who = %q", out)
			}
			break
		}
		if i > 80 {
			t.Fatalf("never saw three players: %q", out)
		}
		timeSleep()
	}
}

func timeSleep() { time.Sleep(100 * time.Millisecond) }
