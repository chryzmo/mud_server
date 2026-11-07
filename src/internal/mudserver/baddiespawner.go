package mudserver

import (
	"fmt"
	muddatabase "mud_server/src/internal/database"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type BaddieSpawner struct {
	ID int32
	TotalNumber int
	tickDurationMS int64
	baddie BaddieNPC
	nextSpawnAt time.Time
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
		// The first spawn comes one interval after startup, as with the old ticker.
		spawner.nextSpawnAt = now.Add(time.Millisecond * time.Duration(spawner.tickDurationMS))
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
			newbaddie := &BaddieNPC{
				Location: spawner.Location,
				Name: spawner.baddie.Name,
				Race: spawner.baddie.Race,
				AttackAccuracy: spawner.baddie.AttackAccuracy,
				AttackPower: spawner.baddie.AttackPower,
				Defense: spawner.baddie.Defense,
				HP: spawner.baddie.HP,
				MP: spawner.baddie.MP,
				TurnAction: spawner.baddie.TurnAction,
				combatTickerDurationInMS: spawner.baddie.combatTickerDurationInMS,
				// As with the old per-NPC ticker, the first swing comes one interval after spawning.
				nextAttackAt: now.Add(time.Millisecond * time.Duration(spawner.baddie.combatTickerDurationInMS)),
				LootGoldMin: spawner.baddie.LootGoldMin,
				LootGoldMax: spawner.baddie.LootGoldMax,
				passive: spawner.baddie.passive,
				xp: spawner.baddie.xp,
			}

			if newbaddie.Name == "" {
				newbaddie.Name = newbaddie.GenerateName()
			}

			newbaddie.Name = spawner.Location.uniqueMobName(newbaddie.Name)
			spawner.Location.BaddieMap[strings.ToUpper(newbaddie.Name)] = newbaddie
			if w != nil {
				w.liveMobs++
			}
			spawner.Location.Broadcast(newbaddie.Name+" - "+newbaddie.Race+" - appears in the room!","")
			spawner.Location.roomChanged()
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