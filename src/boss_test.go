package main

import (
	"testing"
	"time"
)

// Bosses: an event step and GM SPAWN place a named monster, the world hears of it, one at a time, and its
// guaranteed drop pays (internal/mudserver/mobs.go).

const bossWorld = `
start_room: hall
rooms:
  hall:
    name: Hall
    description: A hall.
items:
  trophy:
    name: GRAK TROPHY
    description: A grim trophy.
npcs:
  warden:
    race: WARDEN
    name: Old Grak
    boss: true
    hp: 10
    mp: 1
    attack: 1
    accuracy: 100
    defense: 0
    combat_tick_ms: 500
    behavior: orc
    xp: 9
    drops:
      - {badge: flame, chance: 100}
      - {item: trophy, chance: 100}
events:
  duel:
    title: The Duel
    at: "2099-01-07 12:00 America/New_York"
    duration: 10m
    steps:
      - {spawn: {npc: warden, room: hall}}
`

func TestABossArrivesByEventIsAnnouncedOneAtATimeAndPaysItsGuaranteedDrop(t *testing.T) {
	t.Parallel()
	wsURL, _ := startTestServerWithConfig(t, withContent(contentDir(t, map[string]string{"world.yaml": bossWorld})))
	gm := eventGM(t, wsURL, "boss")
	fighter := eventPlayer(t, wsURL, "visitor")

	send(t, gm, "GM EVENT START duel")
	if a := expectMessage(t, fighter, "announce"); a["text"] != "Old Grak has appeared in Hall!" {
		t.Errorf("announce = %v", a)
	}
	send(t, fighter, "LOOK") // a fresh room frame: the one sent at the spawn came before the announcement
	name := monsterTap(t, fighter)
	if name != "Old Grak" {
		t.Fatalf("the boss is called %q", name)
	}

	// One at a time, and an unknown monster is refused.
	send(t, gm, "GM SPAWN warden")
	expect(t, gm, "Not spawned: Old Grak is already about.")
	send(t, gm, "GM SPAWN nothing")
	expect(t, gm, "Not spawned: there is no monster called nothing.")

	for i := 0; i < 80; i++ {
		send(t, fighter, "ATTACK "+name)
		time.Sleep(30 * time.Millisecond)
	}
	expect(t, fighter, "You earned the flame badge.")
	if a := expectMessage(t, gm, "announce"); a["text"] != "Old Grak has fallen to visitor!" {
		t.Errorf("announce = %v", a)
	}
	send(t, fighter, "INVENTORY")
	expect(t, fighter, "GRAK TROPHY")

	// With the boss gone, a GM can call another.
	send(t, gm, "GM SPAWN warden")
	expect(t, gm, "Old Grak is in Hall.")
}
