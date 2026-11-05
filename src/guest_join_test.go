package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"

	mudserver "mud_server/src/internal/mudserver"
)

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// joinAs connects and sends join, returning the connection and the auth reply.
func joinAs(t *testing.T, wsURL, name string) (*websocket.Conn, map[string]any) {
	t.Helper()
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")
	writeRaw(t, conn, fmt.Sprintf(`{"t":"join","name":%q}`, name))
	return conn, expectMessage(t, conn, "auth")
}

func TestJoinCreatesAGuestAndStoresOnlyHashes(t *testing.T) {
	wsURL := startTestServer(t)
	conn, auth := joinAs(t, wsURL, "Zed_1")
	if auth["ok"] != true || !strings.Contains(auth["message"].(string), "Welcome, Zed_1!") {
		t.Fatalf("join auth = %v", auth)
	}
	token, _ := auth["token"].(string)
	recovery, _ := auth["recovery"].(string)
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(token) {
		t.Errorf("token %q is not 43 URL-safe characters", token)
	}
	if !regexp.MustCompile(`^[A-HJ-NP-Z2-9]{4}(-[A-HJ-NP-Z2-9]{4}){2}$`).MatchString(recovery) {
		t.Errorf("recovery %q has the wrong shape", recovery)
	}

	// The character is in the world and can play.
	expect(t, conn, "You are in 'Start Room'")
	send(t, conn, "SAY hello")
	expect(t, conn, `You say: "hello"`)

	// The database holds hashes, a guest flag and no password; never the secrets.
	q := "SELECT %s FROM player WHERE username='Zed_1'"
	if got := queryString(t, fmt.Sprintf(q, "guest::text")); got != "true" {
		t.Errorf("guest = %s", got)
	}
	if got := queryString(t, fmt.Sprintf(q, "passwordhash")); got != "" {
		t.Errorf("passwordhash = %q, want empty", got)
	}
	if got := queryString(t, fmt.Sprintf(q, "resumetokenhash")); got != sha(token) {
		t.Errorf("resumetokenhash = %q, want the SHA-256 of the token", got)
	}
	if got := queryString(t, fmt.Sprintf(q, "recoveryhash")); got != sha(strings.ReplaceAll(recovery, "-", "")) {
		t.Errorf("recoveryhash = %q, want the SHA-256 of the code without dashes", got)
	}
	if n := queryString(t, fmt.Sprintf("SELECT count(*)::text FROM player WHERE resumetokenhash='%s' OR recoveryhash='%s' OR resumetokenhash='%s'", token, recovery, strings.ReplaceAll(recovery, "-", ""))); n != "0" {
		t.Errorf("a raw secret is stored in %s row(s)", n)
	}
	if got := queryString(t, fmt.Sprintf(q, "roomid::text")); got != "1" {
		t.Errorf("roomid = %s, want the start room", got)
	}
}

func TestJoinRefusesBadNamesAndCreatesNothing(t *testing.T) {
	wsURL := startTestServer(t)
	for name, reason := range map[string]string{
		"ab":                 "at least 3",
		"Seventeen_chars_xx": "at most 16",
		"two words":          "only letters",
		"café":               "only letters",
		"12345":              "at least one letter",
		"Admin":              "isn't allowed",
		"Sh1t":               "isn't allowed",
		"Big_Dick":           "isn't allowed",
	} {
		_, auth := joinAs(t, wsURL, name)
		if auth["ok"] != false || auth["code"] != "invalid_name" || !strings.Contains(auth["message"].(string), reason) {
			t.Errorf("join %q = %v, want invalid_name containing %q", name, auth, reason)
		}
		if auth["token"] != nil || auth["recovery"] != nil {
			t.Errorf("join %q leaked credentials: %v", name, auth)
		}
	}
	// The seed has one player; no refused name created a row.
	if n := queryString(t, "SELECT count(*)::text FROM player"); n != "1" {
		t.Errorf("player rows = %s, want only the seeded one", n)
	}
}

func TestJoinNamesAreUniqueIgnoringCase(t *testing.T) {
	wsURL := startTestServer(t)
	if _, auth := joinAs(t, wsURL, "Ann"); auth["ok"] != true {
		t.Fatalf("first join = %v", auth)
	}
	register(t, wsURL, "Pat") // a password account
	for _, name := range []string{"Ann", "ann", "ANN", "Pat", "PAT", "pAt"} {
		if _, auth := joinAs(t, wsURL, name); auth["code"] != "name_taken" || auth["ok"] != false {
			t.Errorf("join %q = %v, want name_taken", name, auth)
		}
	}
	// Password registration obeys the same rule.
	reg := dial(t, wsURL)
	expect(t, reg, "Welcome to the MUD!")
	writeRaw(t, reg, `{"t":"register","username":"ANN","password":"secret"}`)
	if auth := expectMessage(t, reg, "auth"); auth["ok"] != false {
		t.Errorf("registering ANN after guest Ann = %v, want a refusal", auth)
	}
	if n := queryString(t, "SELECT count(*)::text FROM player WHERE lower(username)='ann'"); n != "1" {
		t.Errorf("rows named ann = %s, want 1", n)
	}
}

