package mudserver

import "testing"

func TestDropHitsHonoursItsPercentage(t *testing.T) {
	for i := 0; i < 1000; i++ {
		if !dropHits(100) {
			t.Fatal("a 100% drop missed")
		}
	}
	hits := 0
	for i := 0; i < 20000; i++ {
		if dropHits(1) {
			hits++
		}
	}
	if hits < 50 || hits > 450 { // 1% of 20000 is 200
		t.Errorf("a 1%% drop hit %d times in 20000", hits)
	}
}
