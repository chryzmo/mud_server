package muddata

const (
	MUD_OK              = iota
	ERR_ROOM_DNE        // Room does not exist
	ERR_USER_DNE        // User does not exist
	ERR_COMMAND_UNKNOWN // Command unknown
	ERR_INV_NOITEM      // Item not in inventory
)
