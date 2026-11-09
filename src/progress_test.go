package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// XP, levels and the leaderboard (plans/event-systems.md section 6) through a real server.

func progressWorld(t *testing.T) string {
	t.Helper()
	return contentDir(t, map[string]string{"world.yaml": `
start_room: hall
rooms:
  hall:
    name: Hall
    description: A hall.
    exits: {N: vault}
    features:
      lever: {names: [lever], examine: A lever.}
  vault:
    name: Vault
    description: A vault.
    exits: {S: hall}
progression:
  levels: [0, 15, 40]
  per_level: {max_hp: 3, attack: 2, defense: 1}
  xp: {room: 10, feature: 5, kill: 5}
`})
}

// statsWhere reads stats frames until one satisfies ok.
func statsWhere(t *testing.T, conn *websocket.Conn, ok func(m map[string]any) bool) map[string]any {
	t.Helper()
	for i := 0; i < 40; i++ {
		if m := expectMessage(t, conn, "stats"); ok(m) {
			return m
		}
	}
	t.Fatal("no stats frame matched")
	return nil
}

func TestExploringEarnsXPOnceAndLevelsUpWithStatGains(t *testing.T) {
	t.Parallel()
	wsURL, _ := startTestServerWithConfig(t, withContent(progressWorld(t)))
	conn := eventPlayer(t, wsURL, "scout")
	// Standing in the Hall is the first room: 10 XP, still level 1.
	m := statsWhere(t, conn, func(m map[string]any) bool { return m["xp"] == 10.0 })
	if m["level"] != 1.0 || m["next"] != 15.0 || m["maxhp"] != 20.0 {
		t.Fatalf("stats = %v", m)
	}

	send(t, conn, "EXAMINE lever") // +5 exploration XP: 15 is level 2
	expect(t, conn, "You reach level 2!")
	m = statsWhere(t, conn, func(m map[string]any) bool { return m["level"] == 2.0 })
	if m["xp"] != 15.0 || m["next"] != 40.0 || m["maxhp"] != 23.0 || m["atk"] != 7.0 || m["def"] != 11.0 {
		t.Fatalf("after the level: %v", m)
	}

	send(t, conn, "EXAMINE lever") // the same feature again earns nothing
	send(t, conn, "MOVE N")
	m = statsWhere(t, conn, func(m map[string]any) bool { return m["xp"] == 25.0 })
	send(t, conn, "MOVE S") // an old room earns nothing
	send(t, conn, "RANK")
	expect(t, conn, "Level 2, 25 XP (exploration 25, combat 0). Next level at 40 XP.")

	// It is saved, and the gains are not applied twice on the way back in.
	waitForStateRows(t, "scout", 5) // two rooms, the feature, exploration XP, the level applied
	conn.Close()
	again, _ := loginWhenFree(t, wsURL, "scout", "secret")
	m = statsWhere(t, again, func(m map[string]any) bool { return true })
	if m["level"] != 2.0 || m["xp"] != 25.0 || m["maxhp"] != 23.0 || m["atk"] != 7.0 {
		t.Errorf("after login: %v", m)
	}
}

func TestKillingAMonsterEarnsCombatXP(t *testing.T) {
	t.Parallel()
	wsURL, _ := startTestServerWith(t,
		`UPDATE baddiespawner SET tickdurationms = 100`,
		`UPDATE baddienpc_generic SET combatTickerDurationInMS = 100, attackpower = 0, attackaccuracy = 0, defense = 0, hp = 5`)
	conn := passwordPlayer(t, wsURL, "hero")
	send(t, conn, "MOVE N")
	expect(t, conn, "You are in 'Balcony'")
	send(t, conn, "MOVE E")
	name := waitForNPC(t, conn)
	swinging := make(chan struct{})
	defer close(swinging)
	send(t, conn, "ATTACK "+name)
	go swing(conn, swinging)
	expect(t, conn, "You kill "+name+"!")
	statsWhere(t, conn, func(m map[string]any) bool { return m["xp"] == 5.0 })
}

