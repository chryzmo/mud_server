package mudserver

import "testing"

func TestGearSentenceSaysWhatIsWieldedWornAndCarried(t *testing.T) {
	p := &MudPlayer{Equipment: NewEquipmentSlots()}
	if got, want := gearSentence(p), "They have no gear to speak of."; got != want {
		t.Errorf("no gear = %q, want %q", got, want)
	}
	p.Equipment.Weapon = &EquipmentItem{Name: "IRON SWORD"}
	if got, want := gearSentence(p), "They wield the iron sword."; got != want {
		t.Errorf("weapon = %q, want %q", got, want)
	}
	p.Equipment.Armor = &EquipmentItem{Name: "LEATHER ARMOR"}
	p.Equipment.Boots = &EquipmentItem{Name: "SPIDER-SILK BOOTS"}
	p.Equipment.Shield = &EquipmentItem{Name: "WOODEN SHIELD"}
	want := "They wield the iron sword, wear the leather armor and the spider-silk boots, and carry the wooden shield."
	if got := gearSentence(p); got != want {
		t.Errorf("full kit = %q, want %q", got, want)
	}
	p.Equipment = &EquipmentSlots{Cosmetic: &EquipmentItem{Name: "SILVER PENDANT"}}
	if got, want := gearSentence(p), "They wear the silver pendant."; got != want {
		t.Errorf("cosmetic only = %q, want %q", got, want)
	}
}

func TestHealthSentenceDescribesHowHurtAPlayerLooks(t *testing.T) {
	p := &MudPlayer{MaxHP: 100, Equipment: NewEquipmentSlots()}
	for hp, want := range map[int]string{
		100: "They look hale and healthy.",
		90:  "They look hale and healthy.",
		89:  "They have a few scratches.",
		65:  "They have a few scratches.",
		64:  "They are wounded.",
		40:  "They are wounded.",
		39:  "They are badly hurt.",
		15:  "They are badly hurt.",
		14:  "They are barely standing.",
		1:   "They are barely standing.",
	} {
		p.HP = hp
		if got := healthSentence(p); got != want {
			t.Errorf("at %d%% health: %q, want %q", hp, got, want)
		}
	}
}

func TestJoinList(t *testing.T) {
	for _, c := range []struct {
		in     []string
		oxford bool
		want   string
	}{
		{nil, false, ""},
		{[]string{"a"}, false, "a"},
		{[]string{"a", "b"}, true, "a and b"},
		{[]string{"a", "b", "c"}, false, "a, b and c"},
		{[]string{"a", "b", "c"}, true, "a, b, and c"},
	} {
		if got := joinList(c.in, c.oxford); got != c.want {
			t.Errorf("joinList(%q, %v) = %q, want %q", c.in, c.oxford, got, c.want)
		}
	}
}
