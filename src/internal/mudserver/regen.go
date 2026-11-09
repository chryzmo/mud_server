package mudserver

import "time"

/*
Health regrows by itself, so a player is back on their feet within about half a minute and the game stays quick:
every hpRegenEvery a hurt player gains a tenth of their maximum health (at least hpRegenMin). It does not run
while they are fighting: a blow dealt, taken or swung at them pauses it, and it starts again hpCombatGrace after
the last one. Magic regrows separately (cast.go). The loop calls regenHP for every player on each world tick.
*/

const (
	hpRegenEvery   = 3 * time.Second
	hpRegenMin     = 2
	hpRegenPercent = 10
	hpCombatGrace  = 5 * time.Second
)

// markCombat records that the player is in a fight at now. Loop only.
func (mp *MudPlayer) markCombat(now time.Time) {
	mp.lastCombatAt = now
	mp.hpNextAt = time.Time{}
}

// regenHP regrows the player's health if they are hurt and out of a fight, and tells their client. Loop only.
func (mp *MudPlayer) regenHP(now time.Time) {
	max := mp.EffectiveMaxHP()
	if mp.HP <= 0 || mp.HP >= max || now.Sub(mp.lastCombatAt) < hpCombatGrace {
		mp.hpNextAt = time.Time{}
		return
	}
	if mp.hpNextAt.IsZero() {
		mp.hpNextAt = now.Add(hpRegenEvery)
		return
	}
	if now.Before(mp.hpNextAt) {
		return
	}
	mp.HP = min(max, mp.HP+maxInt(hpRegenMin, max*hpRegenPercent/100))
	mp.hpNextAt = now.Add(hpRegenEvery)
	mp.dirty = true
	mp.SendStatUpdate()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
