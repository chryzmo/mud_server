package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// A message of only spaces used to index into an empty field list in the
// reader goroutine, panicking and taking the whole server down.
func TestWhitespaceOnlyMessageIsIgnored(t *testing.T) {
	wsURL := startTestServer(t)
	conn := register(t, wsURL, "spacer")

	send(t, conn, "   ")
	send(t, conn, "\t")
	send(t, conn, "LOOK")
	expect(t, conn, "You are in 'Start Room'")

	// Before logging in, too.
	other := dial(t, wsURL)
	expect(t, other, "Welcome to the MUD!")
	send(t, other, "  ")
	send(t, other, "LOOK")
	expect(t, other, "Please log in or register first.")
}

// Two clients registering the same name at once: the unique index on
// player.username lets exactly one through.
func TestConcurrentRegistrationOfOneName(t *testing.T) {
	wsURL := startTestServer(t)

	const clients = 5
	replies := make([]string, clients)
	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		conn := dial(t, wsURL)
		expect(t, conn, "Welcome to the MUD!")
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			send(t, conn, "/REGISTER twin secret")
			msg, _ := expectWithin(conn, "", 5*time.Second)
			replies[i] = msg
		}(i)
	}
	wg.Wait()

	created := 0
	for _, r := range replies {
		switch {
		case strings.Contains(r, "Account created! Welcome, twin."):
			created++
		case strings.Contains(r, "That username is already taken."),
			strings.Contains(r, "That username is already online."):
		default:
			t.Errorf("unexpected reply %q", r)
		}
	}
	if created != 1 {
		t.Errorf("%d registrations succeeded, want exactly 1; replies: %q", created, replies)
	}

	db, err := pgx.Connect(context.Background(), testDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(context.Background())
	var rows int
	db.QueryRow(context.Background(), "SELECT count(*) FROM player WHERE username = 'twin'").Scan(&rows)
	if rows != 1 {
		t.Errorf("%d player rows named twin, want 1", rows)
	}
}

func TestLoginWhileAlreadyOnline(t *testing.T) {
	wsURL := startTestServer(t)
	register(t, wsURL, "solo")

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
