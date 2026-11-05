package mudserver

import (
	"errors"
	"fmt"
	muddatabase "mud_server/src/internal/database"
	muddata "mud_server/src/internal/muddata"
	"mud_server/src/internal/mudutils"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

type MudPlayer struct {
	ID            int32
	Session       *Session
	TimeConnected time.Time
	State         int8
	Location      *Room
	Username      string
	PasswordHash  string
	Inventory     *Inventory
	Equipment     *EquipmentSlots
	AttackPower   int
	Defense       int
	HP            int
	MP            int
	MaxHP         int
	MaxMP         int
	NPCTarget     *BaddieNPC
	dirty         bool           // changed since the last save; see persist.go
	sayLimiter    commandLimiter // loop-owned; created on first SAY
	SpawnLocation *Room
}

type MudPlayerDB struct {
	ID               int32
	Username         string
	PasswordHash     string
	InventoryIDSlice map[string]int32
	EquipmentSlots   map[string]int32
	AttackPower      int
	Defense          int
	HP               int
	MP               int
	MaxHP            int
	MaxMP            int
	RoomID           int32
	SpawnLocationID  int32
}

type PlayerStartStats struct {
	HP      int
	MaxHP   int
	MP      int
	MaxMP   int
	Attack  int
	Defense int
}

func CreateMudPlayer(session *Session, room *Room, stats PlayerStartStats) *MudPlayer {
	return &MudPlayer{
		Session:       session,
		TimeConnected: time.Now(),
		Location:      room,
		Inventory:     CreateInventory(),
		Equipment:     NewEquipmentSlots(),
		AttackPower:   stats.Attack,
		Defense:       stats.Defense,
		HP:            stats.HP,
		MaxHP:         stats.MaxHP,
		MP:            stats.MP,
		MaxMP:         stats.MaxMP,
		SpawnLocation: room,
	}
}

// allowSay reports whether the player may SAY now, telling them when it is
// refused. Runs on the game loop.
func (mp *MudPlayer) allowSay(now time.Time) bool {
	if mp.sayLimiter.capacity == 0 {
		mp.sayLimiter = newSayLimiter(mp.Session.settings, now)
	}
	allowed, notify := mp.sayLimiter.allow(now)
	if notify {
		mp.SendMessage("You are talking too fast.")
	}
	return allowed
}

// EffectiveAttackPower returns base attack plus all equipment bonuses.
func (mp *MudPlayer) EffectiveAttackPower() int {
	return mp.AttackPower + mp.Equipment.TotalBonusAttackPower()
}

// EffectiveDefense returns base defense plus all equipment bonuses.
func (mp *MudPlayer) EffectiveDefense() int {
	return mp.Defense + mp.Equipment.TotalBonusDefense()
}

// EffectiveMaxHP returns base MaxHP plus all equipment bonuses.
func (mp *MudPlayer) EffectiveMaxHP() int {
	return mp.MaxHP + mp.Equipment.TotalBonusMaxHP()
}

// EffectiveMaxMP returns base MaxMP plus all equipment bonuses.
func (mp *MudPlayer) EffectiveMaxMP() int {
	return mp.MaxMP + mp.Equipment.TotalBonusMaxMP()
}

// SetPassword hashes and stores the player's password using bcrypt.
func (mp *MudPlayer) SetPassword(password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	mp.PasswordHash = string(hash)
	return nil
}

// CheckPassword returns true if the given password matches the stored hash.
func (mp *MudPlayer) CheckPassword(password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(mp.PasswordHash), []byte(password))
	return err == nil
}

// SendMessage queues a message for the player's client. It never blocks; it
// fails only if the session is closed or the client has fallen too far behind.
func (mp *MudPlayer) SendMessage(message string) error {
	return mp.Session.Send([]byte(message))
}

// SendStatUpdate pushes a STATUPDATE line to the client.  The frontend
// intercepts lines with this prefix and updates the stats bar without
// displaying them in the chat log.
func (mp *MudPlayer) SendStatUpdate() {
	atkBonus := mp.Equipment.TotalBonusAttackPower()
	defBonus := mp.Equipment.TotalBonusDefense()
	line := fmt.Sprintf("STATUPDATE:hp=%d,maxhp=%d,mp=%d,maxmp=%d,atk=%d,atkbonus=%d,def=%d,defbonus=%d\n",
		mp.HP, mp.EffectiveMaxHP(),
		mp.MP, mp.EffectiveMaxMP(),
		mp.EffectiveAttackPower(), atkBonus,
		mp.EffectiveDefense(), defBonus,
	)
	mp.SendMessage(line)
}

func (mp *MudPlayer) MoveRoom(dir string) int {
	retcode := muddata.MUD_OK

	// Standardize direction
	if strings.ToUpper(dir) == "NORTH" {
		dir = "N"
	}
	if strings.ToUpper(dir) == "SOUTH" {
		dir = "S"
	}
	if strings.ToUpper(dir) == "EAST" {
		dir = "E"
	}
	if strings.ToUpper(dir) == "WEST" {
		dir = "W"
	}
	if strings.ToUpper(dir) == "UP" {
		dir = "U"
	}
	if strings.ToUpper(dir) == "DOWN" {
		dir = "D"
	}

	if mp.Location.ExitMap[dir] == nil {
		// Room does not exist
		retcode = muddata.ERR_ROOM_DNE
	} else {
		// Clean up location info in old room, set new room
		mp.Location.RemovePlayer(mp.Username)
		mp.Location.Broadcast(mudutils.ColorString(mp.Username, mudutils.COLOR_BLUE)+" has left to the "+dir+".", mp.Username)

		// Set new location for Player
		newroom := mp.Location.ExitMap[dir]
		mp.Location = newroom
		mp.Location.Broadcast(mudutils.ColorString(mp.Username, mudutils.COLOR_BLUE)+" has entered the room from the "+dir+".", mp.Username)
		mp.Location.AddPlayer(mp)
	}

	return retcode
}

