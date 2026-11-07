package mudserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	muddatabase "mud_server/src/internal/database"
	"mud_server/src/internal/worldfile"
)

/*
World content (plans/event-systems.md). The world is described in YAML files (see
internal/worldfile). At startup, when World.ContentDir is set, the server validates
them, applies them to the database, and builds the world from the rooms they define.

Applying is an upsert by stable key, in one transaction:
  - A row with the key is updated in place; its id never changes, so players' saved
    locations and inventories keep pointing at it.
  - The first time, a row with no key but the same name is adopted (it gets the key).
    That is how a database seeded from src/sql, or a world that predates content
    files, is taken over without moving anyone.
  - Anything else is created.
  - Only static fields are written (names, descriptions, exits, stats, spawner settings).
    A room's items lie on the floor from the moment it is created and are never reset
    by a later deploy, so what players dropped stays.
  - Spawners in rooms the content defines follow the content exactly; the others are
    left alone.
  - Nothing is deleted except those spawners. A room, item or NPC that disappears from
    the content simply stops being part of the world; players saved in such a room are
    placed at the start room.
*/

// npcBehaviorIDs maps a content behavior name to the turn function ID stored in the database.
var npcBehaviorIDs = map[string]int32{"orc": 1}

// contentResult is what applying content tells the world builder.
type contentResult struct {
	startRoomID int32
	areas       []AreaConfig // by room ID, for crowd control
	guide       *guide       // the puzzles and regions, for JOURNAL, HINT and MAP
	events      []*worldfile.ResolvedEvent
	codes       map[string]worldfile.Code // sticker codes, by key
	progression worldfile.Progression
	onboarding  *worldfile.Onboarding // the guided first steps; nil turns them off
	ambient     map[string]*roomAmbient // by room key (ambient.go)
	crowd       map[string]roomCrowd    // capacity settings by room key (stage.go); rooms with none are left out
	rooms       int
	created     int // rows created in this run, of every kind
}

// loadContent validates the files in dir and applies them. Any validation error
// stops startup: serving a broken world is worse than not starting.
func loadContent(db *muddatabase.MudDatabase, dir string, log func(string)) (*contentResult, error) {
	w, res, err := worldfile.ValidateDir(dir)
	if err != nil {
		return nil, fmt.Errorf("world content: %w", err)
	}
	for _, p := range res.Warnings {
		log("world content warning: " + p.String())
	}
	if len(res.Errors) > 0 {
		for _, p := range res.Errors {
			log("world content error: " + p.String())
		}
		return nil, fmt.Errorf("world content in %s has %d errors; not starting", dir, len(res.Errors))
	}
	out, err := applyContent(db, w)
	if err != nil {
		return nil, fmt.Errorf("applying world content: %w", err)
	}
	out.guide = newGuide(w.Puzzles, w.Rooms)
	out.events = w.ResolveEvents()
	out.codes = w.Codes
	out.progression = w.ResolvedProgression()
	out.ambient = buildAmbient(w)
	out.crowd = map[string]roomCrowd{}
	for key, r := range w.Rooms {
		if r.Capacity > 0 || r.QuietAt > 0 {
			out.crowd[key] = r.ResolvedCrowd()
		}
	}
	out.onboarding = w.ResolvedOnboarding()
	log(fmt.Sprintf("World content applied from %s: %d rooms, %d items, %d npcs (%d rows created)", dir, len(w.Rooms), len(w.Items), len(w.NPCs), out.created))
	return out, nil
}

