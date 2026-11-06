package main

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"

	mudserver "mud_server/src/internal/mudserver"
)

/*
	Characterization tests: they pin down how the game behaves today, before the
	Phase 1 game-loop rework (plans/game-loop.md) moves this code around. If one
	of these fails after a refactor, behavior changed.
*/

// expectWithin is like expect but returns false instead of failing when want doesn't arrive in time.
func expectWithin(conn *websocket.Conn, want string, wait time.Duration) (string, bool) {
	deadline := time.Now().Add(wait)
	for {
		conn.SetReadDeadline(deadline)
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return "", false
		}
		if text := flatten(msg); strings.Contains(text, want) {
			return text, true
		}
	}
}

func TestPlayersSeeEachOther(t *testing.T) {
	wsURL := startTestServer(t)
	alice := register(t, wsURL, "alice")
	bob := register(t, wsURL, "bob")

	expect(t, alice, "bob\x1b[0m has appeared in the room.")

	send(t, bob, "SAY hi alice")
	expect(t, bob, `You say: "hi alice"`)
	expect(t, alice, `bob`+"\x1b[0m"+` says: "hi alice"`)

	send(t, bob, "MOVE N")
	expect(t, alice, "bob\x1b[0m has left to the N.")
}

func TestWhisper(t *testing.T) {
	wsURL := startTestServer(t)
	alice := register(t, wsURL, "alice")
	bob := register(t, wsURL, "bob")

	// Bob moves away: whispers reach any online player, not just the room.
	send(t, bob, "MOVE N")
	expect(t, bob, "You are in 'Balcony'")

	send(t, alice, "WHISPER bob meet me upstairs")
	expect(t, alice, `You whisper to bob, "meet me upstairs"`)
	expect(t, bob, `alice whispers, "meet me upstairs"`)

	send(t, alice, "WHISPER nobody hello?")
	expect(t, alice, "Player 'nobody' is not online.")
}

func TestGetDropInventory(t *testing.T) {
	wsURL := startTestServer(t)
	conn := register(t, wsURL, "carrier")

	send(t, conn, "INVENTORY")
	expect(t, conn, "Your inventory is empty.")

	send(t, conn, "GET key")
	expect(t, conn, "You pick up KEY.")
	send(t, conn, "I")
	expect(t, conn, "1x KEY")
	send(t, conn, "LOOK")
	if msg := expect(t, conn, "You are in 'Start Room'"); strings.Contains(msg, "You see:") {
		t.Errorf("KEY still listed in the room after GET:\n%s", msg)
	}

	send(t, conn, "GET key")
	expect(t, conn, "You do not see that item here.")

	send(t, conn, "DROP KEY")
	expect(t, conn, "You drop KEY.")
	send(t, conn, "L")
	expect(t, conn, "You see:\n\x1b[1;32mKEY")
}

func TestInventorySurvivesRelogin(t *testing.T) {
	wsURL := startTestServer(t)
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")
	send(t, conn, "/REGISTER hoarder secret")
	expect(t, conn, "You are in 'Start Room'")
	send(t, conn, "GET KEY")
	expect(t, conn, "You pick up KEY.")
	conn.Close()

	again, msg := loginWhenFree(t, wsURL, "hoarder", "secret")
	if !strings.Contains(msg, "Welcome back, hoarder!") {
		t.Fatalf("login: got %q", msg)
	}
	send(t, again, "INVENTORY")
	expect(t, again, "1x KEY")
}

func TestEquipmentChangesStats(t *testing.T) {
	wsURL, _ := startTestServerWith(t,
		`UPDATE room SET inventoryidslice = json_build_object('i1', 1, 'i' || (SELECT id FROM item WHERE name = 'IRON SWORD'), 1) WHERE id = 1`)
	conn := register(t, wsURL, "knight")

	send(t, conn, "GET IRON SWORD")
	expect(t, conn, "You pick up IRON SWORD.")
	send(t, conn, "EQUIP IRON SWORD")
	expect(t, conn, "You equip IRON SWORD.")
	expect(t, conn, "STATUPDATE:hp=20,maxhp=20,mp=10,maxmp=10,atk=10,atkbonus=5,def=10,defbonus=0")

	send(t, conn, "STATS")
	expect(t, conn, "10 (+5 from equipment)")
	send(t, conn, "EQUIP")
	expect(t, conn, "[Weapon] IRON SWORD")

	send(t, conn, "GET KEY")
	expect(t, conn, "You pick up KEY.")
	send(t, conn, "EQUIP KEY")
	expect(t, conn, "KEY is not equippable.")

	send(t, conn, "UNEQUIP weapon")
	expect(t, conn, "You unequip IRON SWORD and stow it in your inventory.")
	expect(t, conn, "STATUPDATE:hp=20,maxhp=20,mp=10,maxmp=10,atk=5,atkbonus=0")
	send(t, conn, "INVENTORY")
	expect(t, conn, "1x IRON SWORD")
}

