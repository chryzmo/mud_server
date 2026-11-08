package mudserver

import (
	"fmt"
	"sort"
	"strings"

	muddatabase "mud_server/src/internal/database"

	"github.com/jackc/pgx/v5"
)

// EquipmentItem is one entry from the equipment catalog joined with the item table.
type EquipmentItem struct {
	ID               int32
	ItemID           int32
	Name             string
	Slot             string
	BonusHP          int32
	BonusMaxHP       int32
	BonusMP          int32
	BonusMaxMP       int32
	BonusAttackPower int32
	BonusDefense     int32
}

// equipmentItemDB is the pgx scan target; field names lowercase to column names.
type equipmentItemDB struct {
	ID               int32
	ItemID           int32
	Name             string
	Slot             string
	BonusHP          int32
	BonusMaxHP       int32
	BonusMP          int32
	BonusMaxMP       int32
	BonusAttackPower int32
	BonusDefense     int32
}

func dbRowToEquipmentItem(r equipmentItemDB) *EquipmentItem {
	return &EquipmentItem{
		ID:               r.ID,
		ItemID:           r.ItemID,
		Name:             r.Name,
		Slot:             r.Slot,
		BonusHP:          r.BonusHP,
		BonusMaxHP:       r.BonusMaxHP,
		BonusMP:          r.BonusMP,
		BonusMaxMP:       r.BonusMaxMP,
		BonusAttackPower: r.BonusAttackPower,
		BonusDefense:     r.BonusDefense,
	}
}

const equipSelectCols = `e.id, e.itemid, i.name, e.slot,
	e.bonushp, e.bonusmaxhp, e.bonusmp, e.bonusmaxmp,
	e.bonusattackpower, e.bonusdefense`

// FetchEquipmentByItemName looks up the equipment catalog entry for an item by name.
// Returns nil if the item exists but is not equippable.
func FetchEquipmentByItemName(db *muddatabase.MudDatabase, itemName string) *EquipmentItem {
	query := `SELECT ` + equipSelectCols + `
		FROM equipment e
		JOIN item i ON i.id = e.itemid
		WHERE UPPER(i.name) = $1`

	rows, err := db.Connection.Query(db.Context, query, strings.ToUpper(itemName))
	if err != nil {
		fmt.Println("FetchEquipmentByItemName query error: " + err.Error())
		return nil
	}
	ret, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[equipmentItemDB])
	if err != nil {
		return nil // not equippable — not an error
	}
	return dbRowToEquipmentItem(ret)
}

// FetchEquipmentByID loads a catalog entry by its equipment table ID.
func FetchEquipmentByID(db *muddatabase.MudDatabase, equipID int32) *EquipmentItem {
	query := `SELECT ` + equipSelectCols + `
		FROM equipment e
		JOIN item i ON i.id = e.itemid
		WHERE e.id = $1`

	rows, err := db.Connection.Query(db.Context, query, equipID)
	if err != nil {
		fmt.Println("FetchEquipmentByID query error: " + err.Error())
		return nil
	}
	ret, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[equipmentItemDB])
	if err != nil {
		fmt.Println("FetchEquipmentByID row error: " + err.Error())
		return nil
	}
	return dbRowToEquipmentItem(ret)
}

