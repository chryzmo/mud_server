package mudserver

import (
	"errors"
	"fmt"
	muddatabase "mud_server/src/internal/database"
	muddata "mud_server/src/internal/muddata"
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
	dirty         bool // changed since the last save; see persist.go
	// Where the player was saved, by template room ID. Login records these on the
	// reader goroutine; the game loop turns them into Location and SpawnLocation
	// when the player is admitted (see World.place).
	savedRoomID   int32
	savedSpawnID  int32
	world         *World                        // set at admission; used to place a respawn
	guest         bool                          // joined by name only (see guest.go)
	tokenHash     string                        // hash of the resume token; set for a new guest until it is inserted
	recoveryHash  string                        // hash of the recovery code; likewise
	lastCopy      map[*Area]int                 // the copy of each area the player was last in; loop-owned
	dormant       bool                          // connection dropped, still in the world for a grace period (see resume.go)
	lastQuiet     time.Time                     // the last line spoken in a quiet room (stage.go)
	lastStage     time.Time                     // the last STAGE line
	lastActive    time.Time                     // the last command, admission or resume; see idle.go
	ring          [][]byte                      // the last counted frames sent, for replay after a resume
	sent          uint64                        // counted frames sent over the player's lifetime in this process
	replaySize    int                           // ring capacity; 0 means no replay
	sayLimiter    commandLimiter                // loop-owned; created on first SAY
	state         *playerState                  // puzzle state (state.go); created on first use
	lastPuzzle    string                        // the puzzle whose flag was set last (not saved); HINT with no name uses it
	chatLimiter   commandLimiter                // this player's global chat bucket; created on first use
	lastChat      time.Time                     // when they last spoke on global chat (slow mode)
	mod           modState                      // automatic moderation record (moderation.go); loop only
	server        *MudServer                    // set at admission, for the checks a command makes (moderation.go)
	ignored       map[string]bool               // lower-case names whose talk this player does not see (social.go); loop only, for the session
	reports       []time.Time                   // when this player last sent REPORTs (report.go); loop only
	lineSink      func(where, who, text string) // remembers talk for report context; set at admission
	lastHint      time.Time                     // when HINT last answered; loop only
	stateSink     func(stateWrite)              // where its changes are queued; set at admission
	gm            bool                          // a game master (a password account with the gm flag; never a guest); see gm.go
	mutedUntil    time.Time                     // SAY and WHISPER are refused until then; loop-owned
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
	Guest            bool
	ResumeTokenHash  string
	GM               bool
	MutedUntil       *time.Time
}

type PlayerStartStats struct {
	HP      int
	MaxHP   int
	MP      int
	MaxMP   int
	Attack  int
	Defense int
}

// CreateMudPlayer starts a new player who has not yet been placed in a room:
// admission does that, on the game loop.
func CreateMudPlayer(session *Session, stats PlayerStartStats, startRoomID int32) *MudPlayer {
	return &MudPlayer{
		Session:       session,
		TimeConnected: time.Now(),
		savedRoomID:   startRoomID,
		savedSpawnID:  startRoomID,
		Inventory:     CreateInventory(),
		Equipment:     NewEquipmentSlots(),
		AttackPower:   stats.Attack,
		Defense:       stats.Defense,
		HP:            stats.HP,
		MaxHP:         stats.MaxHP,
		MP:            stats.MP,
		MaxMP:         stats.MaxMP,
	}
}

