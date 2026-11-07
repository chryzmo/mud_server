package main

import (
	"fmt"
	"testing"

	"github.com/gorilla/websocket"

	mudserver "mud_server/src/internal/mudserver"
)

// Event-room capacity, the quiet room, STAGE and RAFFLE (plans/event-systems.md, D1 and C3) through a real server.

const arenaWorld = `
start_room: hall
rooms:
  hall:
    name: Hall
    description: A hall.
    exits: {E: arena}
  arena:
    name: Arena
    description: A crowded arena.
    exits: {W: hall}
    capacity: 4
    quiet_at: 2
`

func arenaServer(t *testing.T) string {
	t.Helper()
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		withContent(contentDir(t, map[string]string{"world.yaml": arenaWorld}))(c)
		noWait(c)
		c.Connection.SayPerSecond, c.Connection.SayBurst = 1000, 1000
		c.Connection.CommandsPerSecond, c.Connection.CommandBurst = 1000, 1000
	})
	return wsURL
}

// enterArena logs in a new player and walks them into the arena.
func enterArena(t *testing.T, wsURL, name string) *websocket.Conn {
	t.Helper()
	conn := eventPlayer(t, wsURL, name)
	send(t, conn, "MOVE E")
	expect(t, conn, "You are in 'Arena'")
	return conn
}

func TestARoomHoldsItsOwnCapacity(t *testing.T) {
	wsURL := arenaServer(t)
	for i := 0; i < 4; i++ {
		enterArena(t, wsURL, fmt.Sprintf("guest%d", i))
	}
	late := eventPlayer(t, wsURL, "latecomer")
	send(t, late, "MOVE E")
	expect(t, late, "too crowded")
	send(t, late, "LOOK")
	expect(t, late, "You are in 'Hall'")
}

func TestAQuietRoomLimitsTalkButNotGMsOrTheStage(t *testing.T) {
	wsURL := arenaServer(t)
	a := enterArena(t, wsURL, "alice")
	send(t, a, "SAY one") // alone: not quiet yet
	send(t, a, "SAY two")
	expect(t, a, `You say: "two"`)

	b := enterArena(t, wsURL, "bob") // two in the room: quiet
	send(t, a, "SAY three")
	expect(t, a, `You say: "three"`) // the first line in a quiet room is allowed
	send(t, a, "SAY four")
	expect(t, a, "too crowded to talk over")
	send(t, a, "WAVE")
	expect(t, a, "too crowded to talk over") // socials share the limit

	gm := eventGM(t, wsURL, "warden")
	send(t, gm, "MOVE E")
	expect(t, gm, "You are in 'Arena'")
	send(t, gm, "SAY welcome")
	send(t, gm, "SAY everyone")
	expect(t, gm, `You say: "everyone"`) // a GM is not limited

	send(t, a, "STAGE hello")
	expect(t, a, "You do not have the stage voice.")
	send(t, gm, "GM STAGE alice ON")
	expect(t, a, "You have the stage voice.")
	send(t, a, "STAGE Welcome, everyone!")
	expect(t, a, "[Stage]")
	expect(t, b, "Welcome, everyone!")
	expect(t, gm, "Welcome, everyone!")
	send(t, a, "SAY and again")
	send(t, a, "SAY and more")
	expect(t, a, `You say: "and more"`) // the stage voice is not limited either

	send(t, gm, "GM STAGE alice OFF")
	expect(t, a, "You no longer have the stage voice.")
	send(t, a, "STAGE hello")
	expect(t, a, "You do not have the stage voice.")
	send(t, gm, "GM STAGE")
	expect(t, gm, "Usage: GM STAGE")
}

func TestRaffleDrawsAGuestNotAGM(t *testing.T) {
	wsURL := arenaServer(t)
	lucky := enterArena(t, wsURL, "lucky")
	faraway := eventPlayer(t, wsURL, "faraway") // in the hall
	gm := eventGM(t, wsURL, "warden")

	send(t, gm, "GM RAFFLE") // the GM and faraway are in the hall: faraway is the only guest there
	expect(t, gm, "Drew faraway from 1.")
	expect(t, faraway, "Raffle! The winner is")
	send(t, lucky, "SAY probe") // lucky is in the arena and must not have heard it
	neverSaw(t, readUntil(t, lucky, `You say: "probe"`), "Raffle")

	send(t, gm, "GM RAFFLE ALL")
	expect(t, gm, "from 2.")
	expect(t, lucky, "Raffle! The winner is")
	expect(t, faraway, "Raffle! The winner is")
	send(t, gm, "GM RAFFLE SOMEONE")
	expect(t, gm, "Usage: GM RAFFLE")
}

func TestRaffleWithNoGuestsSaysSo(t *testing.T) {
	wsURL := arenaServer(t)
	gm := eventGM(t, wsURL, "warden")
	send(t, gm, "GM RAFFLE")
	expect(t, gm, "No one is here to draw from.")
}
