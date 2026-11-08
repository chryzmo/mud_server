package main

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// waitForStateValue waits until one of the player's saved state rows holds want. Use it instead of a row count
// when the row already exists and is about to change.
func waitForStateValue(t *testing.T, name, kind, key, want string) {
	t.Helper()
	ctx := context.Background()
	db, err := pgx.Connect(ctx, testDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	var got string
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		got = ""
		db.QueryRow(ctx, `SELECT s.value FROM player_state s JOIN player p ON p.id=s.player_id WHERE p.username=$1 AND s.kind=$2 AND s.key=$3`, name, kind, key).Scan(&got)
		if got == want {
			return
		}
	}
	t.Fatalf("%s's %s/%s is %q, never %q", name, kind, key, got, want)
}

// waitForStateRows waits until the player has n saved state rows. State is written by its own
// goroutine a moment after the command, so a test that logs straight back in must wait for it.
func waitForStateRows(t *testing.T, name string, n int) {
	t.Helper()
	ctx := context.Background()
	db, err := pgx.Connect(ctx, testDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		var got int
		db.QueryRow(ctx, `SELECT count(*) FROM player_state s JOIN player p ON p.id=s.player_id WHERE p.username=$1`, name).Scan(&got)
		if got >= n {
			return
		}
	}
	t.Fatalf("%s never had %d state rows", name, n)
}
