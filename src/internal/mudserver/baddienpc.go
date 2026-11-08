package mudserver

import (
	"errors"
	"fmt"
	"math/rand/v2"
	muddatabase "mud_server/src/internal/database"
	"mud_server/src/internal/mudutils"
	"strconv"
	"strings"
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
	TurnAction func(room *Room, baddie *BaddieNPC, now time.Time) int32
	nextAttackAt time.Time // the NPC can swing again once the world tick reaches this
	combatTickerDurationInMS int32
	LootGoldMin int32
	LootGoldMax int32
	passive     bool        // fights only those who attack it (mobs.go); the zero value attacks anyone in the room
	xp          int         // paid to each fighter for a kill; 0 means the progression's kill XP
	hitters     []*MudPlayer // who has attacked it, latest last
	dead        bool        // set when it is killed, so a late blow at the same monster is ignored
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

// attack swings at the player if the NPC's combat timer has run out; otherwise
// it does nothing this tick.
func (npc *BaddieNPC) attack (player *MudPlayer, now time.Time) {
	if now.Before(npc.nextAttackAt) {
		return
	}
	npc.nextAttackAt = now.Add(time.Millisecond * time.Duration(npc.combatTickerDurationInMS))
	{
		// Do attack logic
		npcstyledname := (mudutils.ColorString(npc.Name,mudutils.COLOR_RED))
		attackRoll := rand.IntN(100) + npc.AttackAccuracy
		effDef := player.EffectiveDefense()
		broadcastmsg := fmt.Sprint(npcstyledname, " swings at ", player.Username, " - (ATT:", attackRoll, " vs DEF:", effDef, ")")
		npc.Location.Broadcast(broadcastmsg, player.Username)
		pmsg := fmt.Sprint(npcstyledname+" swings at you - (ATT:", attackRoll, " vs DEF:", effDef, ")")
		player.SendMessage(pmsg)
		if attackRoll > effDef {
			// Hit!
			broadcastmsg = fmt.Sprint(npcstyledname, " hits ", player.Username, " for ", npc.AttackPower, " damage.")
			npc.Location.Broadcast(broadcastmsg, player.Username)

			pmsg = fmt.Sprint(npcstyledname, " hits you for ", npc.AttackPower, " damage!")
			player.SendMessage(pmsg)
			player.HP -= npc.AttackPower
			player.dirty = true
			player.SendStatUpdate()

			// Check for player death
			if player.HP <= 0 {
				fmt.Println("Kill!")
				npc.Location.KillPC(player, npc.Name)
				npc.TargetPC = nil
			}
		} else {
			// NPC missed player
			npc.Location.Broadcast(npcstyledname+" misses "+player.Username+".", player.Username)
			player.SendMessage(npcstyledname + " misses you.")
		}
	}
}

// npcNamePool maps race names to a pool of thematic names.
var npcNamePool = map[string][]string{
	"ORC":    {"Grak", "Morg", "Durg", "Thrak", "Bolgur", "Skrag", "Vrosh", "Hagak", "Orzug", "Krul"},
	"GOBLIN": {"Nib", "Skix", "Fizz", "Wret", "Grix", "Snig", "Pikk", "Dreg", "Yigg", "Zort"},
	"TROLL":  {"Grumble", "Stonefist", "Mudrock", "Bogmaw", "Crumble", "Ironhide", "Slagfoot", "Grimtoe", "Warthud", "Boulderback"},
	"SPIDER": {"Silkfang", "Darkweave", "Venom", "Gossamer", "Nightcrawl", "Skitter", "Tangle", "Dreadspinner", "Blackmaw", "Lurker"},
	"UNDEAD": {"Rattlebone", "Grimshaw", "Deathmask", "Soulless", "Wraithclaw", "Moaning", "Fetid", "Hollowed", "Desecrated", "Ashenform"},
}

// GenerateName picks a thematic name from the race's name pool, falling back
// to a simple race+index label if the race is unknown.
func (npc *BaddieNPC) GenerateName() string {
	race := strings.ToUpper(npc.Race)
	if pool, ok := npcNamePool[race]; ok {
		return pool[rand.IntN(len(pool))]
	}
	return npc.Race + strconv.Itoa(rand.IntN(1000))
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