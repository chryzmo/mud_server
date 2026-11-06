package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	mudserver "mud_server/src/internal/mudserver"
)

// The game master (plans/event-systems.md section 2): one GM, a password account with the gm flag.

// gmPlayer logs in as a game master (an account with the gm flag).
func gmPlayer(t *testing.T, wsURL, name string) *websocket.Conn {
	t.Helper()
	createAccount(t, name, "secret")
	execSQL(t, testDatabaseURL(t), fmt.Sprintf("UPDATE player SET gm=true WHERE username=%s", sqlQuote(name)))
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")
	send(t, conn, "/LOGIN "+name+" secret")
	expect(t, conn, "Welcome back, "+name+"!")
	expect(t, conn, "You are in 'Start Room'")
	return conn
}

func TestGMCommandDoesNotExistForOthers(t *testing.T) {
	wsURL := startTestServer(t)
	conn := passwordPlayer(t, wsURL, "player")
	send(t, conn, "GM WHO")
	expect(t, conn, "Unknown command.")
	send(t, conn, "HELP")
	if help := expect(t, conn, "Available commands"); strings.Contains(help, "GM") {
		t.Errorf("HELP advertises GM to a player: %q", help)
	}

	gm := gmPlayer(t, wsURL, "boss")
	send(t, gm, "HELP")
	expect(t, gm, "GM")
	send(t, gm, "GM")
	expect(t, gm, "GM commands:")
}

func TestAGuestIsNeverAGM(t *testing.T) {
	url := testDatabaseURL(t)
	resetDatabase(t, url)
	wsA, serverA := serveTestServer(t, url, nil)
	conn, auth := joinAs(t, wsA, "Sneaky")
	if auth["ok"] != true {
		t.Fatalf("join = %v", auth)
	}
	expect(t, conn, "You are in 'Start Room'")
	conn.Close()
	serverA.Shutdown(t.Context())

	// Even with the flag set in the guest's row, a restarted server loads them as a guest.
	execSQL(t, url, "UPDATE player SET gm=true WHERE username='Sneaky'")
	wsB, _ := serveTestServer(t, url, nil)
	g := joinGuestResume(t, wsB, auth)
	send(t, g, "GM WHO")
	expect(t, g, "Unknown command.")
}

// joinGuestResume reconnects a guest with the token from their join reply.
func joinGuestResume(t *testing.T, wsURL string, auth map[string]any) *websocket.Conn {
	t.Helper()
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")
	writeRaw(t, conn, fmt.Sprintf(`{"t":"resume","token":%q}`, auth["token"]))
	if got := expectMessage(t, conn, "auth"); got["ok"] != true {
		t.Fatalf("resume = %v", got)
	}
	expect(t, conn, "You are in")
	return conn
}

func TestGMWhoCountsAndNames(t *testing.T) {
	wsURL := startTestServer(t)
	gm := gmPlayer(t, wsURL, "boss")
	passwordPlayer(t, wsURL, "walker")
	joinAs(t, wsURL, "Guesty")
	expect(t, gm, "Guesty") // the arrival notice: everyone is in the same room
	send(t, gm, "GM WHO")
	out := expect(t, gm, "Online: 3 (1 guests, 2 accounts, 0 away). Visitors: welcome.")
	for _, want := range []string{"boss [GM] (Start Room)", "walker (Start Room)", "Guesty (Start Room)"} {
		if !strings.Contains(out, want) {
			t.Errorf("GM WHO is missing %q in %q", want, out)
		}
	}
	send(t, gm, "GM WHO wal")
	if out := expect(t, gm, "Online: 3"); !strings.Contains(out, "walker") || strings.Contains(out, "Guesty (") {
		t.Errorf("prefix filter: %q", out)
	}
}

