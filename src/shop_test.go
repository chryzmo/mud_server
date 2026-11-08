package main

import (
	"fmt"
	mudserver "mud_server/src/internal/mudserver"
	"strings"
	"testing"
	"time"
)

// Shops through a real server (internal/mudserver/shop.go).

const shopWorld = `
start_room: market
rooms:
  market:
    name: Market
    description: A market.
    spawners:
      - {npc: rat, count: 1, tick_ms: 500}
    people:
      smith:
        names: [Hammerhild, smith]
        examine: A broad smith.
        greeting: "Welcome."
        shop:
          - {item: dagger, price: 7}
          - {item: charm, price: 3}
      idler:
        names: [Pip]
        examine: A bystander.
        greeting: "Hello."
items:
  dagger:
    name: BRASS DAGGER
    description: A short brass dagger.
    equipment: {slot: weapon, attack: 2}
  charm:
    name: BRASS CHARM
    description: A charm for looks.
    equipment: {slot: cosmetic}
npcs:
  rat:
    race: RAT
    hp: 5
    mp: 0
    attack: 1
    accuracy: 100
    defense: 0
    combat_tick_ms: 500
    behavior: orc
    xp: 1
    loot_gold_min: 10
    loot_gold_max: 10
`

func TestAShopkeeperSellsForGoldAndOnlyWhatIsListed(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		withContent(contentDir(t, map[string]string{"world.yaml": shopWorld}))(c)
		c.Connection.CommandsPerSecond, c.Connection.CommandBurst = 1000, 1000
	})
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")
	loginNew(t, conn, "buyer")
	expect(t, conn, "You are in 'Market'")

	// The room frame marks the shopkeeper, not the bystander.
	for {
		room := expectMessage(t, conn, "room")
		people, _ := room["people"].([]any)
		if len(people) == 2 {
			for _, p := range people {
				pm := p.(map[string]any)
				verbs, _ := pm["verbs"].([]any)
				if (pm["name"] == "Hammerhild") != (len(verbs) == 1 && verbs[0] == "SHOP") {
					t.Errorf("verbs for %v = %v", pm["name"], pm["verbs"])
				}
			}
			break
		}
	}

	send(t, conn, "SHOP")
	list := expect(t, conn, "Hammerhild sells")
	for _, want := range []string{"BRASS DAGGER", "7 gold", "attack +2", "BRASS CHARM", "worn for looks"} {
		if !strings.Contains(list, want) {
			t.Errorf("SHOP lacks %q:\n%s", want, list)
		}
	}
	send(t, conn, "SHOP Pip")
	expect(t, conn, "Pip has nothing for sale.")
	send(t, conn, "BUY sword")
	expect(t, conn, "does not sell that")

	send(t, conn, "BUY dagger")
	expect(t, conn, "BRASS DAGGER costs 7 gold and you have 0.")

	// Earn gold from the rat, then buy.
	name := monsterTap(t, conn)
	for i := 0; i < 80; i++ {
		send(t, conn, "ATTACK "+name)
		time.Sleep(30 * time.Millisecond)
		if i%10 == 9 {
			send(t, conn, "INVENTORY")
		}
	}
	expect(t, conn, "You receive 10 gold.")
	send(t, conn, "BUY dagger")
	expect(t, conn, "for 7 gold.")
	send(t, conn, "INVENTORY")
	inv := expect(t, conn, "BRASS DAGGER")
	if !strings.Contains(inv, "3x GOLD") && !strings.Contains(inv, "GOLD") {
		t.Errorf("inventory = %q", inv)
	}
	send(t, conn, "BUY charm from smith")
	expect(t, conn, "for 3 gold.")
	send(t, conn, "BUY charm")
	expect(t, conn, "BRASS CHARM costs 3 gold and you have 0.")
	_ = fmt.Sprint
}
