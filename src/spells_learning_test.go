package main

import (
	"strings"
	"testing"
)

// Learning and readying spells through a real server (internal/mudserver/spells.go).

const spellWorld = `
start_room: study
rooms:
  study:
    name: Study
    description: A study.
    features:
      desk: {names: [desk], examine: A desk.}
      plaque: {names: [plaque], examine: A plaque.}
    rules:
      - on: {verb: PULL, target: desk}
        do: [{give_item: bolt-scroll}]
      - on: {verb: READ, target: plaque}
        unless: [{knows_spell: mend}]
        do: [{teach: mend}, {say: "The plaque warms."}]
      - on: {verb: READ, target: plaque}
        if: [{knows_spell: mend}]
        do: [{say: "You know it already."}]
spells:
  bolt:
    name: Firebolt
    effect: bolt
    cost: 3
    cooldown: 5
    power: 4
    description: A bolt of fire.
    cast: You hurl a bolt.
    room: "{player} hurls a bolt."
  mend:
    name: Mend
    effect: mend
    cost: 4
    cooldown: 6
    power: 10
    description: Heals you.
    cast: You mend.
  glow:
    name: Glow
    effect: fun
    cost: 1
    cooldown: 5
    description: A soft glow.
    cast: You glow.
    room: "{player} glows."
  ward:
    name: Ward
    effect: ward
    cost: 5
    cooldown: 20
    power: 3
    description: Shields you.
    cast: You ward yourself.
items:
  bolt-scroll:
    name: FIREBOLT SCROLL
    description: A scroll.
    bound: true
    teaches: bolt
`

func TestSpellsAreLearnedFromScrollsAndRulesReadiedAndRemembered(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, withContent(contentDir(t, map[string]string{"world.yaml": spellWorld})))
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")
	loginNew(t, conn, "scholar")
	expect(t, conn, "You are in 'Study'")

	send(t, conn, "SPELLS")
	expect(t, conn, "You have not learned any spells yet.")

	send(t, conn, "PULL desk")
	expect(t, conn, "You receive")
	send(t, conn, "READ firebolt scroll")
	expect(t, conn, "You learn")
	send(t, conn, "INVENTORY")
	if inv := expect(t, conn, "inventory"); strings.Contains(inv, "SCROLL") {
		t.Errorf("the scroll was not used up: %q", inv)
	}
	send(t, conn, "SPELLS")
	list := expect(t, conn, "Your spells:")
	if !strings.Contains(list, "[1] Firebolt") || !strings.Contains(list, "3 MP") {
		t.Errorf("SPELLS = %q", list)
	}

	// A quest rule teaches too, once; knows_spell reads it.
	send(t, conn, "READ plaque")
	expect(t, conn, "You learn")
	send(t, conn, "READ plaque")
	expect(t, conn, "You know it already.")

	// Slots: Mend took slot 2; READY moves; UNREADY frees.
	send(t, conn, "READY mend 3")
	expect(t, conn, "Mend is ready in slot 3.")
	send(t, conn, "READY mend 5")
	expect(t, conn, "There are 3 slots")
	send(t, conn, "READY nothing")
	expect(t, conn, "You do not know a spell called that.")
	send(t, conn, "UNREADY 1")
	expect(t, conn, "Firebolt is no longer ready.")
	send(t, conn, "UNREADY 1")
	expect(t, conn, "Nothing is readied there.")
	send(t, conn, "READY fire")
	expect(t, conn, "Firebolt is ready in slot 1.")

	// What they know and have readied survives logging out and back in.
	waitForStateRows(t, "scholar", 4) // spell bolt, spell mend, slot 1, slot 3 (plus any others)
	conn.Close()
	again, msg := loginWhenFree(t, wsURL, "scholar", "secret")
	if !strings.Contains(msg, "Welcome back") {
		t.Fatalf("login = %q", msg)
	}
	send(t, again, "SPELLS")
	list = expect(t, again, "Your spells:")
	if !strings.Contains(list, "[1] Firebolt") || !strings.Contains(list, "[3] Mend") {
		t.Errorf("after login SPELLS = %q", list)
	}
}
