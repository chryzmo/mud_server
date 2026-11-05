package mudserver

import (
	"fmt"
	muddatabase "mud_server/src/internal/database"

	"github.com/jackc/pgx/v5"
)

/*
Saving is batched and happens off the game loop. Anything that changes a
player or a room's contents marks it dirty. Every SaveIntervalMS the loop
copies the dirty ones into snapshots (plain values, no pointers into live
state) and hands the batch to the saver goroutine, which writes it in one
transaction. The loop never waits on the database.

A player who disconnects is snapshotted at once and saved in the next batch;
until that batch is written, logging back in under the same name waits for
it, so the login loads what was saved.
*/

type playerSnapshot struct {
	ID              int32
	Username        string
	PasswordHash    string
	Inventory       map[string]int32
	Equipment       map[string]int32
	AttackPower     int
	Defense         int
	HP              int
	MP              int
	MaxHP           int
	MaxMP           int
	RoomID          int32
	SpawnLocationID int32
}

type roomSnapshot struct {
	ID          int32
	Name        string
	Description string
	ExitJSON    string
	Inventory   map[string]int32
}

// saveBatch is one transaction's worth of snapshots. onDone, if set, is run
// on the game loop after the write, with its result.
type saveBatch struct {
	players []playerSnapshot
	rooms   []roomSnapshot
	onDone  func(err error)
}

func (mp *MudPlayer) snapshot() playerSnapshot {
	return playerSnapshot{
		ID:              mp.ID,
		Username:        mp.Username,
		PasswordHash:    mp.PasswordHash,
		Inventory:       mp.Inventory.IDMap(),
		Equipment:       mp.Equipment.ToIDMap(),
		AttackPower:     mp.AttackPower,
		Defense:         mp.Defense,
		HP:              mp.HP,
		MP:              mp.MP,
		MaxHP:           mp.MaxHP,
		MaxMP:           mp.MaxMP,
		RoomID:          mp.Location.ID,
		SpawnLocationID: mp.SpawnLocation.ID,
	}
}

func (r *Room) snapshot() roomSnapshot {
	return roomSnapshot{
		ID:          r.ID,
		Name:        r.Name,
		Description: r.Description,
		ExitJSON:    r.ExitJSON,
		Inventory:   r.Inventory.IDMap(),
	}
}

// writeBatch writes a batch in one transaction.
func writeBatch(db *muddatabase.MudDatabase, b saveBatch) error {
	batch := &pgx.Batch{}
	for _, p := range b.players {
		if p.ID == 0 {
			continue // never inserted; registration inserts before admission
		}
		batch.Queue("UPDATE player SET username=@username, passwordhash=@passwordhash, inventoryidslice=@inventoryidslice, equipmentslots=@equipmentslots, attackpower=@attackpower, defense=@defense, hp=@hp, maxhp=@maxhp, mp=@mp, maxmp=@maxmp, roomid=@roomid, spawnlocationid=@spawnlocationid WHERE id=@playerid",
			pgx.NamedArgs{
				"username":         p.Username,
				"passwordhash":     p.PasswordHash,
				"inventoryidslice": p.Inventory,
				"equipmentslots":   p.Equipment,
				"attackpower":      p.AttackPower,
				"defense":          p.Defense,
				"hp":               p.HP,
				"maxhp":            p.MaxHP,
				"mp":               p.MP,
				"maxmp":            p.MaxMP,
				"roomid":           p.RoomID,
				"spawnlocationid":  p.SpawnLocationID,
				"playerid":         p.ID,
			})
	}
	for _, r := range b.rooms {
		batch.Queue("UPDATE room SET name=@name, description=@description, exitjson=@exitjson, inventoryidslice=@inventoryidslice WHERE id=@roomid",
			pgx.NamedArgs{
				"name":             r.Name,
				"description":      r.Description,
				"exitjson":         r.ExitJSON,
				"inventoryidslice": r.Inventory,
				"roomid":           r.ID,
			})
	}
	if batch.Len() == 0 {
		return nil
	}

	tx, err := db.Connection.Begin(db.Context)
	if err != nil {
		return err
	}
	defer tx.Rollback(db.Context)
	if err := tx.SendBatch(db.Context, batch).Close(); err != nil {
		return err
	}
	return tx.Commit(db.Context)
}

// saver is the goroutine that writes batches until the channel closes.
func (ms *MudServer) saver(batches <-chan saveBatch, finished chan<- struct{}) {
	defer close(finished)
	for b := range batches {
		err := writeBatch(&ms.DB, b)
		if err != nil {
			fmt.Printf("Save failed (%d players, %d rooms): %v\n", len(b.players), len(b.rooms), err)
		}
		if b.onDone != nil {
			onDone := b.onDone
			ms.enqueue(func() { onDone(err) })
		}
	}
}

// flushDirty hands every dirty player and room, plus anyone who disconnected
// since the last batch, to the saver. If the saver is still busy with the
// previous batch, nothing is cleared and the next flush picks it all up.
// Loop only.
func (ms *MudServer) flushDirty() {
	if ms.saving {
		ms.flushAgain = true
		return
	}

	var b saveBatch
	var players []*MudPlayer
	var rooms []*Room
	for _, p := range ms.players {
		if p.dirty {
			p.dirty = false
			players = append(players, p)
			b.players = append(b.players, p.snapshot())
		}
	}
	for _, r := range ms.World.RoomMap {
		if r.dirty {
			r.dirty = false
			rooms = append(rooms, r)
			b.rooms = append(b.rooms, r.snapshot())
		}
	}
	leaving := ms.leaving
	ms.leaving = nil
	for _, l := range leaving {
		b.players = append(b.players, l.snapshot)
	}
	if len(b.players) == 0 && len(b.rooms) == 0 {
		return
	}

	b.onDone = func(err error) {
		ms.saving = false
		if err != nil {
			// Try again next time: re-mark whatever is still in the world.
			for _, p := range players {
				if ms.players[p.Username] == p {
					p.dirty = true
				}
			}
			for _, r := range rooms {
				r.dirty = true
			}
		}
		// Logins waiting on these players can go ahead (even after a failed
		// write, rather than wait forever).
		for _, l := range leaving {
			// Only clear the marker if it's still ours: the player may have logged
			// back in and left again since.
			if ms.savingNames[l.snapshot.Username] == l.saved {
				delete(ms.savingNames, l.snapshot.Username)
			}
			close(l.saved)
		}
		if ms.flushAgain {
			ms.flushAgain = false
			ms.flushDirty()
		}
	}
	ms.saving = true
	ms.saves <- b // the saver is idle, so this doesn't block
}

// leavingPlayer is a disconnected player's final state, waiting to be saved.
type leavingPlayer struct {
	snapshot playerSnapshot
	saved    chan struct{} // closed once the batch holding it is written
}