func TestGMMuteBlocksSpeechAndSurvivesInTheDatabase(t *testing.T) {
	wsURL := startTestServer(t)
	gm := gmPlayer(t, wsURL, "boss")
	bob := passwordPlayer(t, wsURL, "bob")
	expect(t, gm, "bob")                   // arrival
	ear := passwordPlayer(t, wsURL, "ear") // listens in the same room; a read timeout kills a connection, so only this one is used for "heard nothing"

	send(t, gm, "GM MUTE BOB 30") // names ignore case
	expect(t, gm, "bob is muted for 30 minutes.")
	expect(t, bob, "A GM has muted you for 30 minutes.")
	send(t, bob, "SAY hello")
	expect(t, bob, "You are muted for another")
	send(t, bob, "WHISPER boss psst")
	expect(t, bob, "You are muted for another")
	if text, heard := expectWithin(ear, "hello", 300*time.Millisecond); heard {
		t.Errorf("someone heard a muted player: %q", text)
	}
	send(t, bob, "MOVE N") // everything else still works
	expect(t, bob, "You are in 'Balcony'")
	eventually(t, "mute saved", "SELECT (muteduntil > now() + interval '29 minutes')::text FROM player WHERE username='bob'", func(v string) bool { return v == "true" })

	send(t, gm, "GM UNMUTE bob")
	expect(t, gm, "bob can speak again.")
	expect(t, bob, "A GM has lifted your mute.")
	send(t, bob, "SAY back")
	expect(t, bob, `You say: "back"`)
	eventually(t, "mute cleared", "SELECT coalesce(muteduntil::text,'none') FROM player WHERE username='bob'", func(v string) bool { return v == "none" })

	send(t, gm, "GM MUTE boss") // a GM cannot be muted
	expect(t, gm, "A GM cannot be muted.")
	send(t, gm, "GM MUTE nobody")
	expect(t, gm, "No one called nobody is here.")
	send(t, gm, "GM MUTE bob soon")
	expect(t, gm, "Minutes must be a whole number")
}

func TestAMuteIsLoadedAgainAfterARestart(t *testing.T) {
	url := testDatabaseURL(t)
	resetDatabase(t, url)
	wsA, serverA := serveTestServer(t, url, nil)
	gm := gmPlayer(t, wsA, "boss")
	bob := passwordPlayer(t, wsA, "bob")
	expect(t, gm, "bob")
	send(t, gm, "GM MUTE bob 60")
	expect(t, bob, "A GM has muted you")
	eventually(t, "mute saved", "SELECT (muteduntil IS NOT NULL)::text FROM player WHERE username='bob'", func(v string) bool { return v == "true" })
	bob.Close()
	gm.Close()
	serverA.Shutdown(t.Context())

	wsB, _ := serveTestServer(t, url, nil)
	conn := dial(t, wsB)
	expect(t, conn, "Welcome to the MUD!")
	send(t, conn, "/LOGIN bob secret")
	expect(t, conn, "Welcome back, bob!")
	send(t, conn, "SAY still muted?")
	expect(t, conn, "You are muted for another")
}

func TestGMTeleportAndSummon(t *testing.T) {
	wsURL := startTestServer(t)
	gm := gmPlayer(t, wsURL, "boss")
	bob := passwordPlayer(t, wsURL, "bob")
	expect(t, gm, "bob")
	send(t, bob, "MOVE N")
	expect(t, bob, "You are in 'Balcony'")
	expect(t, gm, "has left to the N") // the departure notice in the start room

	send(t, gm, "GM TELEPORT bob")
	expect(t, gm, "You are in 'Balcony'")
	expect(t, bob, "boss") // bob sees the GM arrive

	send(t, gm, "GM TELEPORT ROOM start")
	expect(t, gm, "You are in 'Start Room'")
	send(t, gm, "GM TELEPORT ROOM nowhere")
	expect(t, gm, "No room's name starts with that.")

	send(t, gm, "GM SUMMON bob")
	expect(t, bob, "A GM has summoned you.")
	expect(t, bob, "You are in 'Start Room'")
	expect(t, gm, "bob is here.")
}

func TestGMAnnounceReachesEveryone(t *testing.T) {
	wsURL := startTestServer(t)
	gm := gmPlayer(t, wsURL, "boss")
	far := passwordPlayer(t, wsURL, "far")
	send(t, far, "MOVE N")
	expect(t, far, "You are in 'Balcony'")
	send(t, gm, "GM ANNOUNCE The feast begins at nine!")
	expect(t, far, "[GM] The feast begins at nine!")
	expect(t, gm, "Announced to 2 players.")
	send(t, gm, "GM ANNOUNCE")
	expect(t, gm, "Usage: GM ANNOUNCE")
}

func TestGMLockRefusesNewVisitorsButNotThoseAlreadyIn(t *testing.T) {
	wsURL := startTestServer(t)
	gm := gmPlayer(t, wsURL, "boss")
	conn, auth := joinAs(t, wsURL, "Early")
	if auth["ok"] != true {
		t.Fatalf("join = %v", auth)
	}
	expect(t, conn, "You are in")

	send(t, gm, "GM LOCK")
	expect(t, gm, "New visitors are locked out.")
	_, refused := joinAs(t, wsURL, "Late")
	if refused["ok"] != false || refused["code"] != "joins_locked" {
		t.Errorf("join while locked = %v, want joins_locked", refused)
	}
	// Someone already in can reconnect, and a GM can log in.
	conn.Close()
	back := joinGuestResume(t, wsURL, auth)
	send(t, back, "LOOK")
	expect(t, back, "You are in")

	send(t, gm, "GM WHO")
	expect(t, gm, "Visitors: locked.")
	send(t, gm, "GM UNLOCK")
	expect(t, gm, "New visitors are welcome again.")
	if _, ok := joinAs(t, wsURL, "Late"); ok["ok"] != true {
		t.Errorf("join after unlock = %v", ok)
	}
}

