package mudserver

/*
	Message structure for messages coming from players
*/

type MudMessage struct {
	Command          string `json:"command"`
	CommandParameter string `json:"commandparameter"`
	CommandTarget    string `json:"commandtarget"`

	SenderPlayer *MudPlayer
}