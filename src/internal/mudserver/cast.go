package mudserver

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"mud_server/src/internal/muddata"
	"mud_server/src/internal/mudutils"
	"mud_server/src/internal/worldfile"
)

/*
Spells, part two: casting (plans/event-systems.md, section 12). CAST <spell, or 1 to 3 for a readied slot>
[target]. Magic points regrow on their own, worked out from a timestamp when they are read, so a player at
rest costs nothing; cooldowns are timestamps too. A bolt hurts a monster for the caster's attack plus the
spell's power and ignores its defence roll; a mend heals a player; a ward adds defence for thirty seconds;
a fun spell only shows its lines to the room, so it is chatter and obeys the mute, the quiet room and the
repeat guard like a social. A kill by a spell pays XP and loot like any other kill.
*/

const (
	mpRegenEvery = 6 * time.Second
	wardLasts    = 30 * time.Second
)

// regenMP brings the player's magic up to date: one point for every mpRegenEvery since the stamp, up to
// the maximum. Loop only.
func (mp *MudPlayer) regenMP(now time.Time) {
	max := mp.EffectiveMaxMP()
	if mp.mpStamp.IsZero() || mp.MP >= max {
		mp.mpStamp = now
		return
	}
	gain := int(now.Sub(mp.mpStamp) / mpRegenEvery)
	if gain <= 0 {
		return
	}
	mp.MP = min(max, mp.MP+gain)
	mp.mpStamp = mp.mpStamp.Add(time.Duration(gain) * mpRegenEvery)
	if mp.MP >= max {
		mp.mpStamp = now
	}
}

// spendMP takes magic, starting the regrowth clock if the player was full. Loop only.
func (mp *MudPlayer) spendMP(cost int, now time.Time) {
	mp.regenMP(now)
	if mp.MP >= mp.EffectiveMaxMP() {
		mp.mpStamp = now
	}
	mp.MP -= cost
	mp.dirty = true
}

// castTarget splits "firebolt rat" into the spell words and the target words: the longest start of the
// line that names a known spell wins. A bare number picks a readied slot.
func (mp *MudPlayer) castSpell(arguments string) (key, target string, problem string) {
	fields := strings.Fields(arguments)
	if len(fields) == 0 {
		return "", "", "Cast what? CAST <spell or slot 1-3> [target]  (SPELLS lists yours)"
	}
	if n, err := strconv.Atoi(fields[0]); err == nil {
		if n < 1 || n > spellSlots {
			return "", "", fmt.Sprintf("There are %d slots: 1 to %d.", spellSlots, spellSlots)
		}
		if mp.st().slots[n-1] == "" {
			return "", "", fmt.Sprintf("Nothing is readied in slot %d.", n)
		}
		return mp.st().slots[n-1], strings.Join(fields[1:], " "), ""
	}
	for i := len(fields); i >= 1; i-- {
		k, several := mp.spellByName(strings.Join(fields[:i], " "))
		if k != "" {
			return k, strings.Join(fields[i:], " "), ""
		}
		if len(several) > 0 && i == len(fields) {
			return "", "", "Which do you mean: " + strings.Join(several, ", ") + "?"
		}
	}
	return "", "", "You do not know a spell called that. SPELLS lists yours."
}

// cast does CAST. Loop only.
func (ms *MudServer) cast(p *MudPlayer, arguments string, now time.Time) {
	if p.Location == nil || p.world == nil {
		return
	}
	key, target, problem := p.castSpell(arguments)
	if problem != "" {
		p.SendMessage(problem)
		return
	}
	sp := p.world.spells[key]
	if p.refuseIfMuted(now) {
		return
	}
	if until := p.cooldowns[key]; now.Before(until) {
		p.SendMessage(fmt.Sprintf("%s is not ready for another %d seconds.", sp.Name, int((until.Sub(now)+time.Second-1)/time.Second)))
		return
	}
	p.regenMP(now)
	if p.MP < sp.Cost {
		p.SendMessage(fmt.Sprintf("You do not have the magic for %s (it takes %d, you have %d).", sp.Name, sp.Cost, p.MP))
		return
	}
	switch sp.Effect {
	case "bolt":
		npc, why := p.monsterTarget(target)
		if npc == nil {
			p.SendMessage(why)
			return
		}
		p.spend(key, sp.Cost, sp.Cooldown, now)
		p.tellCast(sp, npc.Name)
		dmg := p.EffectiveAttackPower() + sp.Power
		npc.provoke(p)
		p.NPCTarget = npc
		npc.HP -= dmg
		p.SendMessage(fmt.Sprintf("%s hits %s for %d points of damage!", sp.Name, npc.Name, dmg))
		if npc.HP <= 0 {
			p.SendMessage("You kill " + npc.Name + "!")
			p.Location.defeated(npc, p)
			p.NPCTarget = nil
			p.dirty = true
		}
	case "mend":
		who := p
		if strings.TrimSpace(target) != "" && !strings.EqualFold(strings.TrimSpace(target), "me") && !strings.EqualFold(strings.TrimSpace(target), "self") {
			other, why := pickInRoom(p, target)
			if other == nil {
				p.SendMessage(why)
				return
			}
			who = other
		}
		if who.HP >= who.EffectiveMaxHP() {
			p.SendMessage(func() string {
				if who == p {
					return "You are"
				}
				return who.Username + " is"
			}() + " already unhurt.")
			return
		}
		p.spend(key, sp.Cost, sp.Cooldown, now)
		healed := min(sp.Power, who.EffectiveMaxHP()-who.HP)
		who.HP += healed
		who.dirty = true
		p.tellCast(sp, who.Username)
		if who == p {
			p.SendMessage(fmt.Sprintf("You are healed for %d. (HP %d/%d)", healed, p.HP, p.EffectiveMaxHP()))
		} else {
			p.SendMessage(fmt.Sprintf("%s is healed for %d.", who.Username, healed))
			who.SendMessage(fmt.Sprintf("%s heals you for %d. (HP %d/%d)", p.Username, healed, who.HP, who.EffectiveMaxHP()))
			who.SendStatUpdate()
		}
	case "ward":
		p.spend(key, sp.Cost, sp.Cooldown, now)
		p.wardBonus, p.wardUntil = sp.Power, now.Add(wardLasts)
		p.tellCast(sp, "")
		p.SendMessage(fmt.Sprintf("Your defence rises by %d for %d seconds.", sp.Power, int(wardLasts/time.Second)))
	case "fun":
		if !ms.chatterAllowed(p, now) {
			return
		}
		p.spend(key, sp.Cost, sp.Cooldown, now)
		p.tellCast(sp, "")
	}
	p.SendStatUpdate()
	p.SendSpells()
}

// spend pays for a cast: magic out, cooldown on. Loop only.
func (mp *MudPlayer) spend(key string, cost, cooldown int, now time.Time) {
	mp.spendMP(cost, now)
	if mp.cooldowns == nil {
		mp.cooldowns = map[string]time.Time{}
	}
	mp.cooldowns[key] = now.Add(time.Duration(cooldown) * time.Second)
}

// tellCast shows the spell's lines: the caster's, and the room's with {player} (the caster) and {target}
// filled in. Loop only.
func (mp *MudPlayer) tellCast(sp worldfile.Spell, target string) {
	fill := func(s string) string {
		return strings.NewReplacer("{player}", mp.Username, "{target}", target).Replace(s)
	}
	if strings.TrimSpace(sp.Cast) != "" {
		mp.SendMessage(mudutils.ColorString(fill(sp.Cast), mudutils.COLOR_GREEN))
	}
	if strings.TrimSpace(sp.Room) != "" {
		tellRoom(mp, fill(sp.Room))
	}
}

// monsterTarget picks the monster a bolt flies at: the named one, else the one being fought, else the
// only one here.
func (mp *MudPlayer) monsterTarget(query string) (*BaddieNPC, string) {
	q := strings.ToUpper(strings.Join(strings.Fields(query), " "))
	var here []*BaddieNPC
	for _, b := range mp.Location.BaddieMap {
		if !b.dead {
			here = append(here, b)
		}
	}
	sort.Slice(here, func(i, j int) bool { return here[i].Name < here[j].Name })
	if len(here) == 0 {
		return nil, "There is nothing here to cast at."
	}
	if q == "" {
		if t := mp.NPCTarget; t != nil && t.Location == mp.Location && !t.dead {
			return t, ""
		}
		if len(here) == 1 {
			return here[0], ""
		}
		return nil, "Cast at what? " + monsterNames(here) + "."
	}
	var hit []*BaddieNPC
	for _, b := range here {
		if strings.ToUpper(b.Name) == q {
			return b, ""
		}
		if strings.HasPrefix(strings.ToUpper(b.Name), q) {
			hit = append(hit, b)
		}
	}
	switch len(hit) {
	case 1:
		return hit[0], ""
	case 0:
		return nil, "You do not see that here."
	}
	return nil, "Which do you mean: " + monsterNames(hit) + "?"
}

func monsterNames(list []*BaddieNPC) string {
	names := make([]string, len(list))
	for i, b := range list {
		names[i] = b.Name
	}
	return strings.Join(names, ", ")
}

func registerCast(ms *MudServer, cList map[string]*MudCommand) {
	cList["CAST"] = &MudCommand{
		description: "Cast a spell you know.  Usage: CAST <spell or slot 1-3> [target]",
		action: func(p *MudPlayer, arg string) int {
			ms.cast(p, arg, time.Now())
			return muddata.MUD_OK
		},
	}
}
