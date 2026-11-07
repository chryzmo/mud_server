package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Scheduled events and world flags (plans/event-systems.md section 4) through a real server. The
// state machine itself is tested with a moving clock in internal/mudserver/scheduler_test.go.

// eventsWorld has a vault that opens while the world flag con-open is set, a lever that works
// only while late-flag is set, and an event that should already be running when the server starts.
func eventsWorld(t *testing.T) string {
	t.Helper()
	// The event time has minute precision: start the late one two minutes ago, so its announcement is stale.
	late := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute).Format("2006-01-02 15:04")
	return contentDir(t, map[string]string{"world.yaml": fmt.Sprintf(`
start_room: hall
rooms:
  hall:
    name: Hall
    description: A hall.
    features:
      lever: {names: [lever], examine: A lever.}
    rules:
      - on: {verb: PULL, target: lever}
        if: [{world_flag: late-flag}]
        do: [{say: "The lever moves."}]
      - on: {verb: PULL, target: lever}
        do: [{say: "The lever is stuck."}]
    gates:
      vault:
        exit: {dir: E, to: vault}
        mode: locked
        message: "The vault door is sealed."
        open_when: con-open
  vault:
    name: Vault
    description: A vault.
    exits: {W: hall}
events:
  opening:
    title: Grand Opening
    at: "2099-01-07 12:00 America/New_York"
    duration: 10m
    steps:
      - {announce: "The Hall is open."}
      - {set_flag: con-open}
      - {after: 1s, room_say: {room: hall, text: "The Hall hums."}}
    finish:
      - {clear_flag: con-open}
  secret:
    title: A Secret
    at: "2099-01-08 03:00 America/New_York"
    hidden: true
    steps:
      - {announce: "Shh."}
  late:
    title: Old News
    at: "%s UTC"
    duration: 30m
    steps:
      - {announce: "You missed this."}
      - {set_flag: late-flag}
`, late)})
}

func eventsServer(t *testing.T) string {
	t.Helper()
	wsURL, _ := startTestServerWithConfig(t, withContent(eventsWorld(t)))
	return wsURL
}

// eventPlayer logs in a new password player in the events world.
func eventPlayer(t *testing.T, wsURL, name string) *websocket.Conn {
	t.Helper()
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")
	loginNew(t, conn, name)
	expect(t, conn, "Welcome back, "+name+"!")
	expect(t, conn, "You are in 'Hall'")
	return conn
}

// eventGM logs in a game master in the events world (whose start room is the Hall).
func eventGM(t *testing.T, wsURL, name string) *websocket.Conn {
	t.Helper()
	createAccount(t, name, "secret")
	execSQL(t, testDatabaseURL(t), fmt.Sprintf("UPDATE player SET gm=true WHERE username=%s", sqlQuote(name)))
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")
	send(t, conn, "/LOGIN "+name+" secret")
	expect(t, conn, "Welcome back, "+name+"!")
	expect(t, conn, "You are in 'Hall'")
	return conn
}

func TestAnEventAnnouncesOpensAGateForEveryoneAndClosesItAgain(t *testing.T) {
	wsURL := eventsServer(t)
	gm := eventGM(t, wsURL, "boss")
	guest := eventPlayer(t, wsURL, "visitor")
	send(t, guest, "LOOK")
	if out := expect(t, guest, "Sealed: [E]"); strings.Contains(out, "Exits: [E]") {
		t.Fatalf("the vault is open before the event: %q", out)
	}
	send(t, guest, "MOVE E")
	expect(t, guest, "The vault door is sealed.")

	send(t, gm, "GM EVENT START opening")
	expect(t, gm, "Started Grand Opening.")
	if a := expectMessage(t, guest, "announce"); a["text"] != "The Hall is open." || a["from"] != "event" {
		t.Errorf("announce = %v", a)
	}
	expect(t, guest, "The Hall hums.") // a minute-free step, one second in
	send(t, guest, "MOVE E")
	expect(t, guest, "You are in 'Vault'")
	send(t, guest, "MOVE W")
	expect(t, guest, "You are in 'Hall'")

	send(t, gm, "GM EVENT STOP opening") // the finish step clears the flag
	expect(t, gm, "Stopped Grand Opening.")
	// The vault is sealed again, and the player's room frame says so without asking.
	for {
		room := expectMessage(t, guest, "room")
		if room["locked"] != nil {
			break
		}
	}
	send(t, guest, "MOVE E")
	expect(t, guest, "The vault door is sealed.")
}

