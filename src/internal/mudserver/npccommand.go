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
			if baddie.passive && baddie.TargetPC != nil {
				// A passive monster fights only those who have hit it: drop a target that has left or fallen.
				baddie.stillFighting()
				found := false
				for _, h := range baddie.hitters {
					found = found || h == baddie.TargetPC
				}
				if !found {
					baddie.TargetPC = nil
				}
			}
			if baddie.TargetPC == nil {
				// Select one fighter and announce once. Broadcasting for every
				// candidate floods each client's queue in a crowded room.
				if p := baddie.pickTarget(); p != nil {
					baddie.TargetPC = p
					baddie.Location.Broadcast(mudutils.ColorString(baddie.Name, mudutils.COLOR_RED)+" looks at "+p.Username+".", "")
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