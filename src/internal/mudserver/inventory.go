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

func (i Inventory) IDSlice() ([]int32) {
	idslice := make([]int32,0)

	if len(i.items) > 0 {
		for _,v := range i.items {
			idslice = append(idslice,v.ID)
		}
	}

	return idslice
}

func (i Inventory) isEmpty() (bool) {
	return len(i.items) == 0
}