package main

import (
	"strings"
	"testing"
	"time"

	mudserver "mud_server/src/internal/mudserver"
)

// Casting spells through a real server (internal/mudserver/cast.go).

const castWorld = `
start_room: yard
rooms:
  yard:
    name: Yard
    description: A yard.
    features:
      plaque: {names: [plaque], examine: A plaque.}
    rules:
      - on: {verb: READ, target: plaque}
        do: [{teach: bolt}, {teach: mend}, {teach: ward}, {teach: glow}, {teach: spark}]
    spawners:
      - {npc: rat, count: 1, tick_ms: 500}
spells:
  bolt: {name: Firebolt, effect: bolt, cost: 3, cooldown: 3, power: 4, description: Fire., cast: "You hurl fire at {target}.", room: "{player} hurls fire at {target}."}
  mend: {name: Mend, effect: mend, cost: 4, cooldown: 3, power: 10, description: Heals., cast: You mend.}
  ward: {name: Ward, effect: ward, cost: 5, cooldown: 20, power: 3, description: Shields., cast: You ward yourself.}
  glow: {name: Glow, effect: fun, cost: 1, cooldown: 5, description: Glows., cast: You glow., room: "{player} glows."}
  spark: {name: Spark, effect: bolt, cost: 8, cooldown: 3, power: 1, description: Sparks., cast: Spark.}
npcs:
  rat:
    race: RAT
    hp: 12
    mp: 0
    attack: 2
    accuracy: 100
    defense: 0
    combat_tick_ms: 1000
    behavior: orc
    xp: 7
`

func TestSpellsAreCastWithMagicCooldownsAndEffects(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		withContent(contentDir(t, map[string]string{"world.yaml": castWorld}))(c)
		c.Connection.CommandsPerSecond, c.Connection.CommandBurst = 1000, 1000
	})
	caster := mobPlayer(t, wsURL, "caster")
	watcher := mobPlayer(t, wsURL, "watcher")

	send(t, caster, "READ plaque")
	frame := expectMessage(t, caster, "spells")
	ready, _ := frame["ready"].([]any)
	if len(ready) != 3 || ready[0] == "" {
		t.Errorf("spells frame ready = %v", frame["ready"])
	}

	// A fun spell shows to the room, and waits for its cooldown.
	send(t, caster, "CAST glow")
	expect(t, caster, "You glow.")
	expect(t, watcher, "caster glows.")
	send(t, caster, "CAST glow")
	expect(t, caster, "Glow is not ready for another")

	// Hurt by the rat, mend from slot 2.
	name := monsterTap(t, caster)
	send(t, caster, "ATTACK "+name)
	time.Sleep(2500 * time.Millisecond)
	send(t, caster, "CAST 2")
	expect(t, caster, "You are healed for")

	// A bolt at the rat; it does not need a defence roll, and a kill pays XP.
	send(t, caster, "CAST firebolt "+name)
	expect(t, caster, "Firebolt hits "+name)
	expect(t, caster, "You kill "+name)
	expect(t, caster, "combat XP")

	// Magic is spent: glow 1 + bolt 3 + mend 4 leaves about 2, too little for a ward (5) or a spark (8).
	send(t, caster, "CAST ward")
	expect(t, caster, "You do not have the magic for Ward")
	send(t, caster, "CAST spark")
	expect(t, caster, "You do not have the magic for Spark")
	send(t, caster, "CAST nothing")
	expect(t, caster, "You do not know a spell called that.")
	send(t, caster, "CAST 9")
	expect(t, caster, "There are 3 slots")

	// A fresh player has the magic for a ward, and a mend on someone unhurt costs nothing.
	send(t, watcher, "READ plaque")
	expect(t, watcher, "You learn")
	send(t, watcher, "CAST ward")
	expect(t, watcher, "Your defence rises by 3 for 30 seconds.")
	send(t, watcher, "CAST mend caster")
	if out := expect(t, watcher, "already unhurt"); !strings.Contains(out, "caster") {
		t.Errorf("mend message = %q", out)
	}
}
