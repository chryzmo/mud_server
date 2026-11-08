package mudserver

import (
	"fmt"
	"strings"

	"mud_server/src/internal/mudutils"
)

/*
Consumables (plans/event-systems.md, P4-35). An item with `consume: {hp, mp}` is used up by USE <item>:
it restores that much health and magic, up to the player's maximum, and one of it leaves the pack. If
nothing would be restored it is kept, with a word saying so. Nothing here reads the database.
*/

// useConsumable handles USE <item> for an item in the pack that can be used up. It reports whether the
// words named such an item, so the caller can fall back to the puzzle rules. Loop only.
func (mp *MudPlayer) useConsumable(query string) bool {
	if mp.world == nil || len(mp.world.consumes) == 0 {
		return false
	}
	hit, _ := mp.thing(query, true)
	if hit == nil || hit.key == "" {
		return false
	}
	c, ok := mp.world.consumes[hit.key]
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
	maxHP, maxMP := mp.EffectiveMaxHP(), mp.EffectiveMaxMP()
	hp := min(c.HP, max(maxHP-mp.HP, 0))
	mpGain := min(c.MP, max(maxMP-mp.MP, 0))
	if hp == 0 && mpGain == 0 {
		mp.SendMessage("You do not need " + strings.ToLower(tpl.Name) + " just now.")
		return true
	}
	mp.HP += hp
	mp.MP += mpGain
	mp.Inventory.RemoveItem(it)
	var got []string
	if hp > 0 {
		got = append(got, fmt.Sprintf("+%d HP", hp))
	}
	if mpGain > 0 {
		got = append(got, fmt.Sprintf("+%d MP", mpGain))
	}
	mp.SendMessage("You use " + mudutils.ColorString(tpl.Name, mudutils.COLOR_GREEN) + ". (" + strings.Join(got, ", ") + ")")
	mp.SendStatUpdate()
	mp.dirty = true
	return true
}
