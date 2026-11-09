package main

import (
	"strings"
	"testing"
	"time"

	mudserver "mud_server/src/internal/mudserver"
)

// Monster drop tables through a real server (internal/mudserver/mobs.go, rollDrops).

const dropWorld = `
start_room: yard
rooms:
  yard:
    name: Yard
    description: A swept yard.
    spawners:
      - {npc: rat, count: 1, tick_ms: 500}
items:
  charm:
    name: LUCKY CHARM
    description: A small brass charm.
npcs:
  rat:
    race: RAT
    hp: 20
    mp: 1
    attack: 1
    accuracy: 100
    defense: 0
    combat_tick_ms: 500
    behavior: orc
    xp: 6
    drops:
      - {item: charm, chance: 100}
      - {badge: flame, chance: 100}
`

func TestEveryFighterRollsForTheirOwnDropsAndACosmeticDropIsEarnedOnce(t *testing.T) {
	t.Parallel()
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		withContent(contentDir(t, map[string]string{"world.yaml": dropWorld}))(c)
		noWait(c)
		c.Connection.CommandsPerSecond, c.Connection.CommandBurst = 1000, 1000
	})
	a := mobPlayer(t, wsURL, "firstblow")
	b := mobPlayer(t, wsURL, "secondblow")
	name := monsterTap(t, a)

	send(t, a, "ATTACK "+name)
	send(t, b, "ATTACK "+name)
	for i := 0; i < 80; i++ {
		send(t, a, "ATTACK "+name)
		send(t, b, "ATTACK "+name)
		time.Sleep(30 * time.Millisecond)
	}
	// Both were paid the drops, whichever of them landed the blow.
	expect(t, a, "You earned the flame badge.")
	expect(t, b, "You earned the flame badge.")
	send(t, a, "INVENTORY")
	if inv := expect(t, a, "LUCKY CHARM"); !strings.Contains(inv, "LUCKY CHARM") {
		t.Errorf("inventory = %q", inv)
	}
	send(t, b, "INVENTORY")
	expect(t, b, "LUCKY CHARM")
}
