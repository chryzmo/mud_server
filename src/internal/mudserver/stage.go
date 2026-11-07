package mudserver

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
	"unicode/utf8"

	muddata "mud_server/src/internal/muddata"
	"mud_server/src/internal/worldfile"
)

/*
Event rooms (plans/event-systems.md, D1 and C3). A crowded room goes quiet: once its audience reaches
the room's quiet_at, SAY, emotes and socials are limited to one line every ten seconds per player,
except for game masters and performers the GM has given the stage voice. Those speak with STAGE,
which everyone in the room hears, so a speaker can still carry a room of two hundred. GM RAFFLE
draws a winner from the room or from everyone online; it awards nothing by itself.
*/

const (
	quietGap       = 10 * time.Second // between lines from one player in a quiet room
	stageGap       = 2 * time.Second  // between a performer's lines
	stageMax       = 300              // characters in one STAGE line
	flagStageVoice = "stage-voice"
)

// roomCrowd is a room's capacity settings from content; the zero value is the server default.
type roomCrowd = worldfile.RoomCrowd

// capacity is the most players the room holds: its own, or the world's hard cap. 0 means no limit. Loop only.
func (w *World) capacity(room *Room) int {
	if room.crowd.Capacity > 0 {
		return room.crowd.Capacity
	}
	return w.hardCap
}

// quiet reports whether the room's audience has reached its quiet threshold. Loop only.
func (r *Room) quiet() bool { return r.crowd.QuietAt > 0 && len(r.UserMap) >= r.crowd.QuietAt }

// hasStage is true for a GM or a player the GM gave the stage voice.
func (mp *MudPlayer) hasStage() bool { return mp.gm || mp.flag(flagStageVoice) }

// quietRefused is true, with the player told, when the room is quiet and they spoke less than
// quietGap ago. Loop only.
func (mp *MudPlayer) quietRefused(now time.Time) bool {
	if mp.Location == nil || !mp.Location.quiet() || mp.hasStage() {
		return false
	}
	if wait := quietGap - now.Sub(mp.lastQuiet); wait > 0 {
		mp.SendMessage(fmt.Sprintf("The room is too crowded to talk over. Try again in %d seconds, or listen for the stage.", int(wait/time.Second)+1))
		return true
	}
	mp.lastQuiet = now
	return false
}

// stage is STAGE <text>: a performer's line, heard by everyone in the room. Loop only.
func (mp *MudPlayer) stage(text string, now time.Time) {
	if !mp.hasStage() {
		mp.SendMessage("You do not have the stage voice.")
		return
	}
	text = strings.Join(strings.Fields(text), " ")
	switch {
	case text == "":
		mp.SendMessage("Usage: STAGE <what you say to the whole room>")
		return
	case utf8.RuneCountInString(text) > stageMax:
		mp.SendMessage(fmt.Sprintf("That is too long for the stage (at most %d characters).", stageMax))
		return
	case mp.refuseIfMuted(now):
		return
	case now.Sub(mp.lastStage) < stageGap:
		mp.SendMessage("Not so fast. The room is still taking in your last line.")
		return
	}
	mp.lastStage = now
	if mp.server != nil {
		text = mp.server.names.mask(text)
	}
	if mp.lineSink != nil && mp.Location != nil {
		mp.lineSink(mp.Location.Name, mp.Username, "[stage] "+text)
	}
	frame := encodeText(fmt.Sprintf("[Stage] %s: %s", styled(mp.Username), text))
	for _, p := range mp.Location.Players() {
		if p != mp && !p.ignores(mp) {
			p.sendFrame(frame)
		}
	}
	mp.sendFrame(frame)
}

func registerStage(cList map[string]*MudCommand) {
	cList["STAGE"] = &MudCommand{
		description: "Speak to the whole room, for performers chosen by the staff.  Usage: STAGE <text>",
		action: func(player *MudPlayer, arguments string) int {
			player.stage(arguments, time.Now())
			return muddata.MUD_OK
		},
	}
}

// gmStage is GM STAGE <name> ON|OFF: give a performer the stage voice, or take it back.
func (ms *MudServer) gmStage(gm *MudPlayer, args []string) string {
	if len(args) != 2 || (strings.ToUpper(args[1]) != "ON" && strings.ToUpper(args[1]) != "OFF") {
		gm.SendMessage("Usage: GM STAGE <name> ON|OFF")
		return "usage"
	}
	target := ms.findPlayerFold(args[0])
	if target == nil {
		gm.SendMessage("No one called " + args[0] + " is here.")
		return "not found"
	}
	on := strings.ToUpper(args[1]) == "ON"
	if on {
		target.setFlag(flagStageVoice)
		target.SendMessage("You have the stage voice. STAGE <text> reaches the whole room.")
		gm.SendMessage(target.Username + " has the stage voice.")
	} else {
		target.clearFlag(flagStageVoice)
		target.SendMessage("You no longer have the stage voice.")
		gm.SendMessage(target.Username + " no longer has the stage voice.")
	}
	return fmt.Sprintf("stage %s %s", target.Username, strings.ToLower(args[1]))
}

// gmRaffle is GM RAFFLE [ALL]: pick one guest at random from your room, or from everyone online,
// and say who. GMs and players who have dropped their connection are never drawn.
func (ms *MudServer) gmRaffle(gm *MudPlayer, args []string) string {
	all := len(args) > 0 && strings.ToUpper(args[0]) == "ALL"
	if len(args) > 1 || (len(args) == 1 && !all) {
		gm.SendMessage("Usage: GM RAFFLE [ALL]")
		return "usage"
	}
	var pool []*MudPlayer
	var audience []*MudPlayer
	if all {
		for _, p := range ms.players {
			audience = append(audience, p)
		}
	} else if gm.Location != nil {
		audience = gm.Location.Players()
	}
	for _, p := range audience {
		if !p.gm && !p.dormant {
			pool = append(pool, p)
		}
	}
	if len(pool) == 0 {
		gm.SendMessage("No one is here to draw from.")
		return "nobody"
	}
	winner := pool[rand.IntN(len(pool))]
	frame := encodeText(fmt.Sprintf("Raffle! The winner is %s.", styled(winner.Username)))
	for _, p := range audience {
		p.sendFrame(frame)
	}
	gm.SendMessage(fmt.Sprintf("Drew %s from %d.", winner.Username, len(pool)))
	return fmt.Sprintf("raffle won %s of %d", winner.Username, len(pool))
}
