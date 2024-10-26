package mudserver

import (
	"errors"
	"fmt"
	"math/rand/v2"
	muddatabase "mud_server/src/internal/database"
	"time"

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
	TargetPC *MudPlayer
	TurnAction func(room *Room, baddie *BaddieNPC) int32
	combatTicker *time.Ticker
	combatTickerDurationInMS int32
	LootGoldMin int32
	LootGoldMax int32
}

type baddieNPC_GenericDB struct {
	ID int32
	Race string
	AttackAccuracy int
	AttackPower int
	Defense int
	HP int
	MP int
	CombatTickerDurationInMS int32
	TurnActionFunctionID int32
	LootGoldMin int32
	LootGoldMax int32
}

func (npc *BaddieNPC) attack (player *MudPlayer) {
	select {
	case <-npc.combatTicker.C:
		// Do attack logic
		attackRoll := rand.IntN(100) + npc.AttackAccuracy
		broadcastmsg := fmt.Sprint(npc.Name," swings at ",player.Username," - (ATT:",attackRoll," vs DEF:",player.Defense,")")
		npc.Location.Broadcast(broadcastmsg,player.Username)
		pmsg := fmt.Sprint(npc.Name+" swings at you - (ATT:",attackRoll," vs DEF:",player.Defense,")")
		player.SendMessage(pmsg)
		if attackRoll > player.Defense {
			// Hit!
			broadcastmsg = fmt.Sprint(npc.Name," hits ",player.Username," for ",npc.AttackPower," damage.")
			npc.Location.Broadcast(broadcastmsg,player.Username)
			pmsg = fmt.Sprint(npc.Name," hits you for ",npc.AttackPower," damage!")
			player.SendMessage(pmsg)
			player.HP -= npc.AttackPower

			// Check for player death
			if player.HP <= 0 {
				fmt.Println("Kill!")
				npc.Location.KillPC(player,npc.Name)
				npc.TargetPC = nil
			}
		} else {
			// NPC missed player
			npc.Location.Broadcast(npc.Name+" misses "+player.Username+".",player.Username)
			player.SendMessage(npc.Name+" misses you.")
		}
	default:
		// Do nothing as combat ticker is running
		
	}

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

func (npc BaddieNPC) FetchGenericFromDB (db *muddatabase.MudDatabase, baddieid interface{}) (BaddieNPC) {
	query := ""
	var err error

	switch baddieid.(type) {
	case string:
		query = "SELECT id,race,attackaccuracy,attackpower,defense,hp,mp,lootgoldmax,lootgoldmin,"+
				"combatTickerDurationInMS,turnActionFunctionID"+
				" FROM BADDIENPC_GENERIC WHERE race=$1"
	case int, int8, int32, int64:
		query = "SELECT id,race,attackaccuracy,attackpower,defense,hp,mp,lootgoldmax,lootgoldmin,"+
				"combatTickerDurationInMS,turnActionFunctionID"+
				" FROM BADDIENPC_GENERIC WHERE id=$1"
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
			combatTickerDurationInMS: ret.CombatTickerDurationInMS,
			TurnAction: GenerateNPCCommandList()[ret.TurnActionFunctionID].action,
			LootGoldMin: ret.LootGoldMin,
			LootGoldMax: ret.LootGoldMax,
		}
	}

	return baddie

}