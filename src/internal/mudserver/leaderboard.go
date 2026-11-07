package mudserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"mud_server/src/internal/worldfile"
)

/*
The leaderboard (plans/event-systems.md section 6). It is a published snapshot: a goroutine away from
the game loop reads everyone's XP from the database every few seconds, ranks it, and stores immutable
JSON behind an atomic pointer. GET /api/leaderboard serves those bytes from memory, so any number of
viewers cost the loop and the database nothing. Game masters, and players a GM hid, are left out, and
only public things appear: name, level, XP and the cosmetic keys.
*/

const (
	boardInterval = 10 * time.Second
	boardTop      = 50 // entries in each tab
)

type boardEntry struct {
	Name    string `json:"name"`
	Level   int    `json:"level"`
	XP      int    `json:"xp"`
	Explore int    `json:"explore"`
	Combat  int    `json:"combat"`
	Color   string `json:"color,omitempty"`
	Badge   string `json:"badge,omitempty"`
}

type boardJSON struct {
	Updated   string       `json:"updated"`
	Total     int          `json:"total"` // everyone ranked, not only the top of each tab
	Overall   []boardEntry `json:"overall"`
	Explorers []boardEntry `json:"explorers"`
	Fighters  []boardEntry `json:"fighters"`
}

// board is one published snapshot. Nothing in it changes after it is stored.
type board struct {
	json  []byte
	ranks map[string]int // lower-case name -> place on the overall list
	total int
}

// leaderboardQuery sums each player's XP and picks their cosmetics. The partial index it names
// (see EnsureSchema) holds only the rows this reads. Players with no XP are not ranked.
const leaderboardQuery = `
SELECT p.username,
       COALESCE(sum(CASE WHEN s.kind='xp' AND s.key='explore' THEN s.value::bigint END), 0),
       COALESCE(sum(CASE WHEN s.kind='xp' AND s.key='combat' THEN s.value::bigint END), 0),
       COALESCE(max(CASE WHEN s.kind='cosmetic' AND s.key='color' THEN s.value END), ''),
       COALESCE(max(CASE WHEN s.kind='cosmetic' AND s.key='badge' THEN s.value END), '')
FROM player p JOIN player_state s ON s.player_id = p.id
WHERE s.kind IN ('xp', 'cosmetic', 'hidden') AND NOT p.gm
GROUP BY p.id, p.username
HAVING NOT bool_or(s.kind='hidden' AND s.key='board')
   AND bool_or(s.kind='xp' AND s.key IN ('explore', 'combat'))`

type boardRow struct {
	Name           string
	Explore, Fight int64
	Color, Badge   string
}

// buildBoard ranks rows into a snapshot. Pure, so it is tested without a database.
func buildBoard(rows []boardRow, prog *worldfile.Progression, now time.Time) *board {
	entries := make([]boardEntry, 0, len(rows))
	for _, r := range rows {
		total := int(r.Explore + r.Fight)
		e := boardEntry{Name: r.Name, Level: levelFor(prog, total), XP: total, Explore: int(r.Explore), Combat: int(r.Fight)}
		if contains(worldfile.Colors, r.Color) {
			e.Color = r.Color
		}
		if contains(worldfile.Badges, r.Badge) {
			e.Badge = r.Badge
		}
		entries = append(entries, e)
	}
	byName := func(a, b boardEntry) bool { return strings.ToLower(a.Name) < strings.ToLower(b.Name) }
	rank := func(key func(boardEntry) int) []boardEntry {
		out := make([]boardEntry, 0, len(entries))
		for _, e := range entries {
			if key(e) > 0 {
				out = append(out, e)
			}
		}
		sort.SliceStable(out, func(i, j int) bool {
			if a, b := key(out[i]), key(out[j]); a != b {
				return a > b
			}
			return byName(out[i], out[j])
		})
		return out
	}
	overall := rank(func(e boardEntry) int { return e.XP })
	ranks := make(map[string]int, len(overall))
	for i, e := range overall {
		ranks[strings.ToLower(e.Name)] = i + 1
	}
	top := func(l []boardEntry) []boardEntry {
		if len(l) > boardTop {
			l = l[:boardTop]
		}
		if l == nil {
			l = []boardEntry{}
		}
		return l
	}
	b, _ := json.Marshal(boardJSON{
		Updated: now.UTC().Format(time.RFC3339), Total: len(overall), Overall: top(overall),
		Explorers: top(rank(func(e boardEntry) int { return e.Explore })),
		Fighters:  top(rank(func(e boardEntry) int { return e.Combat })),
	})
	return &board{json: b, ranks: ranks, total: len(overall)}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// refreshBoard reads the database and publishes a new snapshot. It runs off the loop.
func (ms *MudServer) refreshBoard() error {
	rows, err := ms.DB.Connection.Query(ms.DB.Context, leaderboardQuery)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[boardRow])
	if err != nil {
		return err
	}
	ms.board.Store(buildBoard(list, &ms.World.prog, time.Now()))
	return nil
}

// RefreshLeaderboard publishes a fresh snapshot now (the loop does it every few seconds; tests call it).
func (ms *MudServer) RefreshLeaderboard() error { return ms.refreshBoard() }

// boardLoop keeps the snapshot fresh until the server stops.
func (ms *MudServer) boardLoop(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := ms.refreshBoard(); err != nil {
			select {
			case <-ms.done:
				return
			default:
				fmt.Println("leaderboard refresh error: " + err.Error())
			}
		}
		select {
		case <-ms.done:
			return
		case <-t.C:
		}
	}
}

var emptyBoard = []byte(`{"updated":"","total":0,"overall":[],"explorers":[],"fighters":[]}`)

// LeaderboardHandler serves GET /api/leaderboard from the latest snapshot.
func (ms *MudServer) LeaderboardHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body := emptyBoard
	if b := ms.board.Load(); b != nil {
		body = b.json
	}
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "public, max-age=10")
	w.Write(body)
}

// setBoardHidden is GM HIDE and GM UNHIDE: keep a name off the leaderboard (works for anyone who ever
// played, online or not). The answer comes back to the GM when the write is done.
func (ms *MudServer) setBoardHidden(gm *MudPlayer, name string, hide bool) {
	ms.offLoop(func() {
		var n int64
		var err error
		if hide {
			n, err = execRows(ms, `INSERT INTO player_state (player_id, kind, key, value) SELECT id, 'hidden', 'board', '1' FROM player WHERE lower(username)=lower($1) ON CONFLICT (player_id, kind, key) DO NOTHING`, name)
		} else {
			n, err = execRows(ms, `DELETE FROM player_state WHERE kind='hidden' AND key='board' AND player_id IN (SELECT id FROM player WHERE lower(username)=lower($1))`, name)
		}
		ms.enqueue(func() {
			switch {
			case err != nil:
				gm.SendMessage("That did not work: " + err.Error())
			case n == 0 && hide:
				gm.SendMessage("No player called " + name + " (or already hidden).")
			case n == 0:
				gm.SendMessage("No player called " + name + " was hidden.")
			case hide:
				gm.SendMessage(name + " is hidden from the leaderboard within a few seconds.")
			default:
				gm.SendMessage(name + " is back on the leaderboard within a few seconds.")
			}
		})
	})
}

func execRows(ms *MudServer, sql string, args ...any) (int64, error) {
	tag, err := ms.DB.Connection.Exec(ms.DB.Context, sql, args...)
	return tag.RowsAffected(), err
}
