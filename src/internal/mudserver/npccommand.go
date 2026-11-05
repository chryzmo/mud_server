package mudserver

import (
	"mud_server/src/internal/mudutils"
	"time"
)

type NPCCommand struct {
	action func(room *Room, baddie *BaddieNPC, now time.Time) int32
}

func GenerateNPCCommandList() map[int32]NPCCommand {
	npclist := make(map[int32]NPCCommand)

	// 1 - Orc
	c_orc := NPCCommand{
		action: func(room *Room, baddie *BaddieNPC, now time.Time) int32 {
			// If NPC does not have a target, select a target
			if baddie.TargetPC != nil && baddie.Location.GetPlayer(baddie.TargetPC.Username) == nil {
				// Target is no longer in the room
				baddie.TargetPC = nil
			}
			if baddie.TargetPC == nil {
				// Select one occupant and announce once. Broadcasting for every
				// candidate floods each client's queue in a crowded room.
				for _, p := range baddie.Location.Players() {
					baddie.TargetPC = p
					baddie.Location.Broadcast(mudutils.ColorString(baddie.Name, mudutils.COLOR_RED)+" looks at "+p.Username+".", "")
					break
				}
			} else {
				// If NPC has target, attack
				if baddie.TargetPC.HP > 0 {
					baddie.attack(baddie.TargetPC, now)
				} else {
					baddie.TargetPC = nil
				}
			}

			return 0
		},
	}

	npclist[1] = c_orc

	return npclist
}