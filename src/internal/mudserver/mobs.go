package mudserver

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	muddatabase "mud_server/src/internal/database"

	"mud_server/src/internal/worldfile"
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
	drops      []worldfile.Drop
	boss       bool
	name       string
}

// applyMobTraits gives each spawner's template monster its traits from content. A monster that has no
// traits (the seeded orc of a world without content) stays aggressive, as it always was. Before copies of
// rooms are made, so copies inherit it.
func (w *World) applyMobTraits(traits map[string]mobTraits) {
	for _, s := range w.Spawners {
		if t, ok := traits[strings.ToUpper(s.baddie.Race)]; ok {
			s.baddie.passive = !t.aggressive
			s.baddie.xp = t.xp
			s.baddie.drops = t.drops
			s.baddie.boss = t.boss
			s.baddie.Name = t.name
		}
	}
}

// copyOf is a fresh monster from a template: its numbers and traits, no name, place or fight yet.
func (t *BaddieNPC) copyOf(now time.Time) *BaddieNPC {
	return &BaddieNPC{
		Race: t.Race, Name: t.Name,
		AttackAccuracy: t.AttackAccuracy, AttackPower: t.AttackPower, Defense: t.Defense,
		HP: t.HP, MP: t.MP, TurnAction: t.TurnAction,
		combatTickerDurationInMS: t.combatTickerDurationInMS,
		// As with the old per-NPC ticker, the first swing comes one interval after spawning.
		nextAttackAt: now.Add(time.Millisecond * time.Duration(t.combatTickerDurationInMS)),
		LootGoldMin:  t.LootGoldMin, LootGoldMax: t.LootGoldMax,
		passive: t.passive, xp: t.xp, drops: t.drops, boss: t.boss,
	}
}

// placeMob puts a new monster in its Location, named apart from the others there, and tells the room.
// w may be nil. Loop only.
func (w *World) placeMob(m *BaddieNPC) {
	if m.Name == "" {
		m.Name = m.GenerateName()
	}
	m.Name = m.Location.uniqueMobName(m.Name)
	m.Location.BaddieMap[strings.ToUpper(m.Name)] = m
	if w != nil {
		w.liveMobs++
	}
	m.Location.Broadcast(m.Name+" - "+m.Race+" - appears in the room!", "")
	m.Location.roomChanged()
}

// loadMobTemplates reads each content monster's numbers once, at world build, so a spawn by key never
// touches the database on the loop.
func (w *World) loadMobTemplates(db *muddatabase.MudDatabase, content *contentResult) {
	w.mobTemplates = map[string]*BaddieNPC{}
	for key, race := range content.mobRaces {
		t := BaddieNPC{}.FetchGenericFromDB(db, race)
		if t.Race == "" {
			continue
		}
		traits := content.mobs[strings.ToUpper(race)]
		t.passive, t.xp, t.drops, t.boss, t.Name = !traits.aggressive, traits.xp, traits.drops, traits.boss, traits.name
		w.mobTemplates[key] = &t
	}
}

// spawnMob puts one monster (by npc key) in a room. It returns the monster, or the reason it could not
// be placed. A boss is one at a time: a second is refused while one is alive. Loop only.
func (w *World) spawnMob(npcKey string, room *Room, now time.Time) (*BaddieNPC, string) {
	t := w.mobTemplates[npcKey]
	if t == nil {
		return nil, "there is no monster called " + npcKey
	}
	if room == nil {
		return nil, "there is no such room"
	}
	if w.mobCap > 0 && w.liveMobs >= w.mobCap {
		return nil, "the world holds as many monsters as it can"
	}
	if t.boss {
		for _, r := range w.RoomMap {
			for _, b := range r.BaddieMap {
				if b.boss && b.Race == t.Race && !b.dead {
					return nil, b.Name + " is already about"
				}
			}
		}
	}
	m := t.copyOf(now)
	m.Location = room
	w.placeMob(m)
	if m.boss && w.announceAll != nil {
		w.announceAll(m.Name + " has appeared in " + room.Name + "!")
	}
	return m, ""
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
				p.SendMessage(xpLine(fmt.Sprintf("(+%d combat XP)", xp)))
			} else {
				p.SendMessage(xpLine(fmt.Sprintf("You helped defeat %s. (+%d combat XP)", npc.Name, xp)))
			}
			p.dirty = true
		}
		npc.rollDrops(p)
		if p.NPCTarget == npc {
			p.NPCTarget = nil
		}
	}
	if npc.boss && killer.world != nil && killer.world.announceAll != nil {
		names := make([]string, 0, len(fighters))
		for _, p := range fighters {
			names = append(names, p.Username)
		}
		killer.world.announceAll(npc.Name + " has fallen to " + strings.Join(names, ", ") + "!")
	}
	r.KillNPC(npc.Name, killer.Username)
	r.roomChanged() // the monster is no longer tappable
}

// dropHits is one percent roll: true with the given chance (100 always hits).
func dropHits(chance int) bool { return chance >= 100 || rand.IntN(100) < chance }

// rollDrops gives one fighter whatever their own roll wins from the monster's drop table: each fighter
// rolls for themself, so joining a fight never costs you loot. A colour or badge the player already
// owns changes nothing. Loop only.
func (npc *BaddieNPC) rollDrops(p *MudPlayer) {
	for _, d := range npc.drops {
		if !dropHits(d.Chance) {
			continue
		}
		switch {
		case d.Item != "":
			p.giveItemKey(d.Item)
		case d.Color != "":
			p.grantCosmetic("color", d.Color)
		case d.Badge != "":
			p.grantCosmetic("badge", d.Badge)
		}
	}
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

// gmSpawn is GM SPAWN <npc> [room key]: put a monster in your room, or in the room with that key.
func (ms *MudServer) gmSpawn(gm *MudPlayer, args []string) string {
	if len(args) < 1 || len(args) > 2 {
		gm.SendMessage("Usage: GM SPAWN <npc> [room]")
		return "usage"
	}
	room := gm.Location
	if len(args) == 2 {
		room = ms.World.roomsByKey[strings.ToLower(args[1])]
	}
	m, why := ms.World.spawnMob(strings.ToLower(args[0]), room, time.Now())
	if why != "" {
		gm.SendMessage("Not spawned: " + why + ".")
		return "spawn refused"
	}
	gm.SendMessage(m.Name + " is in " + room.Name + ".")
	return "spawn " + strings.ToLower(args[0])
}

// announceAll tells every player a line, in the same frame as a GM announcement. Loop only.
func (ms *MudServer) announceAll(text string) {
	frame := encode(announceMsg{T: msgAnnounce, Text: text, From: "event"})
	for _, p := range ms.players {
		p.sendFrame(frame)
	}
}
