package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"testing"

	"mud_server/src/internal/worldfile"
)

// Sticker codes and cosmetics (plans/event-systems.md section 5) through a real server.

const testCodeSecret = "test-secret-not-a-real-one" // pre-commit: allow-secret (a fake HMAC key for tests)

// Fake codes for the tests only; real sticker codes are never committed.
const (
	codeGold   = "ABCD-2345"
	codeToken  = "WXYZ-6789"
	codeFlag   = "KKKK-2222"
	codeNoNews = "HHHH-3333"
)

func redeemWorld(t *testing.T) string {
	t.Helper()
	h := func(code string) string {
		n, _ := worldfile.NormalizeCode(code)
		return worldfile.HashCode(testCodeSecret, n)
	}
	return contentDir(t, map[string]string{"world.yaml": fmt.Sprintf(`
start_room: hall
items:
  brass-token: {name: brass token, description: A token.}
rooms:
  hall:
    name: Hall
    description: A hall.
codes:
  founder:
    hash: %s
    color: gold
    badge: star
    message: "A warm light finds you."
  token:
    hash: %s
    item: brass-token
  seen:
    hash: %s
    flag: saw-sticker
  quiet:
    hash: %s
    message: "Nothing else."
`, h(codeGold), h(codeToken), h(codeFlag), h(codeNoNews))})
}

func redeemServer(t *testing.T) string {
	t.Helper()
	t.Setenv("MUD_CODE_SECRET", testCodeSecret)
	wsURL, _ := startTestServerWithConfig(t, withContent(redeemWorld(t)))
	return wsURL
}

func TestRedeemGrantsCosmeticsOnceAndTheyRideEveryFrame(t *testing.T) {
	wsURL := redeemServer(t)
	alice := eventPlayer(t, wsURL, "alice")
	bob := eventPlayer(t, wsURL, "bob")

	send(t, alice, "PROGRESS")
	expect(t, alice, "Stickers found: 0 of 4.")
	send(t, alice, "REDEEM  abcd 2345 ") // case, spaces and dashes do not matter
	expect(t, alice, "A warm light finds you.")
	if c := expectMessage(t, alice, "cosmetics"); fmt.Sprint(c["colors"]) != "[gold]" || c["badge"] != "star" || c["color"] != "gold" {
		t.Errorf("cosmetics = %v", c)
	}
	send(t, alice, "REDEEM "+codeGold)
	expect(t, alice, "You have already used that code.")
	send(t, alice, "PROGRESS")
	expect(t, alice, "Stickers found: 1 of 4.")

	send(t, alice, "SAY hello")
	say := expectMessage(t, bob, "say")
	if say["who"] != "alice" || say["color"] != "gold" || say["badge"] != "star" {
		t.Errorf("say = %v", say)
	}
	send(t, alice, "COSMETIC off")
	expect(t, alice, "Cosmetics cleared.")
	send(t, alice, "SAY again")
	if say := expectMessage(t, bob, "say"); say["color"] != nil || say["badge"] != nil {
		t.Errorf("after OFF, say = %v", say)
	}
	send(t, alice, "COSMETIC gold")
	expect(t, alice, "You now wear gold.")
	send(t, alice, "COSMETIC crimson")
	expect(t, alice, "You do not have a cosmetic called 'crimson'.")
}

func TestRedeemItemFlagAndMessageRewards(t *testing.T) {
	wsURL := redeemServer(t)
	conn := eventPlayer(t, wsURL, "finder")
	send(t, conn, "REDEEM "+codeToken)
	expect(t, conn, "You receive")
	send(t, conn, "INVENTORY")
	expect(t, conn, "brass token")
	send(t, conn, "REDEEM "+codeFlag)
	expect(t, conn, "Code accepted.")
	send(t, conn, "REDEEM "+codeNoNews)
	expect(t, conn, "Nothing else.")
}

func TestARedeemedCodeStaysRedeemedAfterLoggingBackIn(t *testing.T) {
	wsURL := redeemServer(t)
	conn := eventPlayer(t, wsURL, "keeper")
	send(t, conn, "REDEEM "+codeGold)
	expect(t, conn, "You earned the star badge.")
	waitForStateRows(t, "keeper", 5) // redeemed, two unlocks, two choices: written within milliseconds
	conn.Close()

	again, _ := loginWhenFree(t, wsURL, "keeper", "secret")
	if c := expectMessage(t, again, "cosmetics"); c["color"] != "gold" || c["badge"] != "star" {
		t.Errorf("cosmetics after login = %v", c)
	}
	send(t, again, "REDEEM "+codeGold)
	expect(t, again, "You have already used that code.")
}

func TestWrongCodesAllFailAlikeThenCoolDownEvenForTheRightCode(t *testing.T) {
	wsURL := redeemServer(t)
	conn := eventPlayer(t, wsURL, "guesser")
	for _, wrong := range []string{"nonsense", "AAAA-AAAA", "!!!!", "ABCD-234", "ABCD-2346"} {
		send(t, conn, "REDEEM "+wrong)
		expect(t, conn, "That code does not work.")
	}
	send(t, conn, "REDEEM "+codeGold) // the right code, but the cooldown has started
	expect(t, conn, "Too many wrong codes. Try again in about")
	send(t, conn, "PROGRESS")
	expect(t, conn, "Stickers found: 0 of 4.")

	// Another player is not held up by it.
	other := eventPlayer(t, wsURL, "bystander")
	send(t, other, "REDEEM "+codeGold)
	expect(t, other, "A warm light finds you.")
}

func TestWithoutASecretNoCodeWorks(t *testing.T) {
	t.Setenv("MUD_CODE_SECRET", "")
	wsURL, _ := startTestServerWithConfig(t, withContent(redeemWorld(t)))
	conn := eventPlayer(t, wsURL, "hopeful")
	send(t, conn, "REDEEM "+codeGold)
	expect(t, conn, "That code does not work.")
}

func TestRedeemWithNoCodeShowsUsage(t *testing.T) {
	wsURL := redeemServer(t)
	conn := eventPlayer(t, wsURL, "blank")
	send(t, conn, "REDEEM")
	expect(t, conn, "Usage: REDEEM <code>")
	send(t, conn, "COSMETIC")
	expect(t, conn, "You have no cosmetics yet.")
}

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
