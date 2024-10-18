package mudserver

type MudMessage struct {
	Message string `json:"message"`
	Command string `json:"command"`
	CommandParameter string `json:"commandparameter"`
	CommandTarget string `json:"commandtarget"`

	SenderPlayer *MudPlayer
}