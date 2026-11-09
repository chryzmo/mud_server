package mudserver

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/term"

	muddatabase "mud_server/src/internal/database"
)

// Resetting characters for testing. These run on the host against the database, like -add-gm, and the
// game server must be stopped first: a running server keeps characters in memory and would save them back.
// Only characters are removed (the player row and its player_state progress); the world, items, world flags,
// event progress, reports and the GM audit log stay.

func openResetDatabase(c Configuration) (muddatabase.MudDatabase, error) {
	db := muddatabase.CreateDatabase(c.DatabaseConnectionURL(), 2)
	if err := db.WaitUntilReachable(15 * time.Second); err != nil {
		db.Connection.Close()
		return muddatabase.MudDatabase{}, errors.New("database unreachable: " + err.Error())
	}
	if err := db.EnsureSchema(); err != nil {
		db.Connection.Close()
		return muddatabase.MudDatabase{}, errors.New("schema update failed: " + err.Error())
	}
	return db, nil
}

// ResetPlayers deletes every character and its saved progress, and returns how many characters went.
// Game masters are kept unless includeGMs is set, so the owner's account survives a reset.
func ResetPlayers(c Configuration, includeGMs bool) (int64, error) {
	db, err := openResetDatabase(c)
	if err != nil {
		return 0, err
	}
	defer db.Connection.Close()
	tx, err := db.Connection.Begin(db.Context)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(db.Context)
	if _, err := tx.Exec(db.Context, `DELETE FROM player_state WHERE player_id IN (SELECT id FROM player WHERE $1 OR NOT gm)`, includeGMs); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(db.Context, `DELETE FROM player WHERE $1 OR NOT gm`, includeGMs)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), tx.Commit(db.Context)
}

// ResetPlayer deletes one character (matched without regard to case) and its saved progress, so the name
// can be joined again. It reports whether there was one. A game master is refused: use ResetPlayers with
// includeGMs for that, so a stray command cannot take away the owner's account.
func ResetPlayer(c Configuration, name string) (bool, error) {
	db, err := openResetDatabase(c)
	if err != nil {
		return false, err
	}
	defer db.Connection.Close()
	tx, err := db.Connection.Begin(db.Context)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(db.Context)
	var id int32
	var gm bool
	err = tx.QueryRow(db.Context, `SELECT id, gm FROM player WHERE lower(username)=lower($1)`, strings.TrimSpace(name)).Scan(&id, &gm)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if gm {
		return false, errors.New("that is a game master account; use -reset-players -include-gms to remove it")
	}
	if _, err := tx.Exec(db.Context, `DELETE FROM player_state WHERE player_id=$1`, id); err != nil {
		return false, err
	}
	if _, err := tx.Exec(db.Context, `DELETE FROM player WHERE id=$1`, id); err != nil {
		return false, err
	}
	return true, tx.Commit(db.Context)
}

// databaseName is the database a connection URL points at ("" if it cannot be read).
func databaseName(connURL string) string {
	u, err := url.Parse(connURL)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Path, "/")
}

// databaseLabel names the database and host a connection URL points at, without its password.
func databaseLabel(connURL string) string {
	u, err := url.Parse(connURL)
	if err != nil || u.Host == "" {
		return "the configured database"
	}
	return databaseName(connURL) + " on " + u.Host
}

// confirmReset shows what is about to go and asks for the database name to be typed. Without a terminal
// there is nobody to ask, so -yes is required.
func confirmReset(stdin *os.File, stdout io.Writer, target string, connURL string, yes bool) bool {
	fmt.Fprintf(stdout, "About to delete %s from %s.\nThe game server must be stopped. This cannot be undone.\n", target, databaseLabel(connURL))
	if yes {
		return true
	}
	if !term.IsTerminal(int(stdin.Fd())) {
		fmt.Fprintln(stdout, "Not a terminal: pass -yes to confirm.")
		return false
	}
	want := databaseName(connURL)
	if want == "" {
		fmt.Fprintln(stdout, "Cannot read the database name from the connection URL: pass -yes to confirm.")
		return false
	}
	fmt.Fprintf(stdout, "Type the database name (%s) to continue: ", want)
	line, _ := bufio.NewReader(stdin).ReadString('\n')
	return strings.TrimSpace(line) == want
}

// RunResetPlayers is `mud_server -reset-players [-include-gms] [-yes]`.
func RunResetPlayers(args []string, stdin *os.File, stdout, stderr io.Writer) int {
	includeGMs, yes := false, false
	for _, a := range args {
		switch a {
		case "-include-gms":
			includeGMs = true
		case "-yes":
			yes = true
		default:
			fmt.Fprintln(stderr, "usage: mud_server -reset-players [-include-gms] [-yes]")
			return 2
		}
	}
	c := loadConfiguration()
	target := "every character and its progress (game masters are kept)"
	if includeGMs {
		target = "every character and its progress, game masters included"
	}
	if !confirmReset(stdin, stdout, target, c.DatabaseConnectionURL(), yes) {
		fmt.Fprintln(stderr, "reset-players: cancelled, nothing deleted")
		return 1
	}
	n, err := ResetPlayers(c, includeGMs)
	if err != nil {
		fmt.Fprintln(stderr, "reset-players:", err)
		return 1
	}
	fmt.Fprintf(stdout, "Deleted %d character(s).\n", n)
	return 0
}

// RunResetPlayer is `mud_server -reset-player NAME [-yes]`.
func RunResetPlayer(args []string, stdin *os.File, stdout, stderr io.Writer) int {
	var name string
	yes := false
	for _, a := range args {
		switch {
		case a == "-yes":
			yes = true
		case strings.HasPrefix(a, "-") || name != "":
			fmt.Fprintln(stderr, "usage: mud_server -reset-player NAME [-yes]")
			return 2
		default:
			name = a
		}
	}
	if name == "" {
		fmt.Fprintln(stderr, "usage: mud_server -reset-player NAME [-yes]")
		return 2
	}
	c := loadConfiguration()
	if !confirmReset(stdin, stdout, fmt.Sprintf("the character %q and its progress", name), c.DatabaseConnectionURL(), yes) {
		fmt.Fprintln(stderr, "reset-player: cancelled, nothing deleted")
		return 1
	}
	found, err := ResetPlayer(c, name)
	if err != nil {
		fmt.Fprintln(stderr, "reset-player:", err)
		return 1
	}
	if !found {
		fmt.Fprintf(stdout, "No character named %q.\n", name)
		return 1
	}
	fmt.Fprintf(stdout, "Deleted %q; the name can be joined again.\n", name)
	return 0
}
