package mudserver

/*
	UsersInZone key is Player.Username
	ExitList key is <direction> of exit
*/

type Room struct {
	Name string
	Description string
	UsersInZone map[string]*MudPlayer
	ExitList map[string]*Room
	Inventory *Inventory
}

func CreateRoom(name string, description string) (*Room) {
	r := &Room{
		Name: name,
		Description: description,
		UsersInZone: make(map[string]*MudPlayer),
		ExitList: make(map[string]*Room),
		Inventory: CreateInventory(),
	}

	return r
}

func (r Room) LookDescription() (string) {
	desc := "You are in '"+r.Name+"'.\n\n"+r.Description+"\n\nThe players here are:\n"
	
	for k := range r.UsersInZone {
		desc += k+"\n"
	}

	if !r.Inventory.isEmpty() {
		desc += "\nYou see:\n"
		desc += r.Inventory.ListAsString(false)
	}

	return desc
}

func (r *Room) RemovePlayer(username string) () {
	delete(r.UsersInZone,username)
}

func (r *Room) AddPlayer(mp *MudPlayer) () {
	r.UsersInZone[mp.Username] = mp
}
