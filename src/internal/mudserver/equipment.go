package mudserver

import (
	"fmt"
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
// EquipmentSlots — the nine gear slots a player can fill.
// ---------------------------------------------------------------------------

type EquipmentSlots struct {
	Weapon *EquipmentItem
	Shield *EquipmentItem
	Helmet *EquipmentItem
	Chest  *EquipmentItem
	Legs   *EquipmentItem
	Hands  *EquipmentItem
	Ring1  *EquipmentItem
	Ring2  *EquipmentItem
	Neck   *EquipmentItem
}

func NewEquipmentSlots() *EquipmentSlots {
	return &EquipmentSlots{}
}

// GetSlotPtr returns a pointer-to-pointer for a named slot so callers can read or set it.
// Returns nil for unknown slot names.
func (es *EquipmentSlots) GetSlotPtr(slotName string) **EquipmentItem {
	switch strings.ToLower(slotName) {
	case "weapon":
		return &es.Weapon
	case "shield":
		return &es.Shield
	case "helmet":
		return &es.Helmet
	case "chest":
		return &es.Chest
	case "legs":
		return &es.Legs
	case "hands":
		return &es.Hands
	case "ring1":
		return &es.Ring1
	case "ring2":
		return &es.Ring2
	case "neck":
		return &es.Neck
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
		{"Shield", es.Shield},
		{"Helmet", es.Helmet},
		{"Chest", es.Chest},
		{"Legs", es.Legs},
		{"Hands", es.Hands},
		{"Ring1", es.Ring1},
		{"Ring2", es.Ring2},
		{"Neck", es.Neck},
	}
}

// ToIDMap serialises equipment slots to a map suitable for the JSON DB column.
func (es *EquipmentSlots) ToIDMap() map[string]int32 {
	m := make(map[string]int32)
	if es.Weapon != nil {
		m["weapon"] = es.Weapon.ID
	}
	if es.Shield != nil {
		m["shield"] = es.Shield.ID
	}
	if es.Helmet != nil {
		m["helmet"] = es.Helmet.ID
	}
	if es.Chest != nil {
		m["chest"] = es.Chest.ID
	}
	if es.Legs != nil {
		m["legs"] = es.Legs.ID
	}
	if es.Hands != nil {
		m["hands"] = es.Hands.ID
	}
	if es.Ring1 != nil {
		m["ring1"] = es.Ring1.ID
	}
	if es.Ring2 != nil {
		m["ring2"] = es.Ring2.ID
	}
	if es.Neck != nil {
		m["neck"] = es.Neck.ID
	}
	return m
}

// LoadFromIDMap populates slots by fetching each equipment entry from the DB.
func (es *EquipmentSlots) LoadFromIDMap(db *muddatabase.MudDatabase, idmap map[string]int32) {
	for slot, equipID := range idmap {
		item := FetchEquipmentByID(db, equipID)
		if item == nil {
			continue
		}
		ptr := es.GetSlotPtr(slot)
		if ptr != nil {
			*ptr = item
		}
	}
}

// ---------------------------------------------------------------------------
// Bonus aggregation helpers — called by MudPlayer.EffectiveX() methods.
// ---------------------------------------------------------------------------

func (es *EquipmentSlots) allItems() []*EquipmentItem {
	return []*EquipmentItem{
		es.Weapon, es.Shield, es.Helmet, es.Chest,
		es.Legs, es.Hands, es.Ring1, es.Ring2, es.Neck,
	}
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
