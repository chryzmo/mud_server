package mudserver

import (
	"errors"
	"fmt"
	muddatabase "mud_server/src/internal/database"
	muddata "mud_server/src/internal/muddata"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
)

type MudPlayer struct {
	ID int32
	Conn	*websocket.Conn
	TimeConnected	time.Time
	State int8
	Location *Room
	Username string
	Inventory *Inventory
	AttackPower int
	Defense int
	HP int
	MP int
	MaxHP int
	MaxMP int
	NPCTarget *BaddieNPC
	send_mutex sync.Mutex
	SpawnLocation *Room
}

type MudPlayerDB struct {
	ID int32
	Username string
	InventoryIDSlice map[string]int32
	AttackPower int
	Defense int
	HP int
	MP int
	MaxHP int
	MaxMP int
	RoomID int32
	SpawnLocationID int32
}

func CreateMudPlayer(ws_conn *websocket.Conn, room *Room) (*MudPlayer) {
	return &MudPlayer{
		Conn: ws_conn,
		TimeConnected: time.Now(),
		Location: room,
		Inventory: CreateInventory(),
		AttackPower: 99,
		Defense: 10,
		HP: 5,
		MaxHP: 5,
		MP: 5,
		MaxMP: 5,
		SpawnLocation: room,
	}
}

func (mp *MudPlayer) SendMessage(message string) (error){
	mp.send_mutex.Lock()
	defer mp.send_mutex.Unlock()
	err := mp.Conn.WriteMessage(websocket.TextMessage, []byte(message))

	if err != nil {
		fmt.Println(err)
	}
	return err
}

func (mp *MudPlayer) MoveRoom (dir string) int {
	retcode := muddata.MUD_OK

	// Standardize direction
	if strings.ToUpper(dir) == "NORTH"{
		dir = "N"
	}
	if strings.ToUpper(dir) == "SOUTH"{
		dir = "S"
	}
	if strings.ToUpper(dir) == "EAST"{
		dir = "E"
	}
	if strings.ToUpper(dir) == "WEST"{
		dir = "W"
	}
	if strings.ToUpper(dir) == "UP"{
		dir = "U"
	}
	if strings.ToUpper(dir) == "DOWN"{
		dir = "D"
	}

	if mp.Location.ExitMap[dir] == nil {
		// Room does not exist
		retcode = muddata.ERR_ROOM_DNE
	} else {
		// Clean up location info in old room, set new room
		mp.Location.RemovePlayer(mp.Username)
		mp.Location.Broadcast(mp.Username+" has left to the "+dir+".",mp.Username)

		// Set new location for Player
		newroom := mp.Location.ExitMap[dir]
		mp.Location = newroom
		mp.Location.Broadcast(mp.Username+" has entered the room from the "+dir+".",mp.Username)
		mp.Location.AddPlayer(mp)
	}

	return retcode
}

func (mp *MudPlayer) LoadFromDB (db *muddatabase.MudDatabase, world *World, player *MudPlayer, playerid interface{}) error {
	query := ""
	var err error

	switch playerid.(type) {
	case string:
		query = "SELECT id,username,inventoryidslice,attackpower,defense,hp,maxhp,mp,maxmp,roomid,spawnlocationid FROM player WHERE username=$1"
	case int, int8, int32, int64:
		query = "SELECT id,username,inventoryidslice,attackpower,defense,hp,maxhp,mp,maxmp,roomid,spawnlocationid FROM player id=$1"
	default:
		err = errors.New("Player.LoadFromDB invalid ID.  ID must be string|int32")
	}

	rows,err := db.Connection.Query(db.Context,query,playerid)

	if err != nil {
		fmt.Println("Player query error: "+err.Error())
	}

	ret,err := pgx.CollectOneRow(rows,pgx.RowToStructByName[MudPlayerDB])

	if err != nil {
		fmt.Println("Player row error: "+err.Error())
	}

	// Build Room from RoomDB struct
	if err == nil {
		// Load DB values into MudPlayer object
		player.ID = ret.ID
		player.Username = ret.Username
		player.Location = world.RoomMap[ret.RoomID]
		player.Inventory = CreateInventory()
		player.AttackPower = ret.AttackPower
		player.Defense = ret.Defense
		player.HP = ret.HP
		player.MaxHP = ret.MaxHP
		player.MP = ret.MP
		player.MaxMP = ret.MaxMP
		player.SpawnLocation = world.RoomMap[ret.SpawnLocationID]

		// Build inventory
		for iid,_ := range ret.InventoryIDSlice {
			i := Item{}
			sid,_ := strconv.ParseInt(iid[1:],10,32)
			player.Inventory.AddItemByQuantity(i.FetchFromDB(db,sid),ret.InventoryIDSlice[iid])
		}
	}

	return err
}

