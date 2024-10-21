package mudserver

import "time"

type BaddieSpawner struct {
	TotalNumber int
	tickDurationMS int64
	ticker      *time.Ticker
	Location *Room
	baddie BaddieNPC
}

func (spawner BaddieSpawner) countBaddiesInRoom (baddiename string) (int) {
	count := 0
	for _, b := range spawner.Location.BaddieSlice {
		if b.Name == baddiename {
			count++
		}
	}

	return count
}

func (spawner BaddieSpawner) RunTicker () {
	spawner.ticker = time.NewTicker(time.Millisecond * time.Duration(spawner.tickDurationMS))


	for range spawner.ticker.C {
		if spawner.countBaddiesInRoom(spawner.baddie.Name) < spawner.TotalNumber{
			// Spawn another baddie!
			newbaddie := &BaddieNPC{
				Location: spawner.baddie.Location,
				Name: spawner.baddie.Name,
				AttackAccuracy: spawner.baddie.AttackAccuracy,
				AttackPower: spawner.baddie.AttackPower,
				Defense: spawner.baddie.Defense,
				HP: spawner.baddie.HP,
				MP: spawner.baddie.MP,
			}

			spawner.Location.BaddieSlice = append(spawner.Location.BaddieSlice,newbaddie)
			spawner.Location.Broadcast(spawner.baddie.Name+" appears in the room!","")
		}
	}
}