package mudserver

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"mud_server/src/internal/mudutils"
)

/*
Spells, part one: knowing and readying them (plans/event-systems.md, section 12). A character learns any
number of spells and readies three. Learning comes from a scroll (READ), the `teach` rule effect, or a
monster's drop; a scroll is a bound item, so it cannot be passed around. What a player knows and has
readied is player state (kinds spell and slot), written at once like the rest. Casting is the next step.
*/

const spellSlots = 3

// knowsSpell is whether the player has learned the spell (a content key).
func (mp *MudPlayer) knowsSpell(key string) bool { return mp.st().spells[key] }

// spellByName finds a spell the player knows from a key, a name, or a unique prefix of either.
func (mp *MudPlayer) spellByName(query string) (key string, several []string) {
	q := strings.ToUpper(strings.Join(strings.Fields(query), " "))
	if q == "" || mp.world == nil {
		return "", nil
	}
	var prefix []string
	for k := range mp.st().spells {
		sp, ok := mp.world.spells[k]
		if !ok {
			continue
		}
		name := strings.ToUpper(sp.Name)
		if name == q || strings.ToUpper(k) == q {
			return k, nil
		}
		if strings.HasPrefix(name, q) || strings.HasPrefix(strings.ToUpper(k), q) {
			prefix = append(prefix, k)
		}
	}
	sort.Strings(prefix)
	switch len(prefix) {
	case 0:
		return "", nil
	case 1:
		return prefix[0], nil
	}
	for _, k := range prefix {
		several = append(several, mp.world.spells[k].Name)
	}
	return "", several
}

// learnSpell teaches a spell for good, readies it if a slot is free, and says so. A spell already known
// changes nothing and says nothing, so a quest that runs again does not repeat the news. Loop only.
func (mp *MudPlayer) learnSpell(key string) bool {
	sp, ok := mp.world.spells[key]
	if !ok || mp.knowsSpell(key) {
		return false
	}
	mp.st().spells[key] = true
	defer mp.SendSpells()
	mp.persistState("spell", key, "1", false)
	msg := "You learn " + mudutils.ColorString(sp.Name, mudutils.COLOR_GREEN) + "."
	for i := 0; i < spellSlots; i++ {
		if mp.st().slots[i] == "" {
			mp.setSlot(i, key)
			msg += fmt.Sprintf(" It is ready in slot %d.", i+1)
			break
		}
	}
	if !strings.Contains(msg, "ready in slot") {
		msg += " READY it to put it on your spell bar."
	}
	mp.SendMessage(msg)
	return true
}

// setSlot readies a spell in a slot (0 to 2); an empty key clears it. A spell sits in one slot only.
func (mp *MudPlayer) setSlot(i int, key string) {
	if key != "" {
		for j := 0; j < spellSlots; j++ {
			if mp.st().slots[j] == key && j != i {
				mp.st().slots[j] = ""
				mp.persistState("slot", strconv.Itoa(j+1), "", true)
			}
		}
	}
	mp.st().slots[i] = key
	mp.persistState("slot", strconv.Itoa(i+1), key, key == "")
}

// useScroll handles READ <item> for a carried scroll: it teaches its spell and is used up. It reports
// whether the words named a scroll, so the caller can fall back to the puzzle rules. Loop only.
func (mp *MudPlayer) useScroll(query string) bool {
	if mp.world == nil || len(mp.world.scrolls) == 0 {
		return false
	}
	hit, _ := mp.thing(query, true)
	if hit == nil || hit.key == "" {
		return false
	}
	spell, ok := mp.world.scrolls[hit.key]
	if !ok {
		return false
	}
	tpl := mp.world.itemsByKey[hit.key]
	if tpl == nil {
		return false
	}
	it := mp.Inventory.GetItem(tpl.Name)
	if it == nil {
		return false
	}
	if mp.knowsSpell(spell) {
		mp.SendMessage("You already know the spell on " + strings.ToLower(tpl.Name) + ".")
		return true
	}
	mp.Inventory.RemoveItem(it)
	mp.dirty = true
	mp.learnSpell(spell)
	return true
}

// spellsList answers SPELLS: what the player knows and what is readied. Loop only.
func (mp *MudPlayer) spellsList() {
	if len(mp.st().spells) == 0 {
		mp.SendMessage("You have not learned any spells yet. Scrolls, quests and some monsters teach them.")
		return
	}
	keys := make([]string, 0, len(mp.st().spells))
	for k := range mp.st().spells {
		if _, ok := mp.world.spells[k]; ok {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return mp.world.spells[keys[i]].Name < mp.world.spells[keys[j]].Name })
	var b strings.Builder
	b.WriteString("Your spells:\n")
	for _, k := range keys {
		sp := mp.world.spells[k]
		slot := "  "
		for i := 0; i < spellSlots; i++ {
			if mp.st().slots[i] == k {
				slot = "[" + strconv.Itoa(i+1) + "]"
			}
		}
		fmt.Fprintf(&b, "  %s %-16s %2d MP  %s\n", slot, sp.Name, sp.Cost, sp.Description)
	}
	b.WriteString("Ready three with READY <spell> [slot 1-3]. UNREADY <slot or spell> frees one.")
	mp.SendMessage(b.String())
}

