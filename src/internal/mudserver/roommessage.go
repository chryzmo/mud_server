package mudserver

/*
	Message structure for messages regarding actions happening within a room
*/

type RoomMessage struct {
	Command          string `json:"command"`
	CommandParameter string `json:"commandparameter"`
	CommandTarget    string `json:"commandtarget"`
	SenderUsername	 string `json:"senderusername"`
}