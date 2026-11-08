package mudserver

import (
	"fmt"
	"strconv"
	"strings"

	"mud_server/src/internal/mudutils"
)

/*
Shops (plans/event-systems.md, P4-34). A person in content may have a `shop` list of items for sale at a
price in gold. SHOP lists what they sell, BUY <item> pays for one and puts it in the pack. A shop sells
stat gear and consumables; the most powerful items are monster and boss drops and are never for sale.
Gold is the GOLD item in the pack. Nothing here touches the database on the loop: the note beside each
ware (its bonuses) is worked out when the world is built.
*/

// ShopItem is one ware: the item's content key, its price in gold, and a short note on what it does.
type ShopItem struct {
	Item  string `json:"item"`
	Price int32  `json:"price"`
	Note  string `json:"note,omitempty"`
}

// goldHeld is how much gold the player carries.
func (mp *MudPlayer) goldHeld() int32 {
	if it := mp.Inventory.GetItem("GOLD"); it != nil {
		return it.Quantity
	}
	return 0
}

// spendGold takes n gold from the pack; false if there is not enough.
func (mp *MudPlayer) spendGold(n int32) bool {
	it := mp.Inventory.GetItem("GOLD")
	if it == nil || it.Quantity < n {
		return false
	}
	it.Quantity -= n
	if it.Quantity == 0 {
		mp.Inventory.RemoveItem(it)
	}
	return true
}

// shopkeeper finds the person to trade with: the named one, or the only shopkeeper in the room.
func (mp *MudPlayer) shopkeeper(query string) (*Person, string) {
	if mp.Location == nil {
		return nil, "There is no one to trade with here."
	}
	if query != "" {
		p, several := mp.findPerson(query)
		switch {
		case p == nil && len(several) > 0:
			return nil, "Which do you mean: " + strings.Join(several, ", ") + "?"
		case p == nil:
			return nil, "There is no one called that here."
		case len(p.Shop) == 0:
			return nil, p.Names[0] + " has nothing for sale."
		}
		return p, ""
	}
	var keepers []*Person
	for i := range mp.Location.People {
		if len(mp.Location.People[i].Shop) > 0 {
			keepers = append(keepers, &mp.Location.People[i])
		}
	}
	switch len(keepers) {
	case 0:
		return nil, "No one here is selling anything."
	case 1:
		return keepers[0], ""
	}
	names := make([]string, len(keepers))
	for i, k := range keepers {
		names[i] = k.Names[0]
	}
	return nil, "Who do you want to trade with? " + strings.Join(names, ", ") + "."
}

// shop answers SHOP [person]: what they sell and what each costs. Loop only.
func (mp *MudPlayer) shop(arguments string) {
	keeper, why := mp.shopkeeper(strings.Join(strings.Fields(strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(arguments)), "AT ")), " "))
	if keeper == nil {
		mp.SendMessage(why)
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s sells (you have %d gold):\n", keeper.Names[0], mp.goldHeld())
	for _, w := range keeper.Shop {
		tpl := mp.world.itemsByKey[w.Item]
		if tpl == nil {
			continue
		}
		line := fmt.Sprintf("  %-22s %4d gold", tpl.Name, w.Price)
		if w.Note != "" {
			line += "  " + w.Note
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("BUY <item> to buy one.")
	mp.SendMessage(b.String())
}

// buy answers BUY <item> [FROM <person>]: pay and take one. Loop only.
func (mp *MudPlayer) buy(arguments string) {
	args := strings.Join(strings.Fields(arguments), " ")
	from := ""
	if i := strings.Index(strings.ToUpper(args), " FROM "); i >= 0 {
		args, from = args[:i], args[i+len(" FROM "):]
	}
	if args == "" {
		mp.SendMessage("Buy what? Usage: BUY <item>  (SHOP lists what is for sale)")
		return
	}
	keeper, why := mp.shopkeeper(strings.ToUpper(from))
	if keeper == nil {
		mp.SendMessage(why)
		return
	}
	want := strings.ToUpper(args)
	var hit *ShopItem
	var matches []string
	for i := range keeper.Shop {
		tpl := mp.world.itemsByKey[keeper.Shop[i].Item]
		if tpl == nil {
			continue
		}
		name := strings.ToUpper(tpl.Name)
		if name == want {
			hit, matches = &keeper.Shop[i], nil
			break
		}
		if strings.Contains(name, want) {
			hit = &keeper.Shop[i]
			matches = append(matches, tpl.Name)
		}
	}
	switch {
	case hit == nil:
		mp.SendMessage(keeper.Names[0] + " does not sell that. SHOP lists what is for sale.")
		return
	case len(matches) > 1:
		mp.SendMessage("Which do you mean: " + strings.Join(matches, ", ") + "?")
		return
	}
	tpl := mp.world.itemsByKey[hit.Item]
	if !mp.spendGold(hit.Price) {
		mp.SendMessage(fmt.Sprintf("%s costs %d gold and you have %d.", tpl.Name, hit.Price, mp.goldHeld()))
		return
	}
	c := tpl.createCopy()
	c.Quantity = 1
	mp.Inventory.AddItem(c)
	mp.dirty = true
	mp.SendMessage("You buy " + mudutils.ColorString(tpl.Name, mudutils.COLOR_GREEN) + " for " + strconv.Itoa(int(hit.Price)) + " gold.")
	mp.SendStatUpdate()
}