func TestTheLeaderboardRanksPlayersLeavesOutGMsAndObeysHide(t *testing.T) {
	t.Parallel()
	wsURL, ms := startTestServerWithConfig(t, withContent(progressWorld(t)))
	gm := eventGM(t, wsURL, "boss")
	alice := eventPlayer(t, wsURL, "alice")
	bob := eventPlayer(t, wsURL, "bob")
	send(t, alice, "EXAMINE lever")
	expect(t, alice, "You reach level 2!")
	// Row counts are not enough: the XP row exists from the first room (10) before the feature's
	// update (15) is written, so wait for the value the board will read.
	waitForStateValue(t, "alice", "xp", "explore", "15")
	waitForStateRows(t, "bob", 2)
	waitForStateRows(t, "boss", 2)

	board := func() map[string]any {
		t.Helper()
		if err := ms.RefreshLeaderboard(); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		ms.LeaderboardHandler(rec, httptest.NewRequest(http.MethodGet, "/api/leaderboard", nil))
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != "public, max-age=10" || rec.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("response %d %v", rec.Code, rec.Header())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	names := func(tab any) (out []string) {
		for _, e := range tab.([]any) {
			out = append(out, e.(map[string]any)["name"].(string))
		}
		return
	}

	b := board()
	if got := names(b["overall"]); len(got) != 2 || got[0] != "alice" || got[1] != "bob" { // the GM is not ranked
		t.Fatalf("overall = %v", got)
	}
	if top := b["overall"].([]any)[0].(map[string]any); top["level"] != 2.0 || top["xp"] != 15.0 || top["explore"] != 15.0 {
		t.Errorf("top entry = %v", top)
	}
	if len(names(b["fighters"])) != 0 {
		t.Errorf("fighters = %v", b["fighters"])
	}
	send(t, bob, "RANK")
	expect(t, bob, "You are number 2 of 2 on the leaderboard.")

	send(t, gm, "GM HIDE alice")
	expect(t, gm, "alice is hidden from the leaderboard")
	if got := names(board()["overall"]); len(got) != 1 || got[0] != "bob" {
		t.Errorf("after HIDE: %v", got)
	}
	send(t, gm, "GM UNHIDE alice")
	expect(t, gm, "alice is back on the leaderboard")
	if got := names(board()["overall"]); len(got) != 2 {
		t.Errorf("after UNHIDE: %v", got)
	}
	send(t, gm, "GM HIDE nobody-here")
	expect(t, gm, "No player called nobody-here")
}

func TestTheLeaderboardEndpointIsReadOnlyAndEmptyBeforeAnySnapshot(t *testing.T) {
	t.Parallel()
	_, ms := startTestServerWithConfig(t, withContent(progressWorld(t)))
	rec := httptest.NewRecorder()
	ms.LeaderboardHandler(rec, httptest.NewRequest(http.MethodPost, "/api/leaderboard", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST answered %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	ms.LeaderboardHandler(rec, httptest.NewRequest(http.MethodGet, "/api/leaderboard", nil))
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != 200 || out["overall"] == nil {
		t.Errorf("answered %d %s", rec.Code, rec.Body.String())
	}
}

// An XP gain reaches the client in italics (ESC[3m ... ESC[0m) with a blank line after it, so it stands
// apart from the text around it.
func TestXPGainsAreItalicWithABlankLineAfter(t *testing.T) {
	t.Parallel()
	wsURL, _ := startTestServerWithConfig(t, withContent(progressWorld(t)))
	conn := eventPlayer(t, wsURL, "scout")
	got := expect(t, conn, "exploration XP") // the first room
	if !strings.HasPrefix(got, "\x1b[3m") || !strings.HasSuffix(got, "\x1b[0m\n") {
		t.Errorf("exploration XP line = %q, want it wrapped in the italic escape and followed by a newline", got)
	}
}

// The line for examining a feature the first time says what was looked at and that it earned XP.
func TestFeatureXPLineNamesTheThing(t *testing.T) {
	t.Parallel()
	wsURL, _ := startTestServerWithConfig(t, withContent(progressWorld(t)))
	conn := eventPlayer(t, wsURL, "scout")
	send(t, conn, "EXAMINE lever")
	got := expect(t, conn, "You look closely at the lever. (+5 exploration XP)")
	if !strings.HasPrefix(got, "\x1b[3m") {
		t.Errorf("feature XP line = %q, want it in italics", got)
	}
	send(t, conn, "EXAMINE lever") // the second time earns nothing, so no second line
	send(t, conn, "RANK")
	if out := expect(t, conn, "Level"); strings.Contains(out, "look closely") {
		t.Errorf("a repeat examine paid again: %q", out)
	}
}