func (mp *MudPlayer) LoadFromDB(db *muddatabase.MudDatabase, world *World, player *MudPlayer, playerid interface{}) error {
	query := ""
	var err error

	switch playerid.(type) {
	case string:
		query = "SELECT id,username,passwordhash,inventoryidslice,equipmentslots,attackpower,defense,hp,maxhp,mp,maxmp,roomid,spawnlocationid FROM player WHERE username=$1"
	case int, int8, int32, int64:
		query = "SELECT id,username,passwordhash,inventoryidslice,equipmentslots,attackpower,defense,hp,maxhp,mp,maxmp,roomid,spawnlocationid FROM player WHERE id=$1"
	default:
		err = errors.New("Player.LoadFromDB invalid ID.  ID must be string|int32")
	}

	rows, err := db.Connection.Query(db.Context, query, playerid)

	if err != nil {
		fmt.Println("Player query error: " + err.Error())
	}

	ret, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[MudPlayerDB])

	if err != nil {
		fmt.Println("Player row error: " + err.Error())
	}

	if err == nil {
		player.ID = ret.ID
		player.Username = ret.Username
		player.PasswordHash = ret.PasswordHash
		player.Location = world.RoomMap[ret.RoomID]
		if player.Location == nil {
			player.Location = world.RoomMap[1]
		}
		player.Inventory = CreateInventory()
		player.Equipment = NewEquipmentSlots()
		player.Equipment.LoadFromIDMap(db, ret.EquipmentSlots)
		player.AttackPower = ret.AttackPower
		player.Defense = ret.Defense
		player.HP = ret.HP
		player.MaxHP = ret.MaxHP
		player.MP = ret.MP
		player.MaxMP = ret.MaxMP
		player.SpawnLocation = world.RoomMap[ret.SpawnLocationID]
		if player.SpawnLocation == nil {
			player.SpawnLocation = world.RoomMap[1]
		}

		// Build inventory
		for iid := range ret.InventoryIDSlice {
			i := Item{}
			sid, _ := strconv.ParseInt(iid[1:], 10, 32)
			player.Inventory.AddItemByQuantity(i.FetchFromDB(db, sid), ret.InventoryIDSlice[iid])
		}
	}

	return err
}

func (mp *MudPlayer) CountUsernameFromDB(db *muddatabase.MudDatabase, playerid interface{}) int32 {
	query := ""
	var err error
	var usercount int32 = 0

	switch playerid.(type) {
	case string:
		query = "SELECT count(*) FROM player WHERE username=$1"
	case int, int8, int32, int64:
		query = "SELECT count(*) FROM player id=$1"
	default:
		err = errors.New("Player.CountUsernameFromDB invalid ID.  ID must be string|int")
	}

	rows, err := db.Connection.Query(db.Context, query, playerid)

	if err != nil {
		fmt.Println("CountUsernameFromDB query error: " + err.Error())
	}

	usercount, err = pgx.CollectOneRow(rows, pgx.RowTo[int32])

	if err != nil {
		fmt.Println("CountUsernameFromDB scan error: " + err.Error())
	}

	return usercount
}

var errUsernameTaken = errors.New("username taken")

// InsertIntoDB creates the player's row and sets mp.ID. If the name is already
// taken (the unique index on username), it returns errUsernameTaken.
func (mp *MudPlayer) InsertIntoDB(db *muddatabase.MudDatabase) error {
	query := "INSERT INTO player (username,passwordhash,inventoryidslice,equipmentslots,attackpower,defense,hp,maxhp,mp,maxmp,roomid,spawnlocationid) VALUES (@username,@passwordhash,@inventoryidslice,@equipmentslots,@attackpower,@defense,@hp,@maxhp,@mp,@maxmp,@roomid,@spawnlocationid) RETURNING id;"
	args := pgx.NamedArgs{
		"username":         mp.Username,
		"passwordhash":     mp.PasswordHash,
		"inventoryidslice": mp.Inventory.IDMap(),
		"equipmentslots":   mp.Equipment.ToIDMap(),
		"attackpower":      mp.AttackPower,
		"defense":          mp.Defense,
		"hp":               mp.HP,
		"maxhp":            mp.MaxHP,
		"mp":               mp.MP,
		"maxmp":            mp.MaxMP,
		"roomid":           mp.Location.ID,
		"spawnlocationid":  mp.SpawnLocation.ID,
	}

	err := db.Connection.QueryRow(db.Context, query, args).Scan(&mp.ID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		return errUsernameTaken
	}
	return err
}

func (mp *MudPlayer) GetIDFromDB(db *muddatabase.MudDatabase) int32 {
	query := "SELECT id FROM player WHERE username=$1"
	var err error

	rows, err := db.Connection.Query(db.Context, query, mp.Username)

	if err != nil {
		fmt.Println("GetIDFromDB query error: " + err.Error())
	}

	userid, err := pgx.CollectOneRow(rows, pgx.RowTo[int32])

	if err != nil {
		fmt.Println("GetIDFromDB scan error: " + err.Error())
	}

	return userid
}

func (mp *MudPlayer) Respawn() {
	mp.Location.RemovePlayer(mp.Username)
	mp.HP = mp.MaxHP
	mp.MP = mp.MaxMP
	mp.dirty = true
	if mp.SpawnLocation != nil {
		mp.Location = mp.SpawnLocation
	}
	mp.Location.AddPlayer(mp)
	mp.Location.Broadcast(mp.Username+" has appeared in the room.", mp.Username)
	mp.SendMessage(mp.Location.LookDescription())
}
