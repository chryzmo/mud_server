package mudserver

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"mud_server/src/internal/muddata"
	"mud_server/src/internal/worldfile"
)

/*
Sticker codes and cosmetics (plans/event-systems.md section 5). REDEEM <code> looks a code up by
its HMAC (the content holds hashes, never codes) and grants what it names once per player: a name
colour or badge, an item, a flag, a message. Cosmetics are keys into a fixed set the client draws
(worldfile.Colors and Badges); frames that name a player carry the active keys. Wrong guesses are
limited per player (five a minute, then a cooldown that doubles each time) and across the server,
and every failure reads alike. A player's unlocks, active choices and redeemed codes are
player_state rows (kinds unlock, cosmetic, redeemed), written at once like the rest of it.
*/

const (
	redeemFailures  = 5                // wrong codes allowed in redeemWindow
	redeemWindow    = time.Minute      // ...before the cooldown starts
	redeemCooldown  = time.Minute      // the first cooldown; it doubles with each repeat
	redeemMaxLock   = 10 * time.Minute // the longest cooldown
	redeemForgive   = 10 * time.Minute // a quiet spell this long forgets earlier cooldowns
	redeemPerSecond = 10               // attempts across the whole server
	redeemBurst     = 20
	redeemFail      = "That code does not work."
)

// redeemGuard is a player's wrong-code record. Loop only.
type redeemGuard struct {
	fails       []time.Time
	strikes     int
	lockedUntil time.Time
	lastFail    time.Time
}

// locked reports how long until the player may try a code again; zero means now.
func (g *redeemGuard) locked(now time.Time) time.Duration {
	if now.Before(g.lockedUntil) {
		return g.lockedUntil.Sub(now)
	}
	return 0
}

// fail records a wrong code and starts a cooldown when there have been too many.
func (g *redeemGuard) fail(now time.Time) {
	if now.Sub(g.lastFail) > redeemForgive {
		g.strikes = 0
	}
	g.lastFail = now
	kept := g.fails[:0]
	for _, t := range g.fails {
		if now.Sub(t) < redeemWindow {
			kept = append(kept, t)
		}
	}
	g.fails = append(kept, now)
	if len(g.fails) >= redeemFailures {
		lock := min(redeemCooldown<<g.strikes, redeemMaxLock)
		g.strikes = min(g.strikes+1, 10)
		g.lockedUntil = now.Add(lock)
		g.fails = nil
	}
}

// codeSecret is the HMAC key for sticker codes, from MUD_CODE_SECRET. Without it no code can be redeemed.
func codeSecretFromEnv() string { return os.Getenv("MUD_CODE_SECRET") }

// codeIndex maps a code's hash to its key, for lookups.
func codeIndex(codes map[string]worldfile.Code) map[string]string {
	idx := make(map[string]string, len(codes))
	for key, c := range codes {
		idx[c.Hash] = key
	}
	return idx
}

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

func (ms *MudServer) redeem(p *MudPlayer, arg string, now time.Time) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		p.SendMessage("Usage: REDEEM <code>  (the code on a sticker, like ABCD-2345)")
		return
	}
	if wait := p.redeem.locked(now); wait > 0 {
		p.SendMessage(fmt.Sprintf("Too many wrong codes. Try again in about %s.", roundUp(wait)))
		return
	}
	if ms.redeemBucket.capacity == 0 {
		ms.redeemBucket = newLimiter(redeemPerSecond, redeemBurst, now)
	}
	if ok, _ := ms.redeemBucket.allow(now); !ok {
		p.SendMessage("Codes are busy right now. Try again in a moment.")
		return
	}
	key := ""
	if n, ok := worldfile.NormalizeCode(arg); ok && ms.codeSecret != "" {
		key = ms.codeByHash[worldfile.HashCode(ms.codeSecret, n)]
	}
	if key == "" {
		p.redeem.fail(now)
		p.SendMessage(redeemFail)
		return
	}
	if p.st().redeemed[key] {
		p.SendMessage("You have already used that code.") // a retry after a dropped reply is safe
		return
	}
	p.st().redeemed[key] = true
	p.persistState("redeemed", key, "1", false)
	c := ms.codes[key]
	if c.Message != "" {
		p.SendMessage(c.Message)
	}
	if c.Color != "" {
		p.unlockCosmetic("color", c.Color)
		p.SendMessage(fmt.Sprintf("Your name now shows in %s. COSMETIC changes it.", c.Color))
	}
	if c.Badge != "" {
		p.unlockCosmetic("badge", c.Badge)
		p.SendMessage(fmt.Sprintf("You earned the %s badge. COSMETIC changes it.", c.Badge))
	}
	if c.Item != "" {
		p.giveItemKey(c.Item)
	}
	if c.Flag != "" {
		p.setFlag(c.Flag)
	}
	if c.Message == "" && c.Color == "" && c.Badge == "" && c.Item == "" {
		p.SendMessage("Code accepted.")
	}
	if c.Color != "" || c.Badge != "" {
		p.SendCosmetics()
	}
}

// cosmeticCommand is COSMETIC: list what you have, or pick the colour or badge to wear.
func cosmeticCommand(p *MudPlayer, arg string) {
	arg = strings.ToLower(strings.TrimSpace(arg))
	colors, badges := p.owned("color", worldfile.Colors), p.owned("badge", worldfile.Badges)
	switch {
	case arg == "":
		if len(colors)+len(badges) == 0 {
			p.SendMessage("You have no cosmetics yet. Stickers around the con have codes: REDEEM <code>.")
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

func (ms *MudServer) progressText(p *MudPlayer) string {
	if len(ms.codes) == 0 {
		return "There are no stickers to find."
	}
	found := 0
	keys := make([]string, 0, len(ms.codes))
	for k := range ms.codes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if p.st().redeemed[k] {
			found++
		}
	}
	return fmt.Sprintf("Stickers found: %d of %d.", found, len(ms.codes))
}

func registerCosmetics(ms *MudServer, cList map[string]*MudCommand) {
	cList["REDEEM"] = &MudCommand{description: "Use a sticker code.  Usage: REDEEM <code>", action: func(p *MudPlayer, arg string) int {
		ms.redeem(p, arg, time.Now())
		return muddata.MUD_OK
	}}
	cList["COSMETIC"] = &MudCommand{description: "List or choose your name colour and badge.  Usage: COSMETIC [name|OFF]", action: func(p *MudPlayer, arg string) int {
		cosmeticCommand(p, arg)
		return muddata.MUD_OK
	}}
	cList["PROGRESS"] = &MudCommand{description: "How many stickers you have found.", action: func(p *MudPlayer, arg string) int {
		p.SendMessage(ms.progressText(p))
		return muddata.MUD_OK
	}}
}
