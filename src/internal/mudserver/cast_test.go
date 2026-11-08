package mudserver

import (
	"testing"
	"time"
)

func TestMagicRegrowsOnePointEverySixSecondsFromWhenItWasSpent(t *testing.T) {
	now := time.Date(2027, 1, 8, 12, 0, 0, 0, time.UTC)
	p := &MudPlayer{MP: 10, MaxMP: 10, Equipment: NewEquipmentSlots()}
	p.regenMP(now) // full: just starts the clock
	p.spendMP(4, now)
	if p.MP != 6 {
		t.Fatalf("MP after spending = %d", p.MP)
	}
	p.regenMP(now.Add(5 * time.Second))
	if p.MP != 6 {
		t.Errorf("MP after 5s = %d, want 6 (no point yet)", p.MP)
	}
	p.regenMP(now.Add(13 * time.Second)) // two points, with a second left over
	if p.MP != 8 {
		t.Errorf("MP after 13s = %d, want 8", p.MP)
	}
	p.regenMP(now.Add(18 * time.Second)) // the left-over second counts: 18s is three points in all
	if p.MP != 9 {
		t.Errorf("MP after 18s = %d, want 9", p.MP)
	}
	p.regenMP(now.Add(10 * time.Minute))
	if p.MP != 10 {
		t.Errorf("MP never goes above the maximum, got %d", p.MP)
	}
	// Spending while full restarts the clock from that moment, not from long ago.
	later := now.Add(time.Hour)
	p.spendMP(1, later)
	p.regenMP(later.Add(5 * time.Second))
	if p.MP != 9 {
		t.Errorf("MP = %d, want 9 five seconds after spending from full", p.MP)
	}
}

func TestAWardAddsDefenceOnlyWhileItLasts(t *testing.T) {
	p := &MudPlayer{Defense: 10, Equipment: NewEquipmentSlots()}
	p.wardBonus, p.wardUntil = 3, time.Now().Add(time.Hour)
	if got := p.EffectiveDefense(); got != 13 {
		t.Errorf("warded defence = %d, want 13", got)
	}
	p.wardUntil = time.Now().Add(-time.Second)
	if got := p.EffectiveDefense(); got != 10 {
		t.Errorf("expired ward defence = %d, want 10", got)
	}
}
