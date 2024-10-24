package mudserver

import (
	"errors"
	"fmt"
	"math/rand/v2"
	muddatabase "mud_server/src/internal/database"
	muddata "mud_server/src/internal/muddata"

	"github.com/jackc/pgx/v5"
)

type BaddieNPC struct {
	ID int32
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

type baddieNPC_GenericDB struct {
	ID int32
	Race string
	AttackAccuracy int
	AttackPower int
	Defense int
	HP int
	MP int
}

type baddieNPC_NamedDB struct {
	ID int32
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

type BaddieNPCID interface {
	string | int32
}

func (npc BaddieNPC) FetchGenericFromDB (db muddatabase.MudDatabase, baddieid interface{}) (BaddieNPC) {
	query := ""
	var err error

	switch baddieid.(type) {
	case string:
		query = "SELECT id,race,attackaccuracy,attackpower,defense,hp,mp FROM BADDIENPC_GENERIC WHERE race=$1"
	case int, int8, int32, int64:
		query = "SELECT id,race,attackaccuracy,attackpower,defense,hp,mp FROM BADDIENPC_GENERIC WHERE id=$1"
	default:
		err = errors.New("BaddieNPC.FetchGenericFromDB invalid ID.  ID must be string|int32")
	}
	
	baddie := BaddieNPC{}

	rows,err := db.Connection.Query(db.Context,query,baddieid)

	if err != nil {
		fmt.Println("BaddieNPCGeneric DB error: "+err.Error())
	} 
	ret,err := pgx.CollectOneRow(rows,pgx.RowToStructByName[baddieNPC_GenericDB])

	if err != nil {
		fmt.Println("BaddieNPCGeneric DB: "+err.Error())
	}

	if err == nil {
		baddie = BaddieNPC{
			ID:				ret.ID,
			Race:           ret.Race,
			AttackAccuracy: ret.AttackAccuracy,
			AttackPower:    ret.AttackPower,
			Defense:        ret.Defense,
			HP:             ret.HP,
			MP:             ret.MP,
		}
	}

	return baddie

}