// ready answers READY <spell> [slot]. Loop only.
func (mp *MudPlayer) ready(arguments string) {
	fields := strings.Fields(arguments)
	if len(fields) == 0 {
		mp.spellsList()
		return
	}
	slot := -1
	if n, err := strconv.Atoi(fields[len(fields)-1]); err == nil && len(fields) > 1 {
		if n < 1 || n > spellSlots {
			mp.SendMessage(fmt.Sprintf("There are %d slots: 1 to %d.", spellSlots, spellSlots))
			return
		}
		slot, fields = n-1, fields[:len(fields)-1]
	}
	key, several := mp.spellByName(strings.Join(fields, " "))
	switch {
	case key == "" && len(several) > 0:
		mp.SendMessage("Which do you mean: " + strings.Join(several, ", ") + "?")
		return
	case key == "":
		mp.SendMessage("You do not know a spell called that. SPELLS lists yours.")
		return
	}
	if slot < 0 {
		for i := 0; i < spellSlots; i++ {
			if mp.st().slots[i] == key {
				slot = i
				break
			}
		}
	}
	if slot < 0 {
		for i := 0; i < spellSlots; i++ {
			if mp.st().slots[i] == "" {
				slot = i
				break
			}
		}
	}
	if slot < 0 {
		mp.SendMessage("All three slots are full. Pick one: READY " + mp.world.spells[key].Name + " 1, 2 or 3.")
		return
	}
	mp.setSlot(slot, key)
	mp.SendMessage(fmt.Sprintf("%s is ready in slot %d.", mp.world.spells[key].Name, slot+1))
	mp.SendSpells()
}

// unready answers UNREADY <slot or spell>. Loop only.
func (mp *MudPlayer) unready(arguments string) {
	q := strings.TrimSpace(arguments)
	if q == "" {
		mp.SendMessage("Usage: UNREADY <slot 1-3 or spell>")
		return
	}
	slot := -1
	if n, err := strconv.Atoi(q); err == nil && n >= 1 && n <= spellSlots {
		slot = n - 1
	} else if key, _ := mp.spellByName(q); key != "" {
		for i := 0; i < spellSlots; i++ {
			if mp.st().slots[i] == key {
				slot = i
			}
		}
	}
	if slot < 0 || mp.st().slots[slot] == "" {
		mp.SendMessage("Nothing is readied there.")
		return
	}
	name := mp.world.spells[mp.st().slots[slot]].Name
	mp.setSlot(slot, "")
	mp.SendMessage(fmt.Sprintf("%s is no longer ready.", name))
	mp.SendSpells()
}

// spellInfo is one known spell in the spells frame.
type spellInfo struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Effect   string `json:"effect"`
	Cost     int    `json:"cost"`
	Cooldown int    `json:"cooldown"`
	Desc     string `json:"desc"`
}

// spellsMsg tells the client what the player knows and has readied (slot keys, empty when free).
type spellsMsg struct {
	T     string      `json:"t"`
	Known []spellInfo `json:"known"`
	Ready []string    `json:"ready"`
}

// SendSpells sends the spells frame. Loop only.
func (mp *MudPlayer) SendSpells() {
	if mp.world == nil {
		return
	}
	msg := spellsMsg{T: msgSpells, Known: []spellInfo{}, Ready: append([]string{}, mp.st().slots[:]...)}
	for k := range mp.st().spells {
		if sp, ok := mp.world.spells[k]; ok {
			msg.Known = append(msg.Known, spellInfo{Key: k, Name: sp.Name, Effect: sp.Effect, Cost: sp.Cost, Cooldown: sp.Cooldown, Desc: sp.Description})
		}
	}
	sort.Slice(msg.Known, func(i, j int) bool { return msg.Known[i].Name < msg.Known[j].Name })
	mp.sendFrame(encode(msg))
}

// sendSpellsIfAny is for entering the world: a player who knows no spell is sent nothing, which the
// client reads as an empty set, so most players see no extra frame.
func (mp *MudPlayer) sendSpellsIfAny() {
	if mp.state != nil && len(mp.state.spells) > 0 {
		mp.SendSpells()
	}
}