// BonusSummary returns a human-readable string of non-zero bonuses, e.g. "(+5 Attack, +2 Defense)".
func (e *EquipmentItem) BonusSummary() string {
	parts := []string{}
	if e.BonusAttackPower != 0 {
		parts = append(parts, fmt.Sprintf("%+d Attack", e.BonusAttackPower))
	}
	if e.BonusDefense != 0 {
		parts = append(parts, fmt.Sprintf("%+d Defense", e.BonusDefense))
	}
	if e.BonusHP != 0 {
		parts = append(parts, fmt.Sprintf("%+d HP", e.BonusHP))
	}
	if e.BonusMaxHP != 0 {
		parts = append(parts, fmt.Sprintf("%+d MaxHP", e.BonusMaxHP))
	}
	if e.BonusMP != 0 {
		parts = append(parts, fmt.Sprintf("%+d MP", e.BonusMP))
	}
	if e.BonusMaxMP != 0 {
		parts = append(parts, fmt.Sprintf("%+d MaxMP", e.BonusMaxMP))
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// ---------------------------------------------------------------------------
// EquipmentSlots — the five gear slots a player can fill.
// ---------------------------------------------------------------------------

// Slot names. The cosmetic slot is for looks only and carries no stats.
const (
	SlotWeapon   = "weapon"
	SlotArmor    = "armor"
	SlotShield   = "shield"
	SlotBoots    = "boots"
	SlotCosmetic = "cosmetic"
)

type EquipmentSlots struct {
	Weapon   *EquipmentItem
	Armor    *EquipmentItem
	Shield   *EquipmentItem
	Boots    *EquipmentItem
	Cosmetic *EquipmentItem
}

func NewEquipmentSlots() *EquipmentSlots {
	return &EquipmentSlots{}
}

// NormalizeSlot returns the slot name in lower case, or "" if it is not one of the five.
func NormalizeSlot(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case SlotWeapon, SlotArmor, SlotShield, SlotBoots, SlotCosmetic:
		return name
	}
	return ""
}

// GetSlotPtr returns a pointer-to-pointer for a named slot so callers can read or set it.
// Returns nil for unknown slot names.
func (es *EquipmentSlots) GetSlotPtr(slotName string) **EquipmentItem {
	switch NormalizeSlot(slotName) {
	case SlotWeapon:
		return &es.Weapon
	case SlotArmor:
		return &es.Armor
	case SlotShield:
		return &es.Shield
	case SlotBoots:
		return &es.Boots
	case SlotCosmetic:
		return &es.Cosmetic
	}
	return nil
}

// AllSlots returns every slot name paired with its current item (or nil).
func (es *EquipmentSlots) AllSlots() []struct {
	Name string
	Item *EquipmentItem
} {
	return []struct {
		Name string
		Item *EquipmentItem
	}{
		{"Weapon", es.Weapon},
		{"Armor", es.Armor},
		{"Shield", es.Shield},
		{"Boots", es.Boots},
		{"Cosmetic", es.Cosmetic},
	}
}

// ToIDMap serialises equipment slots to a map suitable for the JSON DB column.
func (es *EquipmentSlots) ToIDMap() map[string]int32 {
	m := make(map[string]int32)
	for name, item := range map[string]*EquipmentItem{
		SlotWeapon: es.Weapon, SlotArmor: es.Armor, SlotShield: es.Shield,
		SlotBoots: es.Boots, SlotCosmetic: es.Cosmetic,
	} {
		if item != nil {
			m[name] = item.ID
		}
	}
	return m
}

// LoadFromIDMap populates slots by fetching each equipment entry from the DB. It reads
// saved gear from the retired nine-slot saves too: chest and legs move to armor and boots, and a piece
// from a retired slot (helmet, hands, ring1, ring2, neck) or one that would land on a
// slot already filled is returned in `leftover` so the caller can put it in the pack.
// Slots are visited in name order so the result does not depend on map order.
func (es *EquipmentSlots) LoadFromIDMap(db *muddatabase.MudDatabase, idmap map[string]int32) (leftover []*EquipmentItem) {
	names := make([]string, 0, len(idmap))
	for slot := range idmap {
		names = append(names, slot)
	}
	sort.Strings(names)
	for _, slot := range names {
		item := FetchEquipmentByID(db, idmap[slot])
		if item == nil {
			continue
		}
		es.place(slot, item, &leftover)
	}
	return leftover
}

// place puts a saved piece in its slot, or into leftover when the slot is retired or taken.
func (es *EquipmentSlots) place(savedSlot string, item *EquipmentItem, leftover *[]*EquipmentItem) {
	slot := strings.ToLower(savedSlot)
	switch slot {
	case "chest":
		slot = SlotArmor
	case "legs":
		slot = SlotBoots
	case "helmet", "hands", "ring1", "ring2", "neck":
		*leftover = append(*leftover, item)
		return
	}
	ptr := es.GetSlotPtr(slot)
	if ptr == nil || *ptr != nil {
		*leftover = append(*leftover, item)
		return
	}
	*ptr = item
}

// ---------------------------------------------------------------------------
// Bonus aggregation helpers — called by MudPlayer.EffectiveX() methods.
// The cosmetic slot is for looks and never adds a stat.
// ---------------------------------------------------------------------------

func (es *EquipmentSlots) allItems() []*EquipmentItem {
	return []*EquipmentItem{es.Weapon, es.Armor, es.Shield, es.Boots}
}

func (es *EquipmentSlots) TotalBonusAttackPower() int {
	total := 0
	for _, e := range es.allItems() {
		if e != nil {
			total += int(e.BonusAttackPower)
		}
	}
	return total
}

func (es *EquipmentSlots) TotalBonusDefense() int {
	total := 0
	for _, e := range es.allItems() {
		if e != nil {
			total += int(e.BonusDefense)
		}
	}
	return total
}

func (es *EquipmentSlots) TotalBonusMaxHP() int {
	total := 0
	for _, e := range es.allItems() {
		if e != nil {
			total += int(e.BonusMaxHP)
		}
	}
	return total
}

func (es *EquipmentSlots) TotalBonusMaxMP() int {
	total := 0
	for _, e := range es.allItems() {
		if e != nil {
			total += int(e.BonusMaxMP)
		}
	}
	return total
}

// Worn lists what is worn, for EXAMINE <player>: item names in slot order.
func (es *EquipmentSlots) Worn() []string {
	var out []string
	for _, s := range es.AllSlots() {
		if s.Item != nil {
			out = append(out, s.Item.Name)
		}
	}
	return out
}
