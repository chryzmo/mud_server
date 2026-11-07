package mudserver

import (
	"fmt"
	"math/rand/v2"
	muddatabase "mud_server/src/internal/database"
	muddata "mud_server/src/internal/muddata"
	"mud_server/src/internal/mudutils"
	"strconv"
	"strings"
	"time"
)

type MudCommand struct {
	description string
	action      func(player *MudPlayer, arguments string) int
	gmOnly      bool // only for game masters: anyone else gets "Unknown command.", and HELP does not list it
}

// GenerateCommandList builds the command table. findPlayer looks up an online
// player by name; commands run on the game loop, which owns that list.
func GenerateCommandList(db *muddatabase.MudDatabase, findPlayer func(username string) *MudPlayer) map[string]*MudCommand {
	cList := make(map[string]*MudCommand)

	// SAY
	c_say := MudCommand{
		description: "Speak aloud to everyone in your current room.  Usage: SAY <message>",
		action: func(player *MudPlayer, arguments string) int {
			msg := arguments

			if msg == "" {
				player.SendMessage("You stand in silence.")
				return muddata.MUD_OK
			}

			if player.refuseIfMuted(time.Now()) {
				return muddata.MUD_OK
			}

			if !player.allowSay(time.Now()) {
				return muddata.MUD_OK
			}

			if player.server != nil && player.server.checkRepeat(player, msg, time.Now()) {
				return muddata.MUD_OK
			}

			message := "You say: \"" + msg + "\""
			player.SendMessage(message)

			if player.lineSink != nil && player.Location != nil {
				player.lineSink(player.Location.Name, player.Username, msg)
			}
			heard := encodeSay(player, msg)
			for _, target_mp := range player.Location.Players() {
				if target_mp.Username != player.Username && !target_mp.ignores(player) {
					target_mp.sendFrame(heard)
				}
			}
			player.said(msg)
			player.tourDid("say")

			return muddata.MUD_OK
		},
	}

	// LOOK
	c_look := MudCommand{
		description: "Describe your current room, its exits, monsters, and other players.  LOOK AT <thing> looks closely at something.",
		action: func(player *MudPlayer, arguments string) int {
			if strings.TrimSpace(arguments) != "" {
				player.examine(arguments)
				return muddata.MUD_OK
			}
			player.SendLook()
			player.tourDid("look")
			return muddata.MUD_OK
		},
	}

	// TALK and ASK
	c_talk := MudCommand{
		description: "Talk to a friendly character.  Usage: TALK <person>  (just TALK if there is only one)",
		action: func(player *MudPlayer, arguments string) int {
			player.talk(arguments)
			return muddata.MUD_OK
		},
	}
	c_ask := MudCommand{
		description: "Ask a friendly character about something.  Usage: ASK <person> ABOUT <topic>",
		action: func(player *MudPlayer, arguments string) int {
			player.ask(arguments)
			return muddata.MUD_OK
		},
	}

	// EXAMINE
	c_examine := MudCommand{
		description: "Look closely at something: an object, an item, a player or a monster.  Usage: EXAMINE <thing>  (X for short; EXAMINE MY <item> looks in your pack)",
		action: func(player *MudPlayer, arguments string) int {
			player.examine(arguments)
			return muddata.MUD_OK
		},
	}

	// MOVE
	c_move := MudCommand{
		description: "Move in a direction.  Usage: MOVE <N|S|E|W|U|D>",
		action: func(player *MudPlayer, arguments string) int {
			direction := arguments

			switch player.MoveRoom(direction) {
			case muddata.ERR_ROOM_DNE:
				player.SendMessage("You cannot move there.\n")
			case muddata.ERR_ROOM_FULL:
				player.SendMessage("It's too crowded to go that way right now.\n")
			case retRoomLocked:
				// the gate's own message was sent
			default:
				player.dirty = true
				// Send description of new room
				player.SendLook()
				player.entered()
				player.tourDid("move")
			}

			return muddata.MUD_OK
		},
	}

	// WHISPER
	c_whisper := MudCommand{
		description: "Send a private message to any online player.  Usage: WHISPER <player> <message>",
		action: func(player *MudPlayer, arguments string) int {
			// WHISPER <target> <message>
			if player.refuseIfMuted(time.Now()) {
				return muddata.MUD_OK
			}
			words := strings.Split(arguments, " ")
			target := words[0]
			message := strings.Join(words[1:], " ")

			if target == player.Username {
				player.SendMessage("Talking to yourself, eh?")
			} else {
				targetPlayer := findPlayer(target)
				player.SendWhisper(target, message, true, targetPlayer)

				if targetPlayer == nil {
					player.SendMessage("Player '" + target + "' is not online.\n")
				} else {
					if !targetPlayer.ignores(player) { // the sender is not told: it only reads as unanswered
						targetPlayer.SendWhisper(player.Username, message, false, player)
					}
				}
			}

			return muddata.MUD_OK
		},
	}

	// GET
	c_get := MudCommand{
		description: "Pick up an item from the room.  Usage: GET <item>",
		action: func(player *MudPlayer, arguments string) int {
			itemname := strings.ToUpper(arguments)

			i := player.Location.Inventory.GetItem(itemname)
			if i == nil {
				player.SendMessage("You do not see that item here.")
			} else {
				player.SendMessage("You pick up " + i.Name + ".")

				p_item := i.createCopy()
				p_item.Quantity = 1
				player.Inventory.AddItem(p_item)

				if !i.Persistent {
					player.Location.Inventory.RemoveItem(i)
					player.Location.dirty = true
				}
				player.dirty = true // saved in the next batch (persist.go)
				player.Location.roomChanged()
			}

			return muddata.MUD_OK
		},
	}

	// ATTACK
	c_attack := MudCommand{
		description: "Attack a monster.  Repeating ATTACK continues on the same target.  Usage: ATTACK <monster>",
		action: func(player *MudPlayer, arguments string) int {

			if arguments != "" {
				npc_target_name := strings.ToUpper(arguments)
				player.NPCTarget = player.Location.BaddieMap[npc_target_name]
			}

			if player.NPCTarget == nil {
				player.SendMessage("You must have a target to attack.")
			} else if player.NPCTarget.Location == player.Location {
				// Perform attack!
				attackRoll := rand.IntN(100)
				effAtk := player.EffectiveAttackPower()
				if attackRoll > player.NPCTarget.Defense {
					player.NPCTarget.HP -= effAtk
					player.SendMessage("You hit " + player.NPCTarget.Name + " for " + strconv.Itoa(effAtk) + " points of damage!")
					player.Location.Broadcast(player.Username+" hits "+player.NPCTarget.Name+" for "+strconv.Itoa(effAtk)+" points of damage!", player.Username)
					if player.NPCTarget.HP <= 0 {
						player.SendMessage("You kill " + player.NPCTarget.Name + "!")
						player.killedMonster()
						player.Location.KillNPC(player.NPCTarget.Name, player.Username)
						player.NPCTarget = nil
						player.dirty = true // saved in the next batch (persist.go)
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
	c_drop := MudCommand{
		description: "Drop an item from your inventory into the room.  Usage: DROP <item>",
		action: func(player *MudPlayer, arguments string) int {
			itemname := strings.ToUpper(arguments)

			i := player.Inventory.GetItem(itemname)
			if i == nil {
				player.SendMessage("You do not have that item.")
			} else if i.Bound {
				player.SendMessage("The " + i.Name + " is bound to you; you cannot drop it.")
			} else {
				player.SendMessage("You drop " + i.Name + ".")

				p_item := i.createCopy()
				p_item.Quantity = 1
				player.Location.Inventory.AddItem(p_item)

				player.Inventory.RemoveItem(i)
				player.dirty = true
				player.Location.dirty = true
				player.Location.roomChanged()
			}

			return muddata.MUD_OK
		},
	}

	// INVENTORY
	c_inventory := MudCommand{
		description: "List everything you are carrying.",
		action: func(player *MudPlayer, arguments string) int {
			if player.Inventory.isEmpty() {
				player.SendInventory("Your inventory is empty.")
			} else {
				player.SendInventory("Your inventory contains:\n\n" + player.Inventory.ListAsString(true))
			}

			return muddata.MUD_OK
		},
	}

	// STATS
	c_stats := MudCommand{
		description: "Show your current HP, MP, attack, and defense, including any equipment bonuses.",
		action: func(player *MudPlayer, arguments string) int {
			atkBonus := player.Equipment.TotalBonusAttackPower()
			defBonus := player.Equipment.TotalBonusDefense()

			fmtStat := func(label string, effective, bonus int) string {
				valStr := strconv.Itoa(effective)
				if bonus > 0 {
					valStr = mudutils.BoldString(fmt.Sprintf("%d (+%d from equipment)", effective, bonus))
				}
				return fmt.Sprintf("  %-12s %s\n", label+":", valStr)
			}

			msg := "Your stats:\n"
			msg += fmt.Sprintf("  %-12s %d / %d\n", "HP:", player.HP, player.EffectiveMaxHP())
			msg += fmt.Sprintf("  %-12s %d / %d\n", "MP:", player.MP, player.EffectiveMaxMP())
			msg += fmtStat("Attack", player.EffectiveAttackPower(), atkBonus)
			msg += fmtStat("Defense", player.EffectiveDefense(), defBonus)
			msg += "\n"
			player.SendMessage(msg)
			player.SendStatUpdate()
			return muddata.MUD_OK
		},
	}

	// EQUIP
	// EQUIP          — list what the player has equipped
	// EQUIP <item>   — equip a named item from inventory
	c_equip := MudCommand{
		description: "Equip an item from your inventory, or show what you have equipped.  Usage: EQUIP [item name]",
		action: func(player *MudPlayer, arguments string) int {
			if arguments == "" {
				// Show currently equipped items
				msg := "Your equipment:\n"
				for _, slot := range player.Equipment.AllSlots() {
					if slot.Item != nil {
						msg += fmt.Sprintf("  %-8s %s %s\n", "["+slot.Name+"]", slot.Item.Name, slot.Item.BonusSummary())
					} else {
						msg += fmt.Sprintf("  %-8s (empty)\n", "["+slot.Name+"]")
					}
				}
				msg += "\n"
				player.SendMessage(msg)
				return muddata.MUD_OK
			}

			itemName := strings.ToUpper(arguments)

			// Check player has it in inventory
			invItem := player.Inventory.GetItem(itemName)
			if invItem == nil {
				player.SendMessage("You don't have a " + arguments + " in your inventory.\n")
				return muddata.MUD_OK
			}

			// Look up in equipment catalog
			equipItem := FetchEquipmentByItemName(db, itemName)
			if equipItem == nil {
				player.SendMessage(arguments + " is not equippable.\n")
				return muddata.MUD_OK
			}

			// Determine the target slot; rings fill ring1 first, then ring2
			targetSlot := equipItem.Slot
			if targetSlot == "ring" {
				if player.Equipment.Ring1 == nil {
					targetSlot = "ring1"
				} else {
					targetSlot = "ring2"
				}
			}

			slotPtr := player.Equipment.GetSlotPtr(targetSlot)
			if slotPtr == nil {
				player.SendMessage("Unknown equipment slot: " + targetSlot + "\n")
				return muddata.MUD_OK
			}

			// Both ring slots full
			if targetSlot == "ring2" && player.Equipment.Ring2 != nil {
				player.SendMessage("Both ring slots are occupied. Unequip a ring first.\n")
				return muddata.MUD_OK
			}

			// Unequip existing item in that slot → return to inventory
			if *slotPtr != nil {
				old := *slotPtr
				oldItem := &Item{ID: old.ItemID, Name: old.Name, Quantity: 1}
				player.Inventory.AddItem(oldItem)
				player.SendMessage("You unequip " + old.Name + ".\n")
			}

			// Equip the new item
			*slotPtr = equipItem
			player.Inventory.RemoveItem(invItem)
			player.SendMessage("You equip " + equipItem.Name + ". " + equipItem.BonusSummary() + "\n")
			player.SendStatUpdate()
			player.dirty = true // saved in the next batch (persist.go)
			return muddata.MUD_OK
		},
	}

	// UNEQUIP <slot>  — remove gear from the named slot and return it to inventory
	c_unequip := MudCommand{
		description: "Remove equipped gear from a slot and return it to your inventory.  Usage: UNEQUIP <slot>  (slots: weapon shield helmet chest legs hands ring1 ring2 neck)",
		action: func(player *MudPlayer, arguments string) int {
			slotName := strings.ToLower(strings.TrimSpace(arguments))
			if slotName == "" {
				player.SendMessage("Usage: UNEQUIP <slot>\nSlots: weapon shield helmet chest legs hands ring1 ring2 neck\n")
				return muddata.MUD_OK
			}

			slotPtr := player.Equipment.GetSlotPtr(slotName)
			if slotPtr == nil {
				player.SendMessage("Unknown slot '" + arguments + "'. Valid slots: weapon shield helmet chest legs hands ring1 ring2 neck\n")
				return muddata.MUD_OK
			}

			if *slotPtr == nil {
				player.SendMessage("Nothing equipped in " + slotName + ".\n")
				return muddata.MUD_OK
			}

			old := *slotPtr
			oldItem := &Item{ID: old.ItemID, Name: old.Name, Quantity: 1}
			player.Inventory.AddItem(oldItem)
			*slotPtr = nil
			player.SendMessage("You unequip " + old.Name + " and stow it in your inventory.\n")
			player.SendStatUpdate()
			player.dirty = true // saved in the next batch (persist.go)
			return muddata.MUD_OK
		},
	}

	// HELP
	c_help := MudCommand{
		description: "Show all available commands and what they do.",
		action: func(player *MudPlayer, arguments string) int {
			// Collect and sort command names for a stable, readable listing.
			names := make([]string, 0, len(cList))
			for name, c := range cList {
				if c.gmOnly && !player.gm {
					continue
				}
				names = append(names, name)
			}
			// Simple insertion sort — command list is small.
			for i := 1; i < len(names); i++ {
				for j := i; j > 0 && names[j] < names[j-1]; j-- {
					names[j], names[j-1] = names[j-1], names[j]
				}
			}

			msg := "Available commands:\n\n"
			for _, name := range names {
				msg += mudutils.ColorString(name, mudutils.COLOR_GREEN) + " — " + cList[name].description + "\n"
			}
			msg += "\n"
			player.SendMessage(msg)
			return muddata.MUD_OK
		},
	}

	// Load commands into CommandList
	cList["SAY"] = &c_say
	cList["LOOK"] = &c_look
	cList["EXAMINE"] = &c_examine
	cList["TALK"] = &c_talk
	cList["ASK"] = &c_ask
	cList["MOVE"] = &c_move
	cList["GET"] = &c_get
	cList["DROP"] = &c_drop
	cList["INVENTORY"] = &c_inventory
	cList["ATTACK"] = &c_attack
	cList["WHISPER"] = &c_whisper
	cList["STATS"] = &c_stats
	cList["EQUIP"] = &c_equip
	cList["UNEQUIP"] = &c_unequip
	cList["HELP"] = &c_help
	cList["?"] = &c_help
	registerRuleVerbs(cList)
	registerGuide(cList)
	registerTour(cList)

	return cList
}