func TestConcurrentJoinsOfOneNameHaveOneWinner(t *testing.T) {
	wsURL := startTestServer(t)
	const racers = 12
	var wg sync.WaitGroup
	var mu sync.Mutex
	won, taken := 0, 0
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conn, _, err := dialer.Dial(wsURL, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			name := "Racer"
			if i%2 == 1 {
				name = "RACER" // differs only in case
			}
			conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"t":"join","name":%q}`, name)))
			for {
				_, raw, err := conn.ReadMessage()
				if err != nil {
					return
				}
				s := string(raw)
				if strings.Contains(s, `"t":"auth"`) {
					mu.Lock()
					defer mu.Unlock()
					if strings.Contains(s, `"ok":true`) {
						won++
					} else if strings.Contains(s, `"code":"name_taken"`) {
						taken++
					}
					return
				}
			}
		}(i)
	}
	wg.Wait()
	if won != 1 || taken != racers-1 {
		t.Errorf("won=%d taken=%d, want exactly one winner and %d name_taken", won, taken, racers-1)
	}
	if n := queryString(t, "SELECT count(*)::text FROM player WHERE lower(username)='racer'"); n != "1" {
		t.Errorf("rows named racer = %s, want 1", n)
	}
}

func TestJoinWhileAlreadyInTheGame(t *testing.T) {
	wsURL := startTestServer(t)
	conn, _ := joinAs(t, wsURL, "Once")
	writeRaw(t, conn, `{"t":"join","name":"Twice"}`)
	if data := expectMessage(t, conn, "error"); data["code"] != "already_logged_in" {
		t.Errorf("second join = %v", data)
	}
	if n := queryString(t, "SELECT count(*)::text FROM player WHERE username='Twice'"); n != "0" {
		t.Errorf("Twice has %s rows", n)
	}
	// And a join with no name is a bad request, not a crash.
	other := dial(t, wsURL)
	expect(t, other, "Welcome to the MUD!")
	writeRaw(t, other, `{"t":"join"}`)
	if auth := expectMessage(t, other, "auth"); auth["code"] != "bad_request" {
		t.Errorf("nameless join = %v", auth)
	}
}

func TestJoinAtCapacityCreatesNoRow(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, crowdConfig(1, 1))
	if _, auth := joinAs(t, wsURL, "First"); auth["ok"] != true {
		t.Fatalf("first join = %v", auth)
	}
	_, auth := joinAs(t, wsURL, "Second")
	if auth["ok"] != false || auth["code"] != "at_capacity" || auth["token"] != nil {
		t.Errorf("join at capacity = %v", auth)
	}
	if n := queryString(t, "SELECT count(*)::text FROM player WHERE username='Second'"); n != "0" {
		t.Errorf("a refused guest left %s row(s)", n)
	}
}

// A database created before guest sessions gets its columns and indexes at startup.
func TestSchemaUpgradeAddsGuestColumns(t *testing.T) {
	url := testDatabaseURL(t)
	resetDatabase(t, url)
	execSQL(t, url,
		"DROP INDEX IF EXISTS player_resumetoken_idx",
		"DROP INDEX IF EXISTS player_username_lower_key",
		"ALTER TABLE player DROP COLUMN guest, DROP COLUMN resumetokenhash, DROP COLUMN recoveryhash, DROP COLUMN lastseen",
	)

	c := mudserver.Configuration{}
	c.Database.DatabaseURL = url
	c.Game.RoomTickDurationInMS = 250
	c.Game.PlayerStartHP, c.Game.PlayerStartMaxHP = 20, 20
	c.Game.PlayerStartMP, c.Game.PlayerStartMaxMP = 10, 10
	c.Game.PlayerStartAttack, c.Game.PlayerStartDefense = 5, 10
	si, err := mudserver.NewMudServer(c) // EnsureSchema runs here
	if err != nil {
		t.Fatal(err)
	}
	defer si.Shutdown(t.Context())

	for _, col := range []string{"guest", "resumetokenhash", "recoveryhash", "lastseen"} {
		if n := queryString(t, fmt.Sprintf("SELECT count(*)::text FROM information_schema.columns WHERE table_name='player' AND column_name='%s'", col)); n != "1" {
			t.Errorf("column %s missing after startup", col)
		}
	}
	if n := queryString(t, "SELECT count(*)::text FROM pg_indexes WHERE indexname IN ('player_resumetoken_idx','player_username_lower_key')"); n != "2" {
		t.Errorf("indexes after upgrade = %s, want 2", n)
	}
}
