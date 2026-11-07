package mudserver

import (
	"testing"
	"time"
)

func TestRedeemGuardCoolsDownAndEscalatesThenForgives(t *testing.T) {
	var g redeemGuard
	now := time.Unix(1000, 0)
	for i := 0; i < redeemFailures-1; i++ {
		g.fail(now)
		now = now.Add(time.Second)
	}
	if g.locked(now) != 0 {
		t.Fatal("locked before the limit")
	}
	g.fail(now)
	if got := g.locked(now); got != redeemCooldown {
		t.Fatalf("first cooldown %v", got)
	}
	now = now.Add(redeemCooldown + time.Second)
	if g.locked(now) != 0 {
		t.Fatal("still locked after the cooldown")
	}
	for i := 0; i < redeemFailures; i++ {
		g.fail(now)
	}
	if got := g.locked(now); got != 2*redeemCooldown {
		t.Fatalf("second cooldown %v, want it doubled", got)
	}
	now = now.Add(redeemForgive + 3*redeemCooldown)
	for i := 0; i < redeemFailures; i++ {
		g.fail(now)
	}
	if got := g.locked(now); got != redeemCooldown {
		t.Fatalf("after a quiet spell the cooldown was %v, want the first again", got)
	}
}

func TestFailuresOutsideTheWindowDoNotAddUp(t *testing.T) {
	var g redeemGuard
	now := time.Unix(1000, 0)
	for i := 0; i < 3*redeemFailures; i++ {
		g.fail(now)
		now = now.Add(redeemWindow) // one wrong code a minute is not a brute force
		if g.locked(now) != 0 {
			t.Fatalf("locked after %d slow failures", i+1)
		}
	}
}