func TestEventsListsWhatIsOnAndLeavesOutHiddenOnes(t *testing.T) {
	wsURL := eventsServer(t)
	gm := eventGM(t, wsURL, "boss")
	guest := eventPlayer(t, wsURL, "asker")
	// The late event starts on the first tick after the server is up.
	for i := 0; ; i++ {
		send(t, gm, "GM EVENT")
		if strings.Contains(expect(t, gm, "secret [hidden]"), "late: Old News: RUNNING") {
			break
		}
		if i > 20 {
			t.Fatal("the late event never started")
		}
		time.Sleep(200 * time.Millisecond)
	}
	send(t, guest, "EVENTS")
	out := expect(t, guest, "Coming: Grand Opening")
	if !strings.Contains(out, "On now: Old News") || strings.Contains(out, "Secret") {
		t.Errorf("EVENTS = %q, want the running late event, the opening, and no secret", out)
	}

	send(t, gm, "GM EVENT")
	listing := expect(t, gm, "secret [hidden]")
	if !strings.Contains(listing, "opening: Grand Opening: waiting") || !strings.Contains(listing, "late: Old News: RUNNING") {
		t.Errorf("GM EVENT = %q", listing)
	}
	send(t, gm, "GM EVENT SKIP opening")
	expect(t, gm, "Skipped the next occurrence of Grand Opening.")
	send(t, guest, "EVENTS")
	if out := expect(t, guest, "On now: Old News"); strings.Contains(out, "Grand Opening") {
		t.Errorf("a skipped event is still listed: %q", out)
	}
}

func TestAnEventThatStartedBeforeTheServerStillSetsItsFlags(t *testing.T) {
	wsURL := eventsServer(t)
	late := eventPlayer(t, wsURL, "newcomer")
	// The late event began two minutes ago: its flag is set, so the lever works.
	pull := func() string {
		send(t, late, "PULL lever")
		for {
			_, data := readMessage(t, late)
			if text, _ := data["text"].(string); strings.HasPrefix(text, "The lever") {
				return text
			}
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for pull() != "The lever moves." {
		if time.Now().After(deadline) {
			t.Fatal("the late event's flag was never set")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestGMFlagOpensTheGateAndSurvivesARestart(t *testing.T) {
	url := testDatabaseURL(t)
	resetDatabase(t, url)
	dir := eventsWorld(t)
	wsA, serverA := serveTestServer(t, url, withContent(dir))
	gm := eventGM(t, wsA, "boss")
	send(t, gm, "GM FLAG con-open on")
	expect(t, gm, "World flag con-open is now set.")
	send(t, gm, "MOVE E")
	expect(t, gm, "You are in 'Vault'")
	send(t, gm, "MOVE W")
	expect(t, gm, "You are in 'Hall'")
	send(t, gm, "GM EVENT START opening")
	expect(t, gm, "Started Grand Opening.")
	eventually(t, "flag and event saved", "SELECT (SELECT count(*) FROM world_flag WHERE key='con-open')::text || COALESCE((SELECT phase FROM event_state WHERE event_key='opening'), '')", func(v string) bool { return v == "1running" })
	gm.Close()
	serverA.Shutdown(t.Context())

	wsB, _ := serveTestServer(t, url, withContent(dir))
	again := dial(t, wsB)
	expect(t, again, "Welcome to the MUD!")
	send(t, again, "/LOGIN boss secret")
	expect(t, again, "You are in 'Hall'")
	send(t, again, "MOVE E")
	expect(t, again, "You are in 'Vault'") // the flag survived
	send(t, again, "GM EVENT")
	expect(t, again, "opening: Grand Opening: RUNNING") // and so did the event
	send(t, again, "GM FLAG con-open off")
	expect(t, again, "World flag con-open is now clear.")
	send(t, again, "GM FLAG con-open")
	expect(t, again, "World flag con-open is clear.")
}

func TestPlayersAreSentTheScheduleAtLoginAndWhenItChanges(t *testing.T) {
	wsURL := eventsServer(t)
	titles := func(frame map[string]any, key string) string {
		var out []string
		for _, e := range frame[key].([]any) {
			out = append(out, e.(map[string]any)["title"].(string))
		}
		return strings.Join(out, ",")
	}

	alice := dial(t, wsURL)
	expect(t, alice, "Welcome to the MUD!")
	loginNew(t, alice, "alice")
	f := expectMessage(t, alice, "events")
	for i := 0; i < 5 && titles(f, "now") == ""; i++ { // Old News starts on the first tick, which may follow the login
		f = expectMessage(t, alice, "events")
	}
	if titles(f, "now") != "Old News" || titles(f, "next") != "Grand Opening" { // the hidden secret is left out
		t.Errorf("at login: now %q next %q", titles(f, "now"), titles(f, "next"))
	}
	if ends := f["now"].([]any)[0].(map[string]any)["ends"].(float64); ends < float64(time.Now().UnixMilli()) {
		t.Errorf("Old News should end in the future, ends = %v", ends)
	}
	if at := f["next"].([]any)[0].(map[string]any)["at"].(float64); at < float64(time.Now().UnixMilli()) {
		t.Errorf("Grand Opening should start in the future, at = %v", at)
	}

	gm := eventGM(t, wsURL, "warden")
	send(t, gm, "GM EVENT START opening")
	f = expectMessage(t, alice, "events") // everyone hears it
	if titles(f, "now") != "Old News,Grand Opening" && titles(f, "now") != "Grand Opening,Old News" {
		t.Errorf("after START: now %q", titles(f, "now"))
	}
	if titles(f, "next") != "" {
		t.Errorf("after START: next %q", titles(f, "next"))
	}
	send(t, gm, "GM EVENT STOP opening")
	f = expectMessage(t, alice, "events")
	if titles(f, "now") != "Old News" {
		t.Errorf("after STOP: now %q", titles(f, "now"))
	}
}
