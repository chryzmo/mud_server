package main

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// A message of only spaces used to index into an empty field list in the
// reader goroutine, panicking and taking the whole server down.
func TestWhitespaceOnlyMessageIsIgnored(t *testing.T) {
	wsURL := startTestServer(t)
	conn := passwordPlayer(t, wsURL, "spacer")

	send(t, conn, "   ")
	send(t, conn, "\t")
	send(t, conn, "LOOK")
	expect(t, conn, "You are in 'Start Room'")

	// Before logging in, too.
	other := dial(t, wsURL)
	expect(t, other, "Welcome to the MUD!")
	send(t, other, "  ")
	send(t, other, "LOOK")
	expect(t, other, "Please join first.")
}

func TestLoginWhileAlreadyOnline(t *testing.T) {
	wsURL := startTestServer(t)
	passwordPlayer(t, wsURL, "solo")

	second := dial(t, wsURL)
	expect(t, second, "Welcome to the MUD!")
	send(t, second, "/LOGIN solo secret")
	expect(t, second, "That user is already logged in.")
}

// Databases created before the unique index existed (production's, for one)
// get it when the server starts.
func TestStartupAddsUsernameIndex(t *testing.T) {
	startTestServerWith(t, `ALTER TABLE player DROP CONSTRAINT player_username_key`)

	db, err := pgx.Connect(context.Background(), testDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(context.Background())
	var indexes int
	db.QueryRow(context.Background(),
		"SELECT count(*) FROM pg_indexes WHERE tablename = 'player' AND indexname = 'player_username_key'").Scan(&indexes)
	if indexes != 1 {
		t.Errorf("player_username_key indexes = %d, want 1", indexes)
	}
}
