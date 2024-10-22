package mudserver

import (
	"math/rand/v2"
	muddata "mud_server/src/internal/muddata"
)

type BaddieNPC struct {
	State int8
	Location *Room
	Name string
	Race string
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

// TODO:  Update this with a lookup table
func (npc *BaddieNPC) GenerateName() (string) {
	letterRunes := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

	b := make([]rune, 5)
	for i := range b {
		b[i] = letterRunes[rand.IntN(len(letterRunes))]
	}
	return npc.Race+string(b)
}