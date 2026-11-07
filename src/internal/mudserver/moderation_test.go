package mudserver

import (
	"fmt"
	"testing"
	"time"
)

func eastern(t *testing.T, y int, m time.Month, d, h, min int) time.Time {
	t.Helper()
	return time.Date(y, m, d, h, min, 0, 0, easternZone)
}

func TestQuietHoursAreOneToEightEasternWhateverTheServerClockSays(t *testing.T) {
	for _, tc := range []struct {
		at    time.Time
		quiet bool
	}{
		{eastern(t, 2027, 1, 8, 0, 59), false}, {eastern(t, 2027, 1, 8, 1, 0), true}, {eastern(t, 2027, 1, 8, 7, 59), true},
		{eastern(t, 2027, 1, 8, 8, 0), false}, {eastern(t, 2027, 1, 8, 15, 0), false},
		{eastern(t, 2027, 1, 8, 3, 0).UTC(), true}, // the same moment in UTC
	} {
		if got := quietHours(tc.at); got != tc.quiet {
			t.Errorf("%v: quiet = %v, want %v", tc.at, got, tc.quiet)
		}
	}
	if got, want := quietEnd(eastern(t, 2027, 1, 8, 3, 0)), eastern(t, 2027, 1, 8, 8, 0); !got.Equal(want) {
		t.Errorf("end of the night at 03:00 = %v, want %v", got, want)
	}
	if got, want := quietEnd(eastern(t, 2027, 1, 8, 20, 0)), eastern(t, 2027, 1, 9, 8, 0); !got.Equal(want) {
		t.Errorf("next 08:00 from 20:00 = %v, want %v", got, want)
	}
}

func TestNightSlowsChatAndLengthensTheGuestWaitUntilAGMSaysOtherwise(t *testing.T) {
	ms := &MudServer{}
	night, day := eastern(t, 2027, 1, 8, 3, 0), eastern(t, 2027, 1, 8, 13, 0)
	if ms.effectiveSlow(night) != quietSlow || ms.guestWait(time.Minute, night) != quietGuestWait {
		t.Errorf("night: slow %v wait %v", ms.effectiveSlow(night), ms.guestWait(time.Minute, night))
	}
	if ms.effectiveSlow(day) != 0 || ms.guestWait(time.Minute, day) != time.Minute {
		t.Errorf("day: slow %v wait %v", ms.effectiveSlow(day), ms.guestWait(time.Minute, day))
	}
	ms.chat.slow = time.Minute // the GM's own slow mode is never loosened by the night default
	if ms.effectiveSlow(night) != time.Minute {
		t.Errorf("a slower GM setting must win, got %v", ms.effectiveSlow(night))
	}
	ms.chat.slow = 0
	ms.gmOverridesQuiet(night)
	if ms.effectiveSlow(night) != 0 || ms.guestWait(time.Minute, night) != time.Minute {
		t.Errorf("after a GM override the night defaults apply: slow %v wait %v", ms.effectiveSlow(night), ms.guestWait(time.Minute, night))
	}
	if next := eastern(t, 2027, 1, 9, 2, 0); ms.effectiveSlow(next) != quietSlow {
		t.Errorf("the override must end with the night, got %v the next night", ms.effectiveSlow(next))
	}
}

func TestNormalizeLine(t *testing.T) {
	for _, in := range []string{"Buy GOLD now!!!", "buy   gold  now", "  buy gold now? "} {
		if got := normalizeLine(in); got != "buy gold now" {
			t.Errorf("normalizeLine(%q) = %q", in, got)
		}
	}
}

func TestAGMIsNeverStruckForRepeatsOrFlooding(t *testing.T) {
	ms := &MudServer{}
	gm := &MudPlayer{gm: true}
	now := time.Unix(1000, 0)
	for i := 0; i < 20; i++ {
		if ms.checkRepeat(gm, "the same thing again", now) {
			t.Fatal("a GM's repeat was struck")
		}
		ms.noteRefused(gm, now)
	}
	if gm.mod.strikes != 0 {
		t.Errorf("strikes = %d", gm.mod.strikes)
	}
}

func TestStrikesEscalateAndAQuietHourForgetsThem(t *testing.T) {
	var m modState
	now := time.Unix(1000, 0)
	var got []time.Duration
	for i := 0; i < 4; i++ {
		got = append(got, m.nextMute(now))
		now = now.Add(time.Minute)
	}
	if want := []time.Duration{2 * time.Minute, 10 * time.Minute, 30 * time.Minute, 30 * time.Minute}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("mutes = %v, want %v", got, want)
	}
	now = now.Add(strikeForget + time.Second)
	if d := m.nextMute(now); d != 2*time.Minute {
		t.Errorf("after a quiet hour the mute was %v, want the first step again", d)
	}
}
