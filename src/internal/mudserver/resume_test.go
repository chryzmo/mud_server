package mudserver

import (
	"fmt"
	"testing"
)

func ringPlayer(size int, sent int) *MudPlayer {
	p := &MudPlayer{replaySize: size}
	for i := 0; i < sent; i++ {
		p.record([]byte(fmt.Sprint("frame", i)))
	}
	return p
}

func framesAsStrings(frames [][]byte) []string {
	out := make([]string, len(frames))
	for i, f := range frames {
		out[i] = string(f)
	}
	return out
}

func TestFramesAfterReplaysExactlyWhatTheClientMissed(t *testing.T) {
	p := ringPlayer(5, 7) // frames 0..6 sent; the ring holds 2..6

	for _, tc := range []struct {
		received uint64
		want     []string
		ok       bool
	}{
		{7, nil, true},                          // caught up
		{6, []string{"frame6"}, true},           // missed one
		{5, []string{"frame5", "frame6"}, true}, // missed two (across the ring's wrap)
		{2, []string{"frame2", "frame3", "frame4", "frame5", "frame6"}, true}, // missed exactly the ring's worth
		{1, nil, false}, // one too many: a gap
		{0, nil, false}, // everything
		{8, nil, false}, // claims to have seen more than were sent
	} {
		got, ok := p.framesAfter(tc.received)
		if ok != tc.ok || fmt.Sprint(framesAsStrings(got)) != fmt.Sprint(tc.want) {
			t.Errorf("framesAfter(%d) = %v, %v; want %v, %v", tc.received, framesAsStrings(got), ok, tc.want, tc.ok)
		}
	}
}

func TestFramesAfterWithoutARing(t *testing.T) {
	p := ringPlayer(0, 3)
	if p.sent != 3 {
		t.Fatalf("sent = %d; frames are counted even when none are kept", p.sent)
	}
	if _, ok := p.framesAfter(3); !ok {
		t.Error("a client that is caught up needs nothing, ring or not")
	}
	if _, ok := p.framesAfter(2); ok {
		t.Error("with no ring, a missed frame is a gap")
	}
}

func TestRingKeepsReferencesNotCopies(t *testing.T) {
	p := ringPlayer(3, 0)
	shared := []byte("one shared encoding")
	p.record(shared)
	frames, _ := p.framesAfter(0)
	if len(frames) != 1 || &frames[0][0] != &shared[0] {
		t.Error("the ring must hold the shared slice, not a copy (format once)")
	}
}

func TestReplayFramesStayWellInsideTheOutboundQueue(t *testing.T) {
	for _, tc := range []struct{ configured, queue, want int }{
		{0, 0, 100},    // defaults: 100 frames, 256 queue
		{1000, 0, 240}, // clamped to queue - 16
		{50, 100, 50},
		{500, 100, 84},
		{10, 8, 1}, // never below 1
	} {
		ms := &MudServer{}
		ms.Configuration.Connection.ReplayFrames = tc.configured
		ms.Configuration.Connection.OutboundQueueSize = tc.queue
		if got := ms.replayFrames(); got != tc.want {
			t.Errorf("ReplayFrames %d, queue %d: got %d, want %d", tc.configured, tc.queue, got, tc.want)
		}
	}
}

func TestCanResumeOnlyForGuestsWithATokenAndGrace(t *testing.T) {
	for _, tc := range []struct {
		p    MudPlayer
		want bool
	}{
		{MudPlayer{guest: true, tokenHash: "h"}, true},
		{MudPlayer{guest: true}, false},
		{MudPlayer{tokenHash: "h"}, false},
		{MudPlayer{}, false},
	} {
		if got := tc.p.canResume(); got != tc.want {
			t.Errorf("%+v canResume = %v", tc.p, got)
		}
	}
}
