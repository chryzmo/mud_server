package mudserver

import (
	"time"

	muddata "mud_server/src/internal/muddata"
)

// eventsCommand is EVENTS: what is on now and what is coming.
func (ms *MudServer) eventsCommand() *MudCommand {
	return &MudCommand{
		description: "What is happening now and what is coming up.  Usage: EVENTS",
		action: func(player *MudPlayer, arguments string) int {
			if ms.sched == nil {
				player.SendMessage("Nothing is scheduled right now.")
			} else {
				player.SendMessage(ms.sched.summary(time.Now()))
			}
			return muddata.MUD_OK
		},
	}
}
