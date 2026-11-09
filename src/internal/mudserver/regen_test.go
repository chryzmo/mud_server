package mudserver

import (
	"testing"
	"time"
)

func hurt(hp, maxHP int) (*MudPlayer, *World) {
	room, w := mobRoom()
	p := mobPlayer(room, w, "ann")
	p.MaxHP, p.HP = maxHP, hp
	return p, w
}

func TestHealthRegrowsOutOfACombatAndStopsAtFull(t *testing.T) {
	p, _ := hurt(14, 20)
	now := time.Now()
	p.regenHP(now) // first sight: the clock starts
	if p.HP != 14 {
		t.Fatalf("health changed at once: %d", p.HP)
	}
	p.regenHP(now.Add(hpRegenEvery - time.Millisecond))
	if p.HP != 14 {
		t.Fatalf("health grew early: %d", p.HP)
	}
	p.regenHP(now.Add(hpRegenEvery))
	if p.HP != 16 { // a tenth of 20, and at least two
		t.Fatalf("after one period health = %d, want 16", p.HP)
	}
	for i := 2; i <= 6; i++ {
		p.regenHP(now.Add(time.Duration(i) * hpRegenEvery))
	}
	if p.HP != 20 {
		t.Fatalf("health = %d, want the maximum 20 and no more", p.HP)
	}
	if !p.hpNextAt.IsZero() {
		t.Error("the regrowth clock keeps running at full health")
	}
}

func TestRegrowthScalesWithMaximumHealth(t *testing.T) {
	p, _ := hurt(10, 100)
	now := time.Now()
	p.regenHP(now)
	p.regenHP(now.Add(hpRegenEvery))
	if p.HP != 20 { // ten percent of 100
		t.Errorf("health = %d, want 20", p.HP)
	}
}

func TestFightingPausesRegrowthUntilItQuietsDown(t *testing.T) {
	p, _ := hurt(10, 20)
	now := time.Now()
	p.markCombat(now)
	for _, d := range []time.Duration{time.Second, 3 * time.Second, hpCombatGrace - time.Millisecond} {
		p.regenHP(now.Add(d))
		if p.HP != 10 {
			t.Fatalf("health grew %v into a fight: %d", d, p.HP)
		}
	}
	p.regenHP(now.Add(hpCombatGrace)) // quiet at last: the clock starts
	p.regenHP(now.Add(hpCombatGrace + hpRegenEvery))
	if p.HP != 12 {
		t.Errorf("health = %d, want 12 one period after the fight ended", p.HP)
	}
	p.markCombat(now.Add(hpCombatGrace + hpRegenEvery + time.Second)) // another blow: paused again
	p.regenHP(now.Add(hpCombatGrace + 2*hpRegenEvery + time.Second))
	if p.HP != 12 {
		t.Errorf("health grew during a second fight: %d", p.HP)
	}
}

func TestAFallenPlayerDoesNotRegrow(t *testing.T) {
	p, _ := hurt(0, 20)
	now := time.Now()
	p.regenHP(now)
	p.regenHP(now.Add(10 * time.Second))
	if p.HP != 0 {
		t.Errorf("a player at 0 health regrew to %d", p.HP)
	}
}

func TestTheWorldTickRegrowsHealthInEveryRoom(t *testing.T) {
	p, w := hurt(10, 20)
	w.RoomMap = map[int32]*Room{1: p.Location}
	now := time.Now()
	w.tick(now)
	w.tick(now.Add(hpRegenEvery))
	if p.HP != 12 {
		t.Errorf("after the world ticked, health = %d, want 12", p.HP)
	}
}
