package main

import (
	"testing"

	mudserver "mud_server/src/internal/mudserver"
)

// Resetting characters for testing (`mud_server -reset-players`, `-reset-player`): characters and their
// progress go, game masters stay unless asked for, and everything else is left alone.

func resetFixture(t *testing.T) mudserver.Configuration {
	t.Helper()
	url := testDatabaseURL(t)
	resetDatabase(t, url)
	c := mudserver.Configuration{}
	c.Database.DatabaseURL = url
	// The seed holds a character of its own; start from none. AddGM brings the schema up to date, then two guests join beside the game master.
	if _, err := mudserver.AddGM(c, "Boss", "a long enough password"); err != nil {
		t.Fatal(err)
	}
	execSQL(t, url,
		"DELETE FROM player WHERE NOT gm",
		"INSERT INTO player (username, guest) VALUES ('Alice', true), ('Bob', true)",
		"INSERT INTO player_state (player_id, kind, key, value) SELECT id, 'xp', 'total', '5' FROM player",
	)
	return c
}

func TestResetPlayersKeepsGMsByDefault(t *testing.T) {
	t.Parallel()
	c := resetFixture(t)
	n, err := mudserver.ResetPlayers(c, false)
	if err != nil || n != 2 {
		t.Fatalf("ResetPlayers = %d, %v; want 2, nil", n, err)
	}
	if got := queryString(t, "SELECT string_agg(username, ',') FROM player"); got != "Boss" {
		t.Errorf("players left = %q, want Boss", got)
	}
	if got := queryString(t, "SELECT count(*)::text FROM player_state"); got != "1" {
		t.Errorf("player_state rows left = %s, want 1 (the GM's)", got)
	}
	if got := queryString(t, "SELECT count(*)::text FROM room"); got == "0" {
		t.Error("the world's rooms were deleted")
	}
}

func TestResetPlayersCanIncludeGMs(t *testing.T) {
	t.Parallel()
	c := resetFixture(t)
	if n, err := mudserver.ResetPlayers(c, true); err != nil || n != 3 {
		t.Fatalf("ResetPlayers = %d, %v; want 3, nil", n, err)
	}
	if got := queryString(t, "SELECT ((SELECT count(*) FROM player) + (SELECT count(*) FROM player_state))::text"); got != "0" {
		t.Errorf("rows left = %s, want 0", got)
	}
}

func TestResetPlayerDeletesOneAndFreesTheName(t *testing.T) {
	t.Parallel()
	c := resetFixture(t)
	found, err := mudserver.ResetPlayer(c, "alice") // case does not matter
	if err != nil || !found {
		t.Fatalf("ResetPlayer = %v, %v; want true, nil", found, err)
	}
	if got := queryString(t, "SELECT string_agg(username, ',' ORDER BY username) FROM player"); got != "Bob,Boss" {
		t.Errorf("players left = %q, want Bob,Boss", got)
	}
	if got := queryString(t, "SELECT count(*)::text FROM player_state"); got != "2" {
		t.Errorf("player_state rows left = %s, want 2", got)
	}

	// The name can be joined again as a new guest.
	wsURL, _ := serveTestServer(t, testDatabaseURL(t), nil)
	if _, auth := joinAs(t, wsURL, "Alice"); auth["ok"] != true {
		t.Errorf("rejoin after reset = %v", auth)
	}
}

func TestResetPlayerUnknownAndGM(t *testing.T) {
	t.Parallel()
	c := resetFixture(t)
	if found, err := mudserver.ResetPlayer(c, "Nobody"); found || err != nil {
		t.Errorf("unknown name = %v, %v; want false, nil", found, err)
	}
	if found, err := mudserver.ResetPlayer(c, "Boss"); found || err == nil {
		t.Errorf("a GM = %v, %v; want refused", found, err)
	}
	if got := queryString(t, "SELECT count(*)::text FROM player"); got != "3" {
		t.Errorf("players = %s, want 3 (nothing deleted)", got)
	}
}
