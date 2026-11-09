package main

import (
	"strings"
	"testing"
	"time"

	mudserver "mud_server/src/internal/mudserver"
)

// Consumables through a real server (internal/mudserver/consume.go).

const consumeWorld = `
start_room: yard
rooms:
  yard:
    name: Yard
    description: A yard.
    features:
      crate: {names: [crate], examine: A crate.}
    rules:
      - on: {verb: PULL, target: crate}
        do: [{give_item: potion}]
    spawners:
      - {npc: rat, count: 1, tick_ms: 500}
items:
  potion:
    name: RED POTION
    description: A red potion.
    consume: {hp: 30}
npcs:
  rat:
    race: RAT
    hp: 500
    mp: 0
    attack: 3
    accuracy: 100
    defense: 0
    combat_tick_ms: 500
    behavior: orc
    xp: 1
`

func TestAPotionHealsUpToTheMaximumAndIsUsedUp(t *testing.T) {
	t.Parallel()
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		withContent(contentDir(t, map[string]string{"world.yaml": consumeWorld}))(c)
		c.Connection.CommandsPerSecond, c.Connection.CommandBurst = 1000, 1000
	})
	conn := mobPlayer(t, wsURL, "drinker")
	send(t, conn, "PULL crate")
	expect(t, conn, "You receive")

	send(t, conn, "USE potion")
	expect(t, conn, "You do not need red potion just now.")
	send(t, conn, "INVENTORY")
	expect(t, conn, "RED POTION") // kept

	name := monsterTap(t, conn)
	send(t, conn, "ATTACK "+name)
	time.Sleep(2 * time.Second) // the rat hits back every half second
	send(t, conn, "USE potion")
	msg := expect(t, conn, "You use")
	if !strings.Contains(msg, "HP)") {
		t.Errorf("message = %q", msg)
	}
	send(t, conn, "INVENTORY")
	if inv := expect(t, conn, "inventory"); strings.Contains(inv, "RED POTION") {
		t.Errorf("the potion was not used up: %q", inv)
	}
}
