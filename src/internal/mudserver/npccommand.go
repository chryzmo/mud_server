package mudserver

type NPCCommand struct {
	action func(room *Room, baddie *BaddieNPC) int32
}

func GenerateNPCCommandList() map[int32]NPCCommand {
	npclist := make(map[int32]NPCCommand)

	// 1 - Orc
	c_orc := NPCCommand{
		action: func(room *Room, baddie *BaddieNPC) int32 {
			// If NPC does not have a target, select a target
			if baddie.TargetPC != nil && baddie.Location.UserMap[baddie.TargetPC.Username] == nil {
				// Target is no longer in the room
				baddie.TargetPC = nil
			}
			if baddie.TargetPC == nil {
				if len(baddie.Location.UserMap) > 0 {
					// Janky way to select first PC in Room Map
					for k, _ := range baddie.Location.UserMap {
						baddie.TargetPC = baddie.Location.UserMap[k]
						baddie.Location.Broadcast(baddie.Name+" looks at "+baddie.Location.UserMap[k].Username+".", "")
						break
					}
				}
			} else {
				// If NPC has target, attack
				if baddie.TargetPC.HP > 0 {
					baddie.attack(baddie.TargetPC)
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