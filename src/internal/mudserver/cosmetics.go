package mudserver

import (
	"fmt"
	"strings"

	"mud_server/src/internal/muddata"
	"mud_server/src/internal/worldfile"
)

/*
Cosmetics (plans/event-systems.md, section 5 and decision 33). A name colour or badge is earned: a
quest's unlock rule effect grants one (rules.go), and a boss may drop one. Cosmetics are keys into a
fixed set the client draws (worldfile.Colors and Badges); frames that name a player carry the active
keys. A player's unlocks and active choices are player_state rows (kinds unlock and cosmetic), written
at once like the rest of it.
*/

// look returns the keys of the player's active colour and badge, for frames that name them.
func (mp *MudPlayer) look() (color, badge string) {
	if mp == nil || mp.state == nil {
		return "", ""
	}
	return mp.state.active["color"], mp.state.active["badge"]
}

func (mp *MudPlayer) unlocked(kind, key string) bool { return mp.st().unlocks[kind+":"+key] }

// unlockCosmetic adds a colour or badge to the player's set and makes it the active one.
func (mp *MudPlayer) unlockCosmetic(kind, key string) {
	if !mp.unlocked(kind, key) {
		mp.st().unlocks[kind+":"+key] = true
		mp.persistState("unlock", kind+":"+key, "1", false)
	}
	mp.chooseCosmetic(kind, key)
}

// grantCosmetic is a quest reward: the colour or badge is added to the player's set and worn, with
// a message and a fresh cosmetics frame. One the player already owns changes nothing, so a rule that
// runs again does not repeat the news or change what they chose to wear. Loop only.
func (mp *MudPlayer) grantCosmetic(kind, key string) {
	if mp.unlocked(kind, key) {
		return
	}
	mp.unlockCosmetic(kind, key)
	if kind == "color" {
		mp.SendMessage(fmt.Sprintf("Your name now shows in %s. COSMETIC changes it.", key))
	} else {
		mp.SendMessage(fmt.Sprintf("You earned the %s badge. COSMETIC changes it.", key))
	}
	mp.SendCosmetics()
}

// chooseCosmetic makes key the active colour or badge, or clears it when key is empty.
func (mp *MudPlayer) chooseCosmetic(kind, key string) {
	if key == "" {
		if _, ok := mp.st().active[kind]; ok {
			delete(mp.st().active, kind)
			mp.persistState("cosmetic", kind, "", true)
		}
		return
	}
	mp.st().active[kind] = key
	mp.persistState("cosmetic", kind, key, false)
}

// owned lists the player's unlocked keys of one kind in the fixed order.
func (mp *MudPlayer) owned(kind string, all []string) []string {
	out := []string{}
	for _, k := range all {
		if mp.unlocked(kind, k) {
			out = append(out, k)
		}
	}
	return out
}

// SendCosmetics tells the client its own unlocks and active choices (for the picker).
func (mp *MudPlayer) SendCosmetics() {
	color, badge := mp.look()
	mp.sendFrame(encode(cosmeticsMsg{T: msgCosmetics,
		Colors: mp.owned("color", worldfile.Colors), Badges: mp.owned("badge", worldfile.Badges),
		Color: color, Badge: badge}))
}

// sendCosmeticsIfAny is for entering the world: a player with nothing unlocked is sent nothing,
// which the client reads as an empty set, so most players see no extra frame.
func (mp *MudPlayer) sendCosmeticsIfAny() {
	if mp.state != nil && len(mp.state.unlocks) > 0 {
		mp.SendCosmetics()
	}
}

// cosmeticCommand is COSMETIC: list what you have, or pick the colour or badge to wear.
func cosmeticCommand(p *MudPlayer, arg string) {
	arg = strings.ToLower(strings.TrimSpace(arg))
	colors, badges := p.owned("color", worldfile.Colors), p.owned("badge", worldfile.Badges)
	switch {
	case arg == "":
		if len(colors)+len(badges) == 0 {
			p.SendMessage("You have no cosmetics yet. Quests and bosses give them.")
			return
		}
		color, badge := p.look()
		p.SendMessage("Colours: " + describeOwned(colors, color) + "\nBadges: " + describeOwned(badges, badge) +
			"\nUsage: COSMETIC <name> to wear one, COSMETIC OFF to clear both.")
	case arg == "off":
		p.chooseCosmetic("color", "")
		p.chooseCosmetic("badge", "")
		p.SendMessage("Cosmetics cleared.")
		p.announceLook()
	default:
		kind := ""
		switch {
		case p.unlocked("color", arg):
			kind = "color"
		case p.unlocked("badge", arg):
			kind = "badge"
		}
		if kind == "" {
			p.SendMessage("You do not have a cosmetic called '" + arg + "'. COSMETIC lists yours.")
			return
		}
		p.chooseCosmetic(kind, arg)
		p.SendMessage("You now wear " + arg + ".")
		p.announceLook()
	}
}

func (mp *MudPlayer) announceLook() { mp.SendCosmetics() }

func describeOwned(owned []string, active string) string {
	if len(owned) == 0 {
		return "none"
	}
	out := make([]string, len(owned))
	for i, k := range owned {
		out[i] = k
		if k == active {
			out[i] += " (worn)"
		}
	}
	return strings.Join(out, ", ")
}

func registerCosmetics(ms *MudServer, cList map[string]*MudCommand) {
	cList["COSMETIC"] = &MudCommand{description: "List or choose your name colour and badge.  Usage: COSMETIC [name|OFF]", action: func(p *MudPlayer, arg string) int {
		cosmeticCommand(p, arg)
		return muddata.MUD_OK
	}}
}
