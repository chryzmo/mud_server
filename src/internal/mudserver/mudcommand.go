package mudserver

import (
	"fmt"
	"math/rand/v2"
	muddata "mud_server/src/internal/muddata"
	"mud_server/src/internal/mudutils"
	"strconv"
	"strings"
)

type MudCommand struct {
	action func(player *MudPlayer, arguments string) (int)
}

func GenerateCommandList() (map[string]*MudCommand) {
	cList := make(map[string]*MudCommand)

	// TEMPLATE
	/*
	c_name := MudCommand {
		action: func(player *MudPlayer, arguments string) (int) {
			fmt.Println("Do stuff")
			return muddata.MUD_OK
		},
	}
	*/

	// SAY
	c_say := MudCommand {
		action: func(player *MudPlayer, arguments string) (int) {
			msg := arguments

			if msg == "" {
				player.SendMessage("You stand in silence.")
				return muddata.MUD_OK
			}

			message := "You say: \""+msg+"\""
			player.SendMessage(message)

			for _,target_mp := range player.Location.UserMap {
				if target_mp.Username != player.Username {
					message := mudutils.ColorString(player.Username, mudutils.COLOR_BLUE)+" says: \""+msg+"\""
						target_mp.SendMessage(message)
				}
			}

			return muddata.MUD_OK
		},
	}

	// LOOK
	c_look := MudCommand {
		action: func(player *MudPlayer, arguments string) (int) {
			player.SendMessage(player.Location.LookDescription())
			return muddata.MUD_OK
		},
	}

	// MOVE
	c_move := MudCommand {
		action: func(player *MudPlayer, arguments string) (int) {
			direction := arguments
			
			if player.MoveRoom(direction) == muddata.ERR_ROOM_DNE {
				player.SendMessage("You cannot move there.\n")
			} else {
				// Send description of new room
				player.SendMessage(player.Location.LookDescription())
			}

			return muddata.MUD_OK
		},
	}

	// WHISPER
	c_whisper := MudCommand {
		action: func(player *MudPlayer, arguments string) (int) {
			// WHISPER <target> <message>
			words := strings.Split(arguments, " ")
			target := words[0]
			message := strings.Join(words[1:], " ")

			if (target == player.Username) {
				player.SendMessage("Talking to yourself, eh?")
			} else {
				mudMessage := MudMessage{
					Command: "WHISPER",
					CommandTarget: target,
					CommandParameter: message,
					SenderPlayer: player,
				}
	
				fmt.Println(player.Location.GlobalWhisperChannel)
	
				player.Location.GlobalWhisperChannel <- mudMessage
				player.SendMessage(mudutils.ColorString("You whisper to "+target+", \""+message+"\"", mudutils.COLOR_MAGENTA))
			}
			
			return muddata.MUD_OK
		},
	}

	// GET
	c_get := MudCommand {
		action: func(player *MudPlayer, arguments string) (int) {
			itemname := strings.ToUpper(arguments)
			
			i := player.Location.Inventory.GetItem(itemname)
			if i == nil {
				player.SendMessage("You do not see that item here.")
			} else {
				player.SendMessage("You pick up "+i.Name+".")
				
				p_item := i.createCopy()
				p_item.Quantity = 1
				player.Inventory.AddItem(p_item)

				if ! i.Persistent {
					player.Location.Inventory.RemoveItem(i)
				}
			}

			return muddata.MUD_OK
		},
	}

	// ATTACK
	c_attack := MudCommand {
		action: func(player *MudPlayer, arguments string) (int) {

			if arguments != "" {
				npc_target_name := strings.ToUpper(arguments)

				player.NPCTarget = player.Location.BaddieMap[npc_target_name]
			}

			if player.NPCTarget == nil {
				player.SendMessage("You must have a target to attack.")
			} else if player.NPCTarget.Location == player.Location {
				// Perform attack!
				attackRoll := rand.IntN(100)
				if attackRoll > player.NPCTarget.Defense {
					player.NPCTarget.HP -= player.AttackPower
					player.SendMessage("You hit "+player.NPCTarget.Name+" for "+strconv.Itoa(player.AttackPower)+" points of damage!")
					player.Location.Broadcast(player.Username+" hits "+player.NPCTarget.Name+" for "+strconv.Itoa(player.AttackPower)+ " points of damage!",player.Username)
					if player.NPCTarget.HP <= 0 {
						player.SendMessage("You kill "+player.NPCTarget.Name+"!")
						player.Location.KillNPC(player.NPCTarget.Name,player.Username)
						player.NPCTarget = nil
					}
				}
				
			} else {
				player.NPCTarget = nil
				player.SendMessage("You do not see that target here.")
			}

			return muddata.MUD_OK
		},
	}

	// DROP
	c_drop := MudCommand {
		action: func(player *MudPlayer, arguments string) (int) {
			itemname := strings.ToUpper(arguments)
			
			i := player.Inventory.GetItem(itemname)
			if i == nil {
				player.SendMessage("You do not have that item.")
			} else {
				player.SendMessage("You drop "+i.Name+".")
				
				p_item := i.createCopy()
				p_item.Quantity = 1
				player.Location.Inventory.AddItem(p_item)

				player.Inventory.RemoveItem(i)
			}

			return muddata.MUD_OK
		},
	}

	// INVENTORY
	c_inventory := MudCommand {
		action: func(player *MudPlayer, arguments string) (int) {
			if player.Inventory.isEmpty() {
				player.SendMessage("Your inventory is empty.")
			} else {
				player.SendMessage("Your inventory contains:\n\n"+player.Inventory.ListAsString(true))
			}

			return muddata.MUD_OK
		},
	}

	// Load commands into CommandList
	cList["SAY"] = &c_say
	cList["LOOK"] = &c_look
	cList["MOVE"] = &c_move
	cList["GET"] = &c_get
	cList["DROP"] = &c_drop
	cList["INVENTORY"] = &c_inventory
	cList["ATTACK"] = &c_attack
	cList["WHISPER"] = &c_whisper

	return cList
}