func applyContent(db *muddatabase.MudDatabase, w *worldfile.World) (*contentResult, error) {
	ctx := db.Context
	tx, err := db.Connection.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	out := &contentResult{}
	a := applier{ctx: ctx, tx: tx, out: out}

	itemIDs := map[string]int32{}
	for _, key := range sortedKeys(w.Items) {
		it := w.Items[key]
		id, err := a.ensure("item", "name", key, it.Name,
			`INSERT INTO item (name, description, persistent, quantity, contentkey) VALUES ($1, $2, $3, 1, $4) RETURNING id`,
			it.Name, it.Description, it.Persistent, key)
		if err != nil {
			return nil, fmt.Errorf("item %s: %w", key, err)
		}
		itemIDs[key] = id
		if _, err := tx.Exec(ctx, `UPDATE item SET name=$2, description=$3, persistent=$4, bound=$5 WHERE id=$1`, id, it.Name, it.Description, it.Persistent, it.Bound); err != nil {
			return nil, fmt.Errorf("item %s: %w", key, err)
		}
		if e := it.Equipment; e != nil {
			var eqID int32
			err := tx.QueryRow(ctx, `SELECT id FROM equipment WHERE itemid=$1 ORDER BY id LIMIT 1`, id).Scan(&eqID)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				_, err = tx.Exec(ctx, `INSERT INTO equipment (itemid, slot, bonushp, bonusmaxhp, bonusmp, bonusmaxmp, bonusattackpower, bonusdefense) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
					id, e.Slot, e.HP, e.MaxHP, e.MP, e.MaxMP, e.Attack, e.Defense)
				out.created++
			case err == nil:
				_, err = tx.Exec(ctx, `UPDATE equipment SET slot=$2, bonushp=$3, bonusmaxhp=$4, bonusmp=$5, bonusmaxmp=$6, bonusattackpower=$7, bonusdefense=$8 WHERE id=$1`,
					eqID, e.Slot, e.HP, e.MaxHP, e.MP, e.MaxMP, e.Attack, e.Defense)
			}
			if err != nil {
				return nil, fmt.Errorf("item %s equipment: %w", key, err)
			}
		}
	}

	npcIDs := map[string]int32{}
	for _, key := range sortedKeys(w.NPCs) {
		n := w.NPCs[key]
		id, err := a.ensure("baddienpc_generic", "race", key, n.Race,
			`INSERT INTO baddienpc_generic (race, hp, contentkey) VALUES ($1, $2, $3) RETURNING id`, n.Race, n.HP, key)
		if err != nil {
			return nil, fmt.Errorf("npc %s: %w", key, err)
		}
		npcIDs[key] = id
		_, err = tx.Exec(ctx, `UPDATE baddienpc_generic SET race=$2, attackaccuracy=$3, attackpower=$4, defense=$5, hp=$6, mp=$7,
			combattickerdurationinms=$8, turnactionfunctionid=$9, lootgoldmin=$10, lootgoldmax=$11 WHERE id=$1`,
			id, n.Race, n.Accuracy, n.Attack, n.Defense, n.HP, n.MP, n.CombatTickMS, npcBehaviorIDs[n.Behavior], n.LootGoldMin, n.LootGoldMax)
		if err != nil {
			return nil, fmt.Errorf("npc %s: %w", key, err)
		}
	}

	// Rooms in two passes: every room needs an ID before exits can name it.
	roomIDs := map[string]int32{}
	roomKeys := sortedKeys(w.Rooms)
	for _, key := range roomKeys {
		r := w.Rooms[key]
		floor := map[string]int32{}
		for ik, qty := range r.Items {
			floor[fmt.Sprintf("i%d", itemIDs[ik])] = int32(qty)
		}
		floorJSON, _ := json.Marshal(floor)
		id, err := a.ensure("room", "name", key, r.Name,
			`INSERT INTO room (name, description, exitjson, inventoryidslice, contentkey) VALUES ($1, $2, '{}', $3, $4) RETURNING id`,
			r.Name, strings.TrimSpace(r.Description), string(floorJSON), key)
		if err != nil {
			return nil, fmt.Errorf("room %s: %w", key, err)
		}
		roomIDs[key] = id
	}
	for _, key := range roomKeys {
		r := w.Rooms[key]
		exits := map[string]int32{}
		for dir, to := range r.Exits {
			exits[dir] = roomIDs[to]
		}
		exitJSON, _ := json.Marshal(exits)
		features := make([]Feature, 0, len(r.Features))
		for _, fk := range sortedKeys(r.Features) {
			ft := r.Features[fk]
			features = append(features, Feature{Key: fk, Names: ft.Names, Examine: strings.TrimSpace(ft.Examine), Unlisted: ft.Unlisted})
		}
		featuresJSON, _ := json.Marshal(features)
		people := make([]Person, 0, len(r.People))
		for _, pk := range sortedKeys(r.People) {
			pe := r.People[pk]
			person := Person{Key: pk, Names: pe.Names, Examine: strings.TrimSpace(pe.Examine), Greeting: strings.TrimSpace(pe.Greeting), Shrug: strings.TrimSpace(pe.Shrug)}
			for _, tk := range sortedKeys(pe.Topics) {
				t := pe.Topics[tk]
				person.Topics = append(person.Topics, Topic{Key: tk, Keywords: t.Keywords, Say: strings.TrimSpace(t.Say)})
			}
			people = append(people, person)
		}
		peopleJSON, _ := json.Marshal(people)
		rules := r.Rules
		if rules == nil {
			rules = []worldfile.Rule{}
		}
		rulesJSON, _ := json.Marshal(rules)
		gates := r.Gates
		if gates == nil {
			gates = map[string]worldfile.Gate{}
		}
		gatesJSON, _ := json.Marshal(gates)
		if _, err := tx.Exec(ctx, `UPDATE room SET name=$2, description=$3, exitjson=$4, featuresjson=$5, peoplejson=$6, rulesjson=$7, gatesjson=$8 WHERE id=$1`,
			roomIDs[key], r.Name, strings.TrimSpace(r.Description), string(exitJSON), string(featuresJSON), string(peopleJSON), string(rulesJSON), string(gatesJSON)); err != nil {
			return nil, fmt.Errorf("room %s: %w", key, err)
		}
	}

	// Spawners in the rooms the content defines follow the content exactly.
	var wanted []int32
	for _, key := range roomKeys {
		for i, s := range w.Rooms[key].Spawners {
			sk := fmt.Sprintf("%s/%s/%d", key, s.NPC, i)
			roomID, npcID := roomIDs[key], npcIDs[s.NPC]
			id, err := a.ensureSpawner(sk, roomID, npcID)
			if err != nil {
				return nil, fmt.Errorf("spawner %s: %w", sk, err)
			}
			if _, err := tx.Exec(ctx, `UPDATE baddiespawner SET totalnumber=$2, tickdurationms=$3, baddieid=$4, roomid=$5 WHERE id=$1`,
				id, s.Count, s.TickMS, npcID, roomID); err != nil {
				return nil, fmt.Errorf("spawner %s: %w", sk, err)
			}
			wanted = append(wanted, id)
		}
	}
	var contentRooms []int32
	for _, key := range roomKeys {
		contentRooms = append(contentRooms, roomIDs[key])
	}
	if _, err := tx.Exec(ctx, `DELETE FROM baddiespawner WHERE roomid = ANY($1) AND NOT (id = ANY($2))`, contentRooms, wanted); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	out.startRoomID = roomIDs[w.StartRoom]
	out.rooms = len(roomIDs)
	for _, name := range sortedKeys(w.Areas) {
		ac := AreaConfig{Name: name}
		for _, rk := range w.Areas[name].Rooms {
			ac.Rooms = append(ac.Rooms, roomIDs[rk])
		}
		out.areas = append(out.areas, ac)
	}
	return out, nil
}

// applier holds a transaction and counts what it creates.
type applier struct {
	ctx context.Context
	tx  pgx.Tx
	out *contentResult
}

// ensure finds the row for key, adopts an unkeyed row with the same name, or inserts
// one with insertSQL (whose last argument is the key). It returns the row's id.
func (a *applier) ensure(table, nameCol, key, name, insertSQL string, insertArgs ...any) (int32, error) {
	var id int32
	err := a.tx.QueryRow(a.ctx, fmt.Sprintf(`SELECT id FROM %s WHERE contentkey=$1`, table), key).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	err = a.tx.QueryRow(a.ctx, fmt.Sprintf(`SELECT id FROM %s WHERE contentkey IS NULL AND upper(%s)=upper($1) ORDER BY id LIMIT 1`, table, nameCol), name).Scan(&id)
	if err == nil {
		_, err = a.tx.Exec(a.ctx, fmt.Sprintf(`UPDATE %s SET contentkey=$2 WHERE id=$1`, table), id, key)
		return id, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	a.out.created++
	err = a.tx.QueryRow(a.ctx, insertSQL, insertArgs...).Scan(&id)
	return id, err
}

// ensureSpawner is ensure for spawners, which have no name: an unkeyed spawner for
// the same NPC in the same room is adopted.
func (a *applier) ensureSpawner(key string, roomID, npcID int32) (int32, error) {
	var id int32
	err := a.tx.QueryRow(a.ctx, `SELECT id FROM baddiespawner WHERE contentkey=$1`, key).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	err = a.tx.QueryRow(a.ctx, `SELECT id FROM baddiespawner WHERE contentkey IS NULL AND roomid=$1 AND baddieid=$2 ORDER BY id LIMIT 1`, roomID, npcID).Scan(&id)
	if err == nil {
		_, err = a.tx.Exec(a.ctx, `UPDATE baddiespawner SET contentkey=$2 WHERE id=$1`, id, key)
		return id, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	a.out.created++
	err = a.tx.QueryRow(a.ctx, `INSERT INTO baddiespawner (roomid, baddieid, contentkey) VALUES ($1, $2, $3) RETURNING id`, roomID, npcID, key).Scan(&id)
	return id, err
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
