package mudserver

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	muddatabase "mud_server/src/internal/database"
)

/*
A player's puzzle state (plans/interactive-world.md section 7): flags, counters, sequences
being built, and the gates opened for them. It is the player's own, so one player's solving
never spoils a puzzle for another. It lives on the player in memory, is loaded with the player,
and every change is queued to a writer and written within milliseconds (not on the 5 s save
batch), so a solved puzzle survives a crash. The game loop only enqueues; it never touches the
database.
*/

var placeholderRE = regexp.MustCompile(`\{[^{}]*\}`)

type playerState struct {
	flags     map[string]bool
	counters  map[string]int
	sequences map[string][]string
	gates     map[string]bool // "room-key/gate-key" opened for this player
	visited   map[string]bool // content keys of the rooms this player has been in
	hints     map[string]int  // puzzle key -> the hint tier last shown
}

func newPlayerState() *playerState {
	return &playerState{
		flags: map[string]bool{}, counters: map[string]int{},
		sequences: map[string][]string{}, gates: map[string]bool{},
		visited: map[string]bool{}, hints: map[string]int{},
	}
}

// stateWrite is one change to queue: an upsert, or a delete.
type stateWrite struct {
	playerID int32
	kind     string // flag, counter, sequence, gate
	key      string
	value    string
	remove   bool
}

const stateQueueSize = 4096

// st returns the player's state, creating it on first use. Loop only.
func (mp *MudPlayer) st() *playerState {
	if mp.state == nil {
		mp.state = newPlayerState()
	}
	return mp.state
}

func (mp *MudPlayer) persistState(kind, key, value string, remove bool) {
	if mp.stateSink != nil && mp.ID != 0 {
		mp.stateSink(stateWrite{playerID: mp.ID, kind: kind, key: key, value: value, remove: remove})
	}
}

func (mp *MudPlayer) flag(key string) bool { return mp.st().flags[key] }

func (mp *MudPlayer) setFlag(key string) {
	if !mp.st().flags[key] {
		mp.st().flags[key] = true
		mp.persistState("flag", key, "1", false)
		if mp.world != nil && mp.world.guide != nil {
			for _, puzzle := range mp.world.guide.byFlag[key] {
				if p := mp.world.guide.puzzle(puzzle); p != nil && !mp.flag(p.Solved.Flag) {
					mp.lastPuzzle = puzzle // HINT with no name means the one worked on last
					break
				}
			}
		}
	}
}

func (mp *MudPlayer) clearFlag(key string) {
	if mp.st().flags[key] {
		delete(mp.st().flags, key)
		mp.persistState("flag", key, "", true)
	}
}

func (mp *MudPlayer) counter(key string) int { return mp.st().counters[key] }

func (mp *MudPlayer) setCounter(key string, v int) {
	mp.st().counters[key] = v
	mp.persistState("counter", key, strconv.Itoa(v), false)
}

func (mp *MudPlayer) sequence(key string) []string { return mp.st().sequences[key] }

func (mp *MudPlayer) appendSequence(key, value string) {
	seq := append(mp.st().sequences[key], value)
	if len(seq) > 12 { // a puzzle sequence is short; never grow without bound
		seq = seq[len(seq)-12:]
	}
	mp.st().sequences[key] = seq
	b, _ := json.Marshal(seq)
	mp.persistState("sequence", key, string(b), false)
}

func (mp *MudPlayer) resetSequence(key string) {
	if _, ok := mp.st().sequences[key]; ok {
		delete(mp.st().sequences, key)
		mp.persistState("sequence", key, "", true)
	}
}

func (mp *MudPlayer) gateOpen(id string) bool { return mp.st().gates[id] }

func (mp *MudPlayer) setGate(id string, open bool) {
	if mp.st().gates[id] == open {
		return
	}
	if open {
		mp.st().gates[id] = true
		mp.persistState("gate", id, "1", false)
	} else {
		delete(mp.st().gates, id)
		mp.persistState("gate", id, "", true)
	}
}

// loadState reads a player's saved puzzle state. It runs with the rest of LoadFromDB, on a reader goroutine.
func (mp *MudPlayer) loadState(db *muddatabase.MudDatabase) {
	rows, err := db.Connection.Query(db.Context, `SELECT kind, key, value FROM player_state WHERE player_id=$1`, mp.ID)
	if err != nil {
		fmt.Println("loadState error: " + err.Error())
		return
	}
	type row struct{ Kind, Key, Value string }
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		fmt.Println("loadState rows error: " + err.Error())
		return
	}
	state := newPlayerState()
	for _, r := range list {
		switch r.Kind {
		case "flag":
			state.flags[r.Key] = true
		case "counter":
			if n, err := strconv.Atoi(r.Value); err == nil {
				state.counters[r.Key] = n
			}
		case "sequence":
			var seq []string
			if json.Unmarshal([]byte(r.Value), &seq) == nil {
				state.sequences[r.Key] = seq
			}
		case "gate":
			state.gates[r.Key] = true
		case "visited":
			state.visited[r.Key] = true
		case "hint":
			if n, err := strconv.Atoi(r.Value); err == nil {
				state.hints[r.Key] = n
			}
		}
	}
	mp.state = state
}

// queueState is the player's sink: it hands a change to the writer without ever blocking the loop.
func (ms *MudServer) queueState(w stateWrite) {
	defer func() { _ = recover() }() // a send after shutdown closed the queue is simply lost
	select {
	case ms.stateQ <- w:
	default:
		fmt.Printf("player state queue full; dropped %s %s for player %d\n", w.kind, w.key, w.playerID)
	}
}

// stateWriter writes queued changes in order until the queue is closed.
func (ms *MudServer) stateWriter() {
	defer close(ms.stateDone)
	for w := range ms.stateQ {
		var err error
		switch {
		case w.kind == "world":
			if w.remove {
				_, err = ms.DB.Connection.Exec(ms.DB.Context, `DELETE FROM world_flag WHERE key=$1`, w.key)
			} else {
				_, err = ms.DB.Connection.Exec(ms.DB.Context, `INSERT INTO world_flag (key) VALUES ($1) ON CONFLICT DO NOTHING`, w.key)
			}
		case w.kind == "event":
			err = ms.writeEvent(w.key, w.value)
		case w.remove:
			_, err = ms.DB.Connection.Exec(ms.DB.Context, `DELETE FROM player_state WHERE player_id=$1 AND kind=$2 AND key=$3`, w.playerID, w.kind, w.key)
		default:
			_, err = ms.DB.Connection.Exec(ms.DB.Context,
				`INSERT INTO player_state (player_id, kind, key, value) VALUES ($1, $2, $3, $4)
				 ON CONFLICT (player_id, kind, key) DO UPDATE SET value = EXCLUDED.value`, w.playerID, w.kind, w.key, w.value)
		}
		if err != nil {
			fmt.Printf("player_state write error (%s %s): %v\n", w.kind, w.key, err)
		}
	}
}

// fill replaces {player} and {counter} in authored text.
func (mp *MudPlayer) fill(text string) string {
	if !strings.Contains(text, "{") {
		return text
	}
	return placeholderRE.ReplaceAllStringFunc(text, func(m string) string {
		name := m[1 : len(m)-1]
		if name == "player" {
			return mp.Username
		}
		if n, ok := mp.st().counters[name]; ok {
			return strconv.Itoa(n)
		}
		return "0" // a counter the player has not touched yet reads as zero
	})
}
