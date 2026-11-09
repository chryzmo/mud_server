package main

import (
	"testing"
)

// A quest reward that unlocks a name colour or badge (the unlock rule effect), through a real server.

const unlockWorld = `
start_room: hall
rooms:
  hall:
    name: Hall
    description: A hall.
    features:
      plaque: {names: [plaque], examine: A plaque.}
      ribbon: {names: [ribbon], examine: A ribbon.}
    rules:
      - on: {verb: READ, target: plaque}
        do:
          - unlock: {badge: crown}
          - say: "The plaque is warm."
      - on: {verb: READ, target: ribbon}
        do:
          - unlock: {color: gold}
`

func unlockServer(t *testing.T) string {
	t.Helper()
	wsURL, _ := startTestServerWithConfig(t, withContent(contentDir(t, map[string]string{"world.yaml": unlockWorld})))
	return wsURL
}

func TestAQuestRuleUnlocksACosmeticOnceAndOthersSeeIt(t *testing.T) {
	t.Parallel()
	wsURL := unlockServer(t)
	alice := eventPlayer(t, wsURL, "alice")
	bob := eventPlayer(t, wsURL, "bob")

	send(t, alice, "READ plaque")
	expect(t, alice, "You earned the crown badge. The Style button changes it.")
	if c := expectMessage(t, alice, "cosmetics"); c["badge"] != "crown" {
		t.Errorf("cosmetics = %v", c)
	}
	send(t, alice, "SAY hello")
	if say := expectMessage(t, bob, "say"); say["who"] != "alice" || say["badge"] != "crown" {
		t.Errorf("say = %v", say)
	}

	// Reading it again changes nothing, and does not put back a badge she has taken off.
	send(t, alice, "COSMETIC off")
	expect(t, alice, "Cosmetics cleared.")
	send(t, alice, "READ plaque")
	expect(t, alice, "The plaque is warm.")
	send(t, alice, "SAY again")
	if say := expectMessage(t, bob, "say"); say["text"] == "again" && say["badge"] != nil {
		t.Errorf("the badge came back after a second read: %v", say)
	}

	send(t, alice, "READ ribbon")
	expect(t, alice, "Your name now shows in gold. The Style button changes it.") // the client's button is Style (COSMETIC is the typed command)
	if c := expectMessage(t, alice, "cosmetics"); c["color"] != "gold" {
		t.Errorf("cosmetics = %v", c)
	}
}

func TestAnUnlockedCosmeticSurvivesLoggingBackIn(t *testing.T) {
	t.Parallel()
	wsURL := unlockServer(t)
	conn := eventPlayer(t, wsURL, "keeper")
	send(t, conn, "READ plaque")
	expect(t, conn, "You earned the crown badge.")
	waitForStateRows(t, "keeper", 3) // the unlock, the choice, and the visited room
	conn.Close()

	again, _ := loginWhenFree(t, wsURL, "keeper", "secret")
	if c := expectMessage(t, again, "cosmetics"); c["badge"] != "crown" {
		t.Errorf("cosmetics after login = %v", c)
	}
}

func TestStickerCodesAreGone(t *testing.T) {
	t.Parallel()
	wsURL := unlockServer(t)
	conn := eventPlayer(t, wsURL, "hopeful")
	for _, line := range []string{"REDEEM ABCD-2345", "PROGRESS"} {
		send(t, conn, line)
		expect(t, conn, "Unknown command.")
	}
	send(t, conn, "COSMETIC")
	expect(t, conn, "Quests and bosses give them.")
}