func TestGMCommandsAreAudited(t *testing.T) {
	wsURL := startTestServer(t)
	execSQL(t, testDatabaseURL(t), "DELETE FROM gm_audit") // other tests leave rows behind
	gm := gmPlayer(t, wsURL, "boss")
	passwordPlayer(t, wsURL, "bob")
	expect(t, gm, "bob")
	send(t, gm, "GM MUTE bob 5")
	expect(t, gm, "bob is muted")
	send(t, gm, "GM LOCK")
	expect(t, gm, "locked out")
	eventually(t, "audit rows", "SELECT coalesce(string_agg(command || ':' || args || ':' || result, '|' ORDER BY at, id), '') FROM gm_audit WHERE gm='boss'",
		func(v string) bool { return v == "MUTE:bob 5:muted bob 5m|LOCK::locked" })
	send(t, gm, "GM WHO") // reads are audited too
	eventually(t, "who audited", "SELECT count(*)::text FROM gm_audit WHERE command='WHO'", func(v string) bool { return v == "1" })
}

func TestAddGMCreatesAndPromotesAccounts(t *testing.T) {
	url := testDatabaseURL(t)
	resetDatabase(t, url)
	c := mudserver.Configuration{}
	c.Database.DatabaseURL = url
	c.Game.PlayerStartHP, c.Game.PlayerStartMaxHP = 20, 20

	created, err := mudserver.AddGM(c, "Chris", "correct horse battery")
	if err != nil || !created {
		t.Fatalf("AddGM new = %v, %v", created, err)
	}
	if got := queryString(t, "SELECT (gm AND NOT guest AND passwordhash <> '')::text FROM player WHERE username='Chris'"); got != "true" {
		t.Errorf("the new account is not a GM account: %s", got)
	}

	// The account can log in and has the GM command.
	wsURL, _ := serveTestServer(t, url, nil)
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")
	send(t, conn, "/LOGIN Chris correct horse battery")
	expect(t, conn, "Welcome back, Chris!")
	send(t, conn, "GM")
	expect(t, conn, "GM commands:")

	// Running it again promotes in place and replaces the password.
	if created, err := mudserver.AddGM(c, "chris", "another long password"); err != nil || created {
		t.Errorf("AddGM existing = %v, %v", created, err)
	}

	for _, bad := range []struct{ name, password, want string }{
		{"x", "correct horse battery", "2 to 32"},
		{"has space", "correct horse battery", "spaces"},
		{"Fine", "short", "at least 12"},
		{"Fine", strings.Repeat("a", 73), "at most 72"},
	} {
		if _, err := mudserver.AddGM(c, bad.name, bad.password); err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("AddGM(%q) = %v, want an error containing %q", bad.name, err, bad.want)
		}
	}

	// A guest's name cannot be taken over.
	joinAs(t, wsURL, "Guesty")
	if _, err := mudserver.AddGM(c, "Guesty", "correct horse battery"); err == nil || !strings.Contains(err.Error(), "guest") {
		t.Errorf("AddGM on a guest name = %v, want a refusal", err)
	}
}

// A guest who resumes on a new connection the instant they are admitted must not race with
// the join that admitted them (join used to read player.Session after admission, while the
// resume was writing it). The window is tiny, so this is a stress test: run under -race it
// fails reliably only on a loaded machine.
func TestJoinThenResumeAtOnceDoesNotRace(t *testing.T) {
	wsURL := startTestServer(t)
	for i := 0; i < 40; i++ {
		conn, auth := joinAs(t, wsURL, fmt.Sprintf("Quick_%d", i))
		if auth["ok"] != true {
			t.Fatalf("join %d = %v", i, auth)
		}
		again := dial(t, wsURL)
		expect(t, again, "Welcome to the MUD!")
		writeRaw(t, again, fmt.Sprintf(`{"t":"resume","token":%q}`, auth["token"]))
		expectMessage(t, again, "auth")
		conn.Close()
		again.Close()
	}
}