// NPC names are random; take the first one shown in red (LOOK output, attacks)
// or announced by the spawner.
var npcNamePattern = regexp.MustCompile(`\x1b\[1;31m([^\x1b]+)\x1b\[0m|^(\S+) - ORC - appears in the room!`)

func waitForNPC(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		conn.SetReadDeadline(deadline)
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for an NPC: %v", err)
		}
		if m := npcNamePattern.FindStringSubmatch(flatten(msg)); m != nil {
			return m[1] + m[2]
		}
	}
}

func TestCombatPlayerKillsNPC(t *testing.T) {
	wsURL, _ := startTestServerWith(t,
		`UPDATE baddiespawner SET tickdurationms = 100`,
		`UPDATE baddienpc_generic SET combatTickerDurationInMS = 100, attackpower = 0, attackaccuracy = 0, defense = 0, hp = 5`)
	conn := register(t, wsURL, "hero")

	send(t, conn, "MOVE N")
	expect(t, conn, "You are in 'Balcony'")
	send(t, conn, "MOVE E")
	name := waitForNPC(t, conn)

	// Defense 0 means almost every swing hits, and one hit kills. A miss sends
	// nothing, so keep swinging from a second goroutine while this one reads: a
	// websocket is dead after a read timeout, so the reader cannot give up and
	// retry. Only this goroutine's reads and the swinger's writes touch conn.
	swinging := make(chan struct{})
	defer close(swinging)
	send(t, conn, "ATTACK "+name)
	go func() {
		for {
			select {
			case <-swinging:
				return
			case <-time.After(150 * time.Millisecond):
				conn.WriteMessage(websocket.TextMessage, wireLine("ATTACK"))
			}
		}
	}()
	expect(t, conn, "You kill "+name+"!")
	expect(t, conn, "You receive")
}

func TestCombatNPCKillsPlayer(t *testing.T) {
	wsURL, _ := startTestServerWith(t,
		`UPDATE baddiespawner SET tickdurationms = 100`,
		`UPDATE baddienpc_generic SET combatTickerDurationInMS = 100, attackpower = 25, attackaccuracy = 100`)
	conn := register(t, wsURL, "victim")

	send(t, conn, "MOVE N")
	expect(t, conn, "You are in 'Balcony'")
	send(t, conn, "MOVE E")
	name := waitForNPC(t, conn)

	expect(t, conn, "You were killed by "+name+"!")
	expect(t, conn, "You are in 'Start Room'")

	send(t, conn, "STATS")
	expect(t, conn, "20 / 20")
}

func TestShutdownSavesAndDisconnects(t *testing.T) {
	wsURL, si := startTestServerWith(t)
	conn := register(t, wsURL, "saver")
	send(t, conn, "MOVE N")
	expect(t, conn, "You are in 'Balcony'")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	si.Shutdown(ctx)

	expect(t, conn, "The server is restarting.")
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Error("connection still open after Shutdown")
	}

	db, err := pgx.Connect(context.Background(), testDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(context.Background())
	var room int32
	if err := db.QueryRow(context.Background(), "SELECT roomid FROM player WHERE username = 'saver'").Scan(&room); err != nil {
		t.Fatalf("saved player: %v", err)
	}
	if room != 2 {
		t.Errorf("saved roomid = %d, want 2 (Balcony)", room)
	}
}

func TestStartupFailsWithoutDatabase(t *testing.T) {
	c := mudserver.Configuration{}
	c.Database.DatabaseURL = "postgres://mud:mud@127.0.0.1:1/mud"
	c.Database.StartupWaitSeconds = 1

	start := time.Now()
	si, err := mudserver.NewMudServer(c)
	if err == nil {
		si.Shutdown(context.Background())
		t.Fatal("NewMudServer succeeded with no database")
	}
	if !strings.Contains(err.Error(), "database unreachable") {
		t.Errorf("error = %v, want database unreachable", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %v to give up, want about 1s", elapsed)
	}
}
