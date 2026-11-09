package mudserver

import (
	"fmt"
	muddatabase "mud_server/src/internal/database"
	"time"

	"github.com/jackc/pgx/v5"
)

// firstSpawnDelay is how long after a player arrives the first monster appears, so someone looking for
// monsters need not wait a whole interval. It never beats the interval itself, and never comes sooner than
// an interval after the spawner's last monster, so leaving and returning cannot make them faster.
const firstSpawnDelay = 3 * time.Second

type BaddieSpawner struct {
	ID int32
	TotalNumber int
	tickDurationMS int64
	baddie BaddieNPC
	nextSpawnAt time.Time
	lastSpawnAt time.Time // when it last made a monster
	Location *Room
}

type BaddieSpawnerDB struct {
	ID int32
	TotalNumber int
	TickDurationMS int64
	BaddieID int32
	RoomID int32
}

func (spawner *BaddieSpawner) countBaddiesInRoom (baddierace string) (int) {
	count := 0
	for _, b := range spawner.Location.BaddieMap {
		if b.Race == baddierace {
			count++
		}
	}

	return count
}

// tick spawns an NPC if the spawner's interval has passed and the room is
// below its count. Called by the game loop on each world tick.
func (spawner *BaddieSpawner) tick (now time.Time, w *World) {
	if len(spawner.Location.UserMap) == 0 {
		// Nobody is here to fight it: wait, and count the interval from when someone arrives.
		spawner.nextSpawnAt = time.Time{}
		return
	}
	if spawner.nextSpawnAt.IsZero() {
		// Someone just arrived: the first monster comes after a short wait, not a whole interval.
		interval := time.Millisecond * time.Duration(spawner.tickDurationMS)
		next := now.Add(min(firstSpawnDelay, interval))
		if ready := spawner.lastSpawnAt.Add(interval); next.Before(ready) {
			next = ready
		}
		spawner.nextSpawnAt = next
	}
	if now.Before(spawner.nextSpawnAt) {
		return
	}
	spawner.nextSpawnAt = now.Add(time.Millisecond * time.Duration(spawner.tickDurationMS))
	{
		if w != nil && w.mobCap > 0 && w.liveMobs >= w.mobCap {
			return // the world is full of monsters
		}
		if spawner.countBaddiesInRoom(spawner.baddie.Race) < spawner.TotalNumber{
			// Spawn another baddie!
			newbaddie := spawner.baddie.copyOf(now)
			newbaddie.Location = spawner.Location
			w.placeMob(newbaddie)
			spawner.lastSpawnAt = now
		}
	}
}

func (spawner BaddieSpawner) FetchGenericFromDB (db *muddatabase.MudDatabase, roomid int32) (*BaddieSpawner) {
	query := "SELECT id,totalnumber,tickdurationms,baddieid,roomid FROM baddiespawner WHERE id=$1"
	var err error
	tmpspawner := BaddieSpawner{}
	tmpbaddie := BaddieNPC{}

	rows,err := db.Connection.Query(db.Context,query,roomid)

	if err != nil {
		fmt.Println("BaddieSpawner error: "+err.Error())
	}

	ret,err := pgx.CollectOneRow(rows,pgx.RowToStructByName[BaddieSpawnerDB])

	if err != nil {
		fmt.Println("BaddieSpawner error: "+err.Error())
	}

	// Build Room from RoomDB struct
	if err == nil {
		tmpbaddie = tmpbaddie.FetchGenericFromDB(db,ret.BaddieID)
		tmpspawner.TotalNumber = ret.TotalNumber
		tmpspawner.tickDurationMS = ret.TickDurationMS
		tmpspawner.baddie = tmpbaddie
	}

	return &tmpspawner
}