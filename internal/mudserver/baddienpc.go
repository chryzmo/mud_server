package mudserver

import (
	"math/rand/v2"
	muddata "mud_server/internal/muddata"
)

type BaddieNPC struct {
	State int8
	Location *Room
	Name string
	AttackAccuracy int
	AttackPower int
	Defense int
	HP int
	MP int
}

func (npc *BaddieNPC) attack (player *MudPlayer) (int, int) {
	attackRoll := rand.IntN(100) + npc.AttackAccuracy
	if attackRoll > player.Defense {
		// Hit!

		return muddata.STATUS_ATTACK_HIT, npc.AttackPower
	}

	return muddata.STATUS_ATTACK_MISS,0
}