package mudserver

import "strings"

/*
	UsersInZone key is Player.Username
	ExitList key is <direction> of exit
*/

type Room struct {
	Name string
	Description string
	RoomChannel chan(RoomMessage)
	UserMap map[string]*MudPlayer
	BaddieMap map[string]*BaddieNPC
	ExitMap map[string]*Room
	Inventory *Inventory
	BaddieSpawnerMap map[string]*BaddieSpawner
	//ticker *time.Ticker
}

func CreateRoom(name string, description string) (*Room) {
	r := &Room{
		Name: name,
		Description: description,
		RoomChannel: make(chan RoomMessage,5),
		UserMap: make(map[string]*MudPlayer),
		ExitMap: make(map[string]*Room),
		Inventory: CreateInventory(),
		BaddieSpawnerMap: make(map[string]*BaddieSpawner),
		BaddieMap: make(map[string]*BaddieNPC),
	}

	// Launch goroutine to continuously process broadcast messages
	go r.ProcessBroadcast()

	return r
}

/*func (room *Room) RunTicker () {
	room.ticker = time.NewTicker(time.Millisecond * 250) //TODO:  Server config?

	for range room.ticker.C {
		// check if RoomChannel is not empty
			// Perform action (broadcast)
	}
}*/

func (r Room) ProcessBroadcast() {
	for {
		msg := <-r.RoomChannel

		for _,player := range r.UserMap {
			if player.Username != msg.SenderUsername {
				player.SendMessage(msg.CommandParameter)
			}
		}
	}
}

func (r Room) Broadcast(msg string, senderusername string) () {
	roommsg := RoomMessage{
		Command: "BROADCAST",
		CommandParameter: msg,
		SenderUsername: senderusername,
	}

	r.RoomChannel <- roommsg
}

func (r Room) LookDescription() (string) {
	desc := "You are in '"+r.Name+"'.\n\n"+r.Description+"\n"
	
	if len(r.BaddieMap) > 0 {
		desc += "\nThe monsters here are:\n"

		for _,baddie := range r.BaddieMap {
			desc += baddie.Name+"\n"
		}
	}

	if len(r.UserMap) > 0 {
		desc += "\nThe players here are:\n"

		for k := range r.UserMap {
			desc += k+"\n"
		}
	}

	if !r.Inventory.isEmpty() {
		desc += "\nYou see:\n"
		desc += r.Inventory.ListAsString(false)
	}

	desc += "\n"

	return desc
}

func (r *Room) RemovePlayer(username string) () {
	delete(r.UserMap,username)
}

// Remove NPC and broadcast kill
func (r *Room) KillNPC(npcname string, attackername string) () {
	delete(r.BaddieMap,strings.ToUpper(npcname))
	r.Broadcast(attackername+" kills "+npcname+"!",attackername)
}

func (r *Room) AddPlayer(mp *MudPlayer) () {
	r.UserMap[mp.Username] = mp
}