func (mp *MudPlayer) CountUsernameFromDB (db *muddatabase.MudDatabase, playerid interface{}) int32 {
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

	rows,err := db.Connection.Query(db.Context,query,playerid)

	if err != nil {
		fmt.Println("CountUsernameFromDB query error: "+err.Error())
	}

	usercount, err = pgx.CollectOneRow(rows, pgx.RowTo[int32])

	if err != nil {
		fmt.Println("CountUsernameFromDB scan error: "+err.Error())
	}

	return usercount
}

func (mp *MudPlayer) WriteToDB (db *muddatabase.MudDatabase) {
	query := ""

	if mp.ID == 0 {
		// User does not exist, do INSERT
		query = "INSERT INTO player (username,inventoryidslice,attackpower,defense,hp,maxhp,mp,maxmp,roomid,SpawnLocationID) VALUES (@username,@inventoryidslice,@attackpower,@defense,@hp,@maxhp,@mp,@maxmp,@roomid,@spawnlocationid);"

		args := pgx.NamedArgs{
			"username": mp.Username,
			"inventoryidslice": mp.Inventory.IDMap(),
			"attackpower": mp.AttackPower,
			"defense": mp.Defense,
			"hp": mp.HP,
			"maxhp": mp.MaxHP,
			"mp": mp.MP,
			"maxmp": mp.MaxMP,
			"roomid": mp.Location.ID,
			"spawnlocationid": mp.SpawnLocation.ID,
		}
		
		err := db.ExecWithArgs(query,args)
		if err != nil {
			fmt.Println("MudPlauer.WriteToDB INSERT error:"+err.Error())
		}
		  

	} else {
		// User exists, do UPDATE
		query = "UPDATE player SET username=@username, inventoryidslice=@inventoryidslice, attackpower=@attackpower, defense=@defense, hp=@hp, maxhp=@maxhp, mp=@mp, maxmp=@maxmp, roomid=@roomid, spawnlocationid=@spawnlocationid WHERE id=@playerid;"

		args := pgx.NamedArgs{
			"username": mp.Username,
			"inventoryidslice": mp.Inventory.IDMap(),
			"attackpower": mp.AttackPower,
			"defense": mp.Defense,
			"hp": mp.HP,
			"maxhp": mp.MaxHP,
			"mp": mp.MP,
			"maxmp": mp.MaxMP,
			"roomid": mp.Location.ID,
			"playerid": mp.ID,
			"spawnlocationid": mp.SpawnLocation.ID,
		}
		
		err := db.ExecWithArgs(query,args)

		if err != nil {
			fmt.Println("MudPlauer.WriteToDB UPDATE error:"+err.Error())
		}
		
	}
}

func (mp *MudPlayer) GetIDFromDB (db *muddatabase.MudDatabase) int32 {
	query := "SELECT id FROM player WHERE username=$1"
	var err error

	rows,err := db.Connection.Query(db.Context,query,mp.Username)

	if err != nil {
		fmt.Println("GetIDFromDB query error: "+err.Error())
	}

	userid, err := pgx.CollectOneRow(rows, pgx.RowTo[int32])

	if err != nil {
		fmt.Println("GetIDFromDB scan error: "+err.Error())
	}

	return userid
}

func (mp *MudPlayer) Respawn () {
	mp.Location.RemovePlayer(mp.Username)
	mp.HP = mp.MaxHP
	mp.MP = mp.MaxMP
	mp.Location = mp.SpawnLocation
	mp.Location.AddPlayer(mp)
	mp.Location.Broadcast(mp.Username+" has appeared in the room.",mp.Username)
	mp.SendMessage(mp.Location.LookDescription())

}