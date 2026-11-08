package mudserver

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
)

/*
Small mobs (plans/event-systems.md, P4-7): monsters a guest can fight at any hour without a GM event. What
content says about a monster decides how it behaves:

  - It attacks only when attacked (aggressive: false, the default) and then fights back at whoever has hit
    it, latest first. An aggressive monster attacks whoever is in the room, as the seeded orc always did.
  - A kill pays each fighter who hit it and is still in the room the monster's xp (or the progression's
    kill XP) in full, so joining a fight never costs you the reward. Gold loot goes to the killer.
  - Dying costs nothing: the player wakes at their spawn point with full health (Respawn).
  - A spawner only works while someone is in its room, and the world holds at most mobCap monsters, so a
    crowd cannot grow the monster list without bound.
*/

// defaultMobCap is the most live monsters in the world.
const defaultMobCap = 300

// mobTraits is what content says about one monster race beyond its numbers.
type mobTraits struct {
	aggressive bool
	xp         int
}

// applyMobTraits gives each spawner's template monster its traits from content. A monster that has no
// traits (the seeded orc of a world without content) stays aggressive, as it always was. Before copies of
// rooms are made, so copies inherit it.
func (w *World) applyMobTraits(traits map[string]mobTraits) {
	for _, s := range w.Spawners {
		if t, ok := traits[strings.ToUpper(s.baddie.Race)]; ok {
			s.baddie.passive = !t.aggressive
			s.baddie.xp = t.xp
		}
	}
}

// provoke notes that the player attacked this monster: it will fight back at them, and a kill pays them.
// The latest attacker is last. Loop only.
func (npc *BaddieNPC) provoke(p *MudPlayer) {
	for i, h := range npc.hitters {
		if h == p {
			npc.hitters = append(npc.hitters[:i], npc.hitters[i+1:]...)
			break
		}
	}
	npc.hitters = append(npc.hitters, p)
}

// stillFighting drops hitters who have left the room or are down. Loop only.
func (npc *BaddieNPC) stillFighting() {
	keep := npc.hitters[:0]
	for _, h := range npc.hitters {
		if npc.Location != nil && npc.Location.GetPlayer(h.Username) == h && h.HP > 0 {
			keep = append(keep, h)
		}
	}
	npc.hitters = keep
}

// pickTarget chooses who the monster fights this turn, or nil. A passive monster fights only those who have
// hit it, the latest first; an aggressive one takes someone in the room. Loop only.
func (npc *BaddieNPC) pickTarget() *MudPlayer {
	if npc.passive {
		npc.stillFighting()
		if n := len(npc.hitters); n > 0 {
			return npc.hitters[n-1]
		}
		return nil
	}
	for _, p := range npc.Location.Players() {
		return p
	}
	return nil
}

// killXP is what each fighter is paid for this monster.
func (npc *BaddieNPC) killXP(w *World) int {
	if npc.xp > 0 {
		return npc.xp
	}
	if w != nil {
		return w.prog.XP.Kill
	}
	return 0
}

// defeated is the end of a fight: every fighter who is still here is paid the XP, the killer gets the
// loot, and the monster is gone. A second blow at a monster already dead does nothing. Loop only.
func (r *Room) defeated(npc *BaddieNPC, killer *MudPlayer) {
	if npc.dead {
		return
	}
	npc.dead = true
	npc.stillFighting()
	paid := map[*MudPlayer]bool{killer: true}
	fighters := []*MudPlayer{killer}
	for _, h := range npc.hitters {
		if !paid[h] {
			paid[h] = true
			fighters = append(fighters, h)
		}
	}
	for _, p := range fighters {
		if p.world == nil {
			continue
		}
		if xp := npc.killXP(p.world); xp > 0 {
			p.addXP(xpCombat, xp)
			if p == killer {
				p.SendMessage(fmt.Sprintf("(+%d combat XP)", xp))
			} else {
				p.SendMessage(fmt.Sprintf("You helped defeat %s. (+%d combat XP)", npc.Name, xp))
			}
			p.dirty = true
		}
		if p.NPCTarget == npc {
			p.NPCTarget = nil
		}
	}
	r.KillNPC(npc.Name, killer.Username)
	r.roomChanged() // the monster is no longer tappable
}

// uniqueMobName is the monster's name, made different from every monster already in the room: two with
// the same name would overwrite each other in the room's table and one would vanish.
func (r *Room) uniqueMobName(name string) string {
	if _, taken := r.BaddieMap[strings.ToUpper(name)]; !taken {
		return name
	}
	for i := 2; ; i++ {
		candidate := name + " " + strconv.Itoa(i)
		if _, taken := r.BaddieMap[strings.ToUpper(candidate)]; !taken {
			return candidate
		}
	}
}

// lootGold is a random amount from min to max, inclusive. (rand.Int31n panics when max equals min.)
func lootGold(min, max int32) int32 {
	if max <= min {
		return min
	}
	return min + rand.Int32N(max-min+1)
}
