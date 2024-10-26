package mudserver

import (
	"fmt"
	"mud_server/src/internal/muddata"
)

type Inventory struct {
	items map[string]*Item
	weight int
}

func CreateInventory () (*Inventory) {
	return &Inventory{
		items: make(map[string]*Item),
		weight: 0,
	}
}

func (i Inventory) GetItem (itemname string) (*Item) {
	if _, exists := i.items[itemname]; exists {
		return i.items[itemname]
	}

	return nil
}

func (i Inventory) AddItem (item *Item) {
	if _, exists := i.items[item.Name]; exists {
		i.items[item.Name].Quantity++
	} else {
		i.items[item.Name] = item
	}
}

func (i Inventory) AddItemByQuantity (item *Item, quantity int32) {
	if _, exists := i.items[item.Name]; exists {
		i.items[item.Name].Quantity += quantity
	} else {
		i.items[item.Name] = item
		i.items[item.Name].Quantity = quantity
	}
}

func (i Inventory) RemoveItem (item *Item) (int) {
	if _, exists := i.items[item.Name]; exists {
		if i.items[item.Name].Quantity > 1 {
			i.items[item.Name].Quantity--
		} else {
			delete(i.items,item.Name)
		}
	} else {
		return muddata.ERR_INV_NOITEM
	}

	return muddata.MUD_OK
}

func (i Inventory) ListAsString(withQuantity bool) (string) {
	invreturn := ""

	if len(i.items) > 0 {
		for k := range i.items {
			if withQuantity {
				invreturn += fmt.Sprint(i.items[k].Quantity, "x ", k, "\n")
			} else {
				invreturn += fmt.Sprint(k, "\n")
			}
		}
	}

	return invreturn
}

// TODO:  This may ned to be JSON
// inventory: {item_id,quantity}
// Example?  https://gist.github.com/rrafal/46e58682cd1ca70a740b433e57f070cd
func (i Inventory) IDMap() (map[string]int32) {
	// map["i"<item_id>]quantity
	idmap := make(map[string]int32)

	if len(i.items) > 0 {
		for _,v := range i.items {
			iid := fmt.Sprint("i",v.ID)
			idmap[iid] = v.Quantity
		}
	}

	return idmap
}

func (i Inventory) isEmpty() (bool) {
	return len(i.items) == 0
}