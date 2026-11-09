package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// The five gear slots: weapon, armor, shield, boots and cosmetic. The sample catalog still uses the
// retired names (chest, legs, helmet, ring), which must keep working.

// equipID is a SQL expression for an equipment catalog id by item name.
func equipID(item string) string {
	return fmt.Sprintf(`(SELECT e.id FROM equipment e JOIN item i ON i.id = e.itemid WHERE i.name = %s)`, sqlQuote(item))
}

func startRoomLogin(t *testing.T, wsURL, name string) *websocket.Conn {
	t.Helper()
	conn, msg := loginWhenFree(t, wsURL, name, "secret")
	if !strings.Contains(msg, "Welcome back") {
		t.Fatalf("login = %q", msg)
	}
	expect(t, conn, "You are in 'Start Room'")
	return conn
}

func TestSavedGearMovesToTheFiveSlotsAtLogin(t *testing.T) {
	t.Parallel()
	wsURL := startTestServer(t)
	createAccount(t, "knight", "secret")
	execSQL(t, testDatabaseURL(t), fmt.Sprintf(
		`UPDATE player SET equipmentslots = json_build_object('weapon', %s, 'chest', %s, 'legs', %s, 'helmet', %s, 'ring1', %s) WHERE username = 'knight'`,
		equipID("IRON SWORD"), equipID("LEATHER ARMOR"), equipID("LEATHER LEGGINGS"), equipID("LEATHER CAP"), equipID("COPPER RING")))

	conn := startRoomLogin(t, wsURL, "knight")
	send(t, conn, "EQUIP")
	list := expect(t, conn, "[Weapon] IRON SWORD")
	for _, want := range []string{"[Armor]  LEATHER ARMOR", "[Shield] (empty)", "[Boots]  LEATHER LEGGINGS", "[Cosmetic] (empty)"} {
		if !strings.Contains(list, want) {
			t.Errorf("EQUIP list lacks %q:\n%s", want, list)
		}
	}

	// The retired helmet and ring slots were returned to the pack, not lost.
	send(t, conn, "INVENTORY")
	pack := expect(t, conn, "LEATHER CAP")
	if !strings.Contains(pack, "COPPER RING") {
		t.Errorf("the ring was lost:\n%s", pack)
	}
}

func TestACosmeticAddsNoStatsAndOthersSeeWhatYouWear(t *testing.T) {
	t.Parallel()
	wsURL := startTestServer(t)
	createAccount(t, "dandy", "secret")
	createAccount(t, "watcher", "secret")
	execSQL(t, testDatabaseURL(t), fmt.Sprintf(
		`UPDATE player SET inventoryidslice = json_build_object('i' || (SELECT id FROM item WHERE name = 'LEATHER CAP'), 1) WHERE username = 'dandy'`))

	dandy := startRoomLogin(t, wsURL, "dandy")
	watcher := startRoomLogin(t, wsURL, "watcher")

	send(t, dandy, "EQUIP LEATHER CAP")
	expect(t, dandy, "You put on LEATHER CAP. It is for looks only.")
	expect(t, dandy, "STATUPDATE:hp=20,maxhp=20,mp=10,maxmp=10,atk=5,atkbonus=0,def=10,defbonus=0")

	send(t, watcher, "EXAMINE dandy")
	expect(t, watcher, "They wear LEATHER CAP.")

	send(t, dandy, "UNEQUIP cosmetic")
	expect(t, dandy, "You unequip LEATHER CAP and stow it in your inventory.")
}
