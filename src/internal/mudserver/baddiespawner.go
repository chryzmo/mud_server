package mudserver

import (
	"fmt"
	muddatabase "mud_server/src/internal/database"
	"mud_server/src/internal/mudutils"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type BaddieSpawner struct {
	ID int32
	TotalNumber int
	tickDurationMS int64
	baddie BaddieNPC
	ticker      *time.Ticker
	Location *Room
}

type BaddieSpawnerDB struct {
	ID int32
	TotalNumber int
	TickDurationMS int64
	BaddieID int32
	RoomID int32
}

func (spawner *BaddieSpawner) RunTicker() {
	spawner.ticker = time.NewTicker(time.Millisecond * time.Duration(spawner.tickDurationMS))

	for range spawner.ticker.C {
		// Count existing baddies of this race under read lock.
		spawner.Location.baddieMu.RLock()
		count := 0
		for _, b := range spawner.Location.BaddieMap {
			if b.Race == spawner.baddie.Race {
				count++
			}
		}
		spawner.Location.baddieMu.RUnlock()

		if count < spawner.TotalNumber {
			newbaddie := &BaddieNPC{
				Location:                 spawner.Location,
				Name:                     spawner.baddie.Name,
				Race:                     spawner.baddie.Race,
				AttackAccuracy:           spawner.baddie.AttackAccuracy,
				AttackPower:              spawner.baddie.AttackPower,
				Defense:                  spawner.baddie.Defense,
				HP:                       spawner.baddie.HP,
				MP:                       spawner.baddie.MP,
				TurnAction:               spawner.baddie.TurnAction,
				combatTickerDurationInMS: spawner.baddie.combatTickerDurationInMS,
				combatTicker:             time.NewTicker(time.Millisecond * time.Duration(spawner.baddie.combatTickerDurationInMS)),
				LootGoldMin:              spawner.baddie.LootGoldMin,
				LootGoldMax:              spawner.baddie.LootGoldMax,
			}

			if newbaddie.Name == "" {
				newbaddie.Name = newbaddie.GenerateName()
			}

			spawner.Location.baddieMu.Lock()
			spawner.Location.BaddieMap[strings.ToUpper(newbaddie.Name)] = newbaddie
			spawner.Location.baddieMu.Unlock()

			spawner.Location.Broadcast(mudutils.ColorString(newbaddie.Name, mudutils.COLOR_RED)+" - "+newbaddie.Race+" - appears in the room!", "")
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