package main

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"mud_server/src/internal/mudbot"
	"mud_server/src/internal/mudserver"
)

// This is intentionally part of the ordinary suite: scripts/test.sh runs it
// under -race, against the same isolated mud_test fixture as other integration
// tests. Password hashing is fixture-only cost 4; production login cost is not
// being benchmarked. Game code and all websocket/queue/persistence paths are real.
func TestTwoHundredBotsKeepPlaying(t *testing.T) {
	t.Parallel()
	const count = 200
	ctx := context.Background()
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		conn, err := pgx.Connect(ctx, c.Database.DatabaseURL)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close(ctx)
		if err := mudbot.SeedPlayers(ctx, conn, count, "racebot", "fixture"); err != nil {
			t.Fatal(err)
		}
		// One durable NPC gives the bots a real target throughout the run,
		// without random kills changing which commands the test exercises.
	}, `UPDATE baddiespawner SET tickdurationms=100`,
		`UPDATE baddienpc_generic SET hp=1000000, combatTickerDurationInMS=500, attackpower=1, attackaccuracy=100`)
	r, err := mudbot.Run(ctx, mudbot.Config{
		URL: wsURL, Count: count, Duration: 10 * time.Second, Interval: time.Second,
		Timeout: 5 * time.Second, JoinTimeout: 90 * time.Second, LoginWorkers: 4,
		Prefix: "racebot", Password: "fixture",
	})
	t.Logf("200-bot report: %+v", r)
	if err != nil {
		t.Fatalf("bots failed: %v", err)
	}
	if r.MinBotSamples < 8 {
		t.Fatalf("not every bot completed a full action cycle: %+v", r)
	}
	if r.Moves == 0 || r.Says == 0 || r.Whispers == 0 || r.TargetedAttacks == 0 || r.Hits == 0 {
		t.Fatalf("missing workload coverage: %+v", r)
	}
	if r.GameplayTime < 10*time.Second {
		t.Fatalf("gameplay window too short: %v", r.GameplayTime)
	}
}

func TestBotSeedFixtureUsesNormalLoginData(t *testing.T) {
	t.Parallel()
	url := testDatabaseURL(t)
	resetDatabase(t, url)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if err := mudbot.SeedPlayers(ctx, conn, 2, "fixturebot", "fixture"); err != nil {
		t.Fatal(err)
	}
	var hash string
	var room int
	if err := conn.QueryRow(ctx, `SELECT passwordhash,roomid FROM player WHERE username=$1`, mudbot.Name("fixturebot", 1)).Scan(&hash, &room); err != nil {
		t.Fatal(err)
	}
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil || cost != bcrypt.MinCost || room != 1 {
		t.Fatalf("bad fixture: cost=%d room=%d error=%v", cost, room, err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("fixture")); err != nil {
		t.Fatal(err)
	}
	if err := mudbot.SeedPlayers(ctx, conn, 2, "fixturebot", "different"); err == nil {
		t.Fatal("duplicate bot names must not silently reuse credentials")
	}
}
