package mudserver

import (
	"strings"
	"time"
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
	tickDurationMS int64
	baddieID int32
	LocationID int32
}

func (spawner BaddieSpawner) countBaddiesInRoom (baddierace string) (int) {
	count := 0
	for _, b := range spawner.Location.BaddieMap {
		if b.Race == baddierace {
			count++
		}
	}

	return count
}

func (spawner BaddieSpawner) RunTicker () {
	spawner.ticker = time.NewTicker(time.Millisecond * time.Duration(spawner.tickDurationMS))


	for range spawner.ticker.C {
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
			}

			if newbaddie.Name == "" {
				newbaddie.Name = newbaddie.GenerateName()
			}

			spawner.Location.BaddieMap[strings.ToUpper(newbaddie.Name)] = newbaddie
			spawner.Location.Broadcast(newbaddie.Name+" - "+newbaddie.Race+" - appears in the room!","")
		}
	}
}