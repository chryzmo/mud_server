package main

import (
	"testing"
	"time"

	"github.com/gorilla/websocket"

	mudserver "mud_server/src/internal/mudserver"
)

// Small mobs through a real server and a content world (internal/mudserver/mobs.go).

const mobWorld = `
start_room: yard
rooms:
  yard:
    name: Yard
    description: A swept yard.
    spawners:
      - {npc: rat, count: 1, tick_ms: 500}
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
`

func mobServer(t *testing.T) string {
	t.Helper()
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		withContent(contentDir(t, map[string]string{"world.yaml": mobWorld}))(c)
		noWait(c)
		c.Connection.CommandsPerSecond, c.Connection.CommandBurst = 1000, 1000
	})
	return wsURL
}

// mobPlayer logs in a new player and waits until they are in the yard.
func mobPlayer(t *testing.T, wsURL, name string) *websocket.Conn {
	t.Helper()
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")
	loginNew(t, conn, name)
	expect(t, conn, "Welcome back, "+name+"!")
	expect(t, conn, "You are in 'Yard'")
	return conn
}

// monsterTap reads room frames until one lists a monster with the ATTACK verb, and returns its name.
func monsterTap(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		frame := expectMessage(t, conn, "room")
		if things, _ := frame["things"].([]any); len(things) > 0 {
			thing := things[0].(map[string]any)
			if verbs, _ := thing["verbs"].([]any); len(verbs) == 1 && verbs[0] == "ATTACK" {
				return thing["name"].(string)
			}
		}
	}
	t.Fatal("no monster ever appeared as a tap target")
	return ""
}

func TestAMonsterAppearsOnlyForPlayersAndFightsBackOnlyWhenAttacked(t *testing.T) {
	wsURL := mobServer(t)
	conn := mobPlayer(t, wsURL, "fighter")
	name := monsterTap(t, conn) // it came after the player did, and tappably

	time.Sleep(1500 * time.Millisecond) // three of its combat ticks: a passive monster stays quiet
	send(t, conn, "LOOK")
	neverSaw(t, readUntil(t, conn, "You are in"), "swings at you")

	send(t, conn, "ATTACK "+name)
	expect(t, conn, "swings at you") // now it fights back

	for i := 0; i < 60; i++ {
		send(t, conn, "ATTACK "+name)
		time.Sleep(40 * time.Millisecond)
	}
	expect(t, conn, "You kill "+name)
	expect(t, conn, "(+6 combat XP)")
}

func TestEveryoneWhoHitAMonsterIsPaidInFull(t *testing.T) {
	wsURL := mobServer(t)
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
	expect(t, a, "combat XP") // one of them landed the blow and one helped: both are paid in full
	expect(t, b, "(+6 combat XP)")
}