// allowSay reports whether the player may SAY now, telling them when it is
// refused. Runs on the game loop.
func (mp *MudPlayer) allowSay(now time.Time) bool {
	if mp.sayLimiter.capacity == 0 {
		mp.sayLimiter = newSayLimiter(mp.Session.settings, now)
	}
	allowed, notify := mp.sayLimiter.allow(now)
	if !allowed && mp.server != nil {
		mp.server.noteRefused(mp, now)
	}
	if notify {
		mp.SendError(codeRateLimited, "You are talking too fast.")
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

// CheckPassword returns true if the given password matches the stored hash.
func (mp *MudPlayer) CheckPassword(password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(mp.PasswordHash), []byte(password))
	return err == nil
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

	newroom, refused := mp.exitFor(dir)
	if refused {
		return retRoomLocked
	}
	if newroom == nil {
		// Room does not exist
		retcode = muddata.ERR_ROOM_DNE
	} else if mp.world != nil && !mp.world.canEnter(mp, &newroom) {
		// Too crowded; nothing has changed.
		retcode = muddata.ERR_ROOM_FULL
	} else {
		// Clean up location info in old room, set new room
		mp.Location.RemovePlayer(mp.Username)
		mp.Location.BroadcastFrame(encodeLeave(mp, dir), mp.Username)

		// Set new location for Player
		mp.Location = newroom
		mp.rememberCopy()
		mp.Location.BroadcastFrame(encodeArrive(mp, dir), mp.Username)
		mp.Location.AddPlayer(mp)
	}

	return retcode
}

func (mp *MudPlayer) LoadFromDB(db *muddatabase.MudDatabase, player *MudPlayer, playerid interface{}) error {
	query := ""
	var err error

	switch playerid.(type) {
	case string:
		query = "SELECT id,username,passwordhash,inventoryidslice,equipmentslots,attackpower,defense,hp,maxhp,mp,maxmp,roomid,spawnlocationid,guest,resumetokenhash,gm,muteduntil FROM player WHERE username=$1"
	case int, int8, int32, int64:
		query = "SELECT id,username,passwordhash,inventoryidslice,equipmentslots,attackpower,defense,hp,maxhp,mp,maxmp,roomid,spawnlocationid,guest,resumetokenhash,gm,muteduntil FROM player WHERE id=$1"
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
		player.savedRoomID = ret.RoomID
		player.Inventory = CreateInventory()
		player.Equipment = NewEquipmentSlots()
		player.Equipment.LoadFromIDMap(db, ret.EquipmentSlots)
		player.AttackPower = ret.AttackPower
		player.Defense = ret.Defense
		player.HP = ret.HP
		player.MaxHP = ret.MaxHP
		player.MP = ret.MP
		player.MaxMP = ret.MaxMP
		player.savedSpawnID = ret.SpawnLocationID
		player.guest = ret.Guest
		player.tokenHash = ret.ResumeTokenHash
		player.gm = ret.GM && !ret.Guest // a guest is never a GM, whatever the row says
		player.loadState(db)
		if ret.MutedUntil != nil {
			player.mutedUntil = *ret.MutedUntil
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
	query := "INSERT INTO player (username,passwordhash,inventoryidslice,equipmentslots,attackpower,defense,hp,maxhp,mp,maxmp,roomid,spawnlocationid,guest,resumetokenhash,recoveryhash) VALUES (@username,@passwordhash,@inventoryidslice,@equipmentslots,@attackpower,@defense,@hp,@maxhp,@mp,@maxmp,@roomid,@spawnlocationid,@guest,@resumetokenhash,@recoveryhash) RETURNING id;"
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
		"roomid":           mp.savedRoomID,
		"spawnlocationid":  mp.savedSpawnID,
		"guest":            mp.guest,
		"resumetokenhash":  mp.tokenHash,
		"recoveryhash":     mp.recoveryHash,
	}

	err := db.Connection.QueryRow(db.Context, query, args).Scan(&mp.ID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		return errUsernameTaken
	}
	return err
}

// DeleteFromDB removes the player's row, for a guest created but never admitted.
func (mp *MudPlayer) DeleteFromDB(db *muddatabase.MudDatabase) {
	if mp.ID == 0 {
		return
	}
	if _, err := db.Connection.Exec(db.Context, "DELETE FROM player WHERE id=$1", mp.ID); err != nil {
		fmt.Println("DeleteFromDB error: " + err.Error())
	}
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
	mp.NPCTarget = nil // the fight is over; dying costs nothing else
	mp.Location.RemovePlayer(mp.Username)
	mp.HP = mp.MaxHP
	mp.MP = mp.MaxMP
	mp.dirty = true
	if mp.SpawnLocation != nil {
		mp.Location = mp.SpawnLocation
		if mp.world != nil {
			// A respawn lands in a copy with room, like an arrival; if every copy is
			// full the player still returns to their spawn room.
			if room, ok := mp.world.placeNear(mp.SpawnLocation.persistID(), mp.preferredCopy(mp.SpawnLocation)); ok {
				mp.Location = room
			}
		}
	}
	mp.Location.AddPlayer(mp)
	mp.rememberCopy()
	mp.Location.BroadcastFrame(encodeArrive(mp, ""), mp.Username)
	mp.SendLook()
}
