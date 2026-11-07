package mudserver

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mud_server/src/internal/worldfile"
)

// The scheduler's state machine, driven by a clock the test moves (the integration tests in
// src/events_test.go cover the wiring through a real server).

type schedRig struct {
	s   *scheduler
	ms  *MudServer
	log []string
}

var t0 = time.Date(2027, 1, 7, 17, 0, 0, 0, time.UTC)

func newRig(t *testing.T, defs ...*worldfile.ResolvedEvent) *schedRig {
	t.Helper()
	ms := &MudServer{
		players: map[string]*MudPlayer{},
		World:   &World{worldFlags: map[string]bool{}, RoomMap: map[int32]*Room{}},
		stateQ:  make(chan stateWrite, 1000),
	}
	r := &schedRig{ms: ms}
	r.s = &scheduler{ms: ms}
	for _, d := range defs {
		r.s.events = append(r.s.events, &liveEvent{def: d})
	}
	r.s.ran = func(step worldfile.ResolvedStep, stale bool) {
		what := step.Kind + ":" + step.Text + step.Flag
		if stale {
			what += " (stale)"
		}
		r.log = append(r.log, what)
	}
	return r
}

func (r *schedRig) tickAt(d time.Duration) { r.s.tick(t0.Add(d)) }

func (r *schedRig) took() string {
	out := strings.Join(r.log, ", ")
	r.log = nil
	return out
}

func event(key string, count int, every time.Duration, duration time.Duration, steps ...worldfile.ResolvedStep) *worldfile.ResolvedEvent {
	return &worldfile.ResolvedEvent{Key: key, Title: strings.ToUpper(key), Start: t0, Count: count, Every: every, Duration: duration, Steps: steps,
		Finish: []worldfile.ResolvedStep{{Kind: worldfile.StepClearFlag, Flag: key + "-open"}}}
}

func say(after time.Duration, text string) worldfile.ResolvedStep {
	return worldfile.ResolvedStep{Offset: after, Kind: worldfile.StepAnnounce, Text: text}
}

func flag(after time.Duration, name string) worldfile.ResolvedStep {
	return worldfile.ResolvedStep{Offset: after, Kind: worldfile.StepSetFlag, Flag: name}
}

func TestAnEventRunsItsStepsInTimeAndThenFinishes(t *testing.T) {
	r := newRig(t, event("door", 1, 0, 10*time.Minute, say(0, "open"), flag(5*time.Minute, "door-open")))
	r.tickAt(-time.Second)
	if got := r.took(); got != "" {
		t.Fatalf("before the start: %q", got)
	}
	r.tickAt(0)
	if got := r.took(); got != "announce:open" {
		t.Errorf("at the start: %q", got)
	}
	r.tickAt(4 * time.Minute)
	if got := r.took(); got != "" || r.ms.World.worldFlags["door-open"] {
		t.Errorf("early: %q flag %v", got, r.ms.World.worldFlags)
	}
	r.tickAt(5 * time.Minute)
	if got := r.took(); got != "set_flag:door-open" || !r.ms.World.worldFlags["door-open"] {
		t.Errorf("at five minutes: %q flag %v", got, r.ms.World.worldFlags)
	}
	r.tickAt(10 * time.Minute) // the end: finish steps clear the flag
	if got := r.took(); got != "clear_flag:door-open" || r.ms.World.worldFlags["door-open"] {
		t.Errorf("at the end: %q flag %v", got, r.ms.World.worldFlags)
	}
	r.tickAt(time.Hour)
	if got := r.took(); got != "" {
		t.Errorf("an ended one-off ran again: %q", got)
	}
}

func TestAWindowThatPassedWhileDownIsSkippedAndNeverReplayed(t *testing.T) {
	r := newRig(t, event("gone", 1, 0, 10*time.Minute, say(0, "late"), flag(time.Minute, "gone-open")))
	r.tickAt(3 * time.Hour)
	if got := r.took(); got != "" || len(r.ms.World.worldFlags) != 0 {
		t.Errorf("a missed event ran: %q %v", got, r.ms.World.worldFlags)
	}
	if r.s.events[0].occ != 1 {
		t.Errorf("occurrence = %d, want it marked as passed", r.s.events[0].occ)
	}
}

func TestResumingInsideTheWindowSetsFlagsButStaysQuiet(t *testing.T) {
	r := newRig(t, event("mid", 1, 0, time.Hour, say(0, "hello"), flag(time.Minute, "mid-open"), say(30*time.Minute, "halfway")))
	r.tickAt(20 * time.Minute) // the server came back twenty minutes in
	if got := r.took(); got != "announce:hello (stale), set_flag:mid-open (stale)" {
		t.Errorf("resume: %q", got)
	}
	if !r.ms.World.worldFlags["mid-open"] {
		t.Error("the flag a missed step sets was not set")
	}
	r.tickAt(30 * time.Minute)
	if got := r.took(); got != "announce:halfway" {
		t.Errorf("a step still to come: %q", got)
	}
}

func TestARepeatingEventPlaysEachOccurrenceAndSkipsMissedOnes(t *testing.T) {
	r := newRig(t, event("bell", 3, 24*time.Hour, 10*time.Minute, say(0, "ding")))
	r.tickAt(0)
	r.tickAt(11 * time.Minute) // first occurrence over
	r.took()
	r.tickAt(24*time.Hour + time.Minute)
	if got := r.took(); got != "announce:ding" {
		t.Errorf("second night: %q", got)
	}
	r.tickAt(24*time.Hour + 11*time.Minute) // the second night ends
	r.took()
	r.tickAt(48*time.Hour + time.Hour) // slept through the third
	if got := r.took(); got != "" {
		t.Errorf("a missed night was replayed: %q", got)
	}
	if e := r.s.events[0]; e.occ != 3 || e.running {
		t.Errorf("occ %d running %v, want all three used up", e.occ, e.running)
	}
}

func TestGMControls(t *testing.T) {
	r := newRig(t, event("show", 2, 24*time.Hour, 10*time.Minute, say(0, "go"), flag(time.Minute, "show-open")))
	now := t0.Add(-time.Hour)
	if got := r.s.gmControl("START", "nope", now); !strings.Contains(got, "No event") {
		t.Errorf("unknown event: %q", got)
	}
	if got := r.s.gmControl("STOP", "show", now); !strings.Contains(got, "not running") {
		t.Errorf("stop when idle: %q", got)
	}
	if got := r.s.gmControl("START", "show", now); got != "Started SHOW." || r.took() != "announce:go" {
		t.Errorf("start: %q", got)
	}
	r.s.tick(now.Add(time.Minute)) // the step a minute in runs relative to the manual start
	if !r.ms.World.worldFlags["show-open"] {
		t.Error("a manual start did not run its steps from the moment it began")
	}
	if got := r.s.gmControl("START", "show", now); !strings.Contains(got, "already running") {
		t.Errorf("double start: %q", got)
	}
	if got := r.s.gmControl("STOP", "show", now); got != "Stopped SHOW." || r.ms.World.worldFlags["show-open"] {
		t.Errorf("stop: %q flag %v (finish steps should clear it)", got, r.ms.World.worldFlags)
	}
	if got := r.s.gmControl("SKIP", "show", now); !strings.Contains(got, "Skipped") || r.s.events[0].occ != 2 {
		t.Errorf("skip: %q occ %d", got, r.s.events[0].occ)
	}
	if got := r.s.gmControl("SKIP", "show", now); !strings.Contains(got, "nothing waiting") {
		t.Errorf("skip with none left: %q", got)
	}
	if got := r.s.gmControl("START", "show", now); got != "Started SHOW." { // every occurrence used: run once more
		t.Errorf("start after the last: %q", got)
	}
}

func TestProgressIsQueuedForTheDatabase(t *testing.T) {
	r := newRig(t, event("saved", 1, 0, 10*time.Minute, say(0, "x"), flag(time.Minute, "saved-open")))
	r.tickAt(2 * time.Minute)
	var writes []string
	for len(r.ms.stateQ) > 0 {
		w := <-r.ms.stateQ
		writes = append(writes, fmt.Sprintf("%s %s %s", w.kind, w.key, w.value[:min(len(w.value), 9)]))
	}
	want := []string{"event saved running|0", "world saved-open ", "event saved running|0"}
	if strings.Join(writes, ";") != strings.Join(want, ";") {
		t.Errorf("writes = %q, want %q", writes, want)
	}
}

func TestEventsSummaryHidesHiddenOnesAndSaysWhatIsNext(t *testing.T) {
	soon := event("soon", 1, 0, 10*time.Minute, say(0, "x"))
	secret := event("secret", 1, 0, 10*time.Minute, say(0, "x"))
	secret.Hidden = true
	r := newRig(t, soon, secret)
	got := r.s.summary(t0.Add(-2 * time.Hour))
	if !strings.Contains(got, "Coming: SOON, Thu 12:00 PM EST (in 2 hours)") || strings.Contains(got, "SECRET") {
		t.Errorf("summary = %q", got)
	}
	r.tickAt(time.Minute)
	if got := r.s.summary(t0.Add(time.Minute)); !strings.Contains(got, "On now: SOON (ends in 9 minutes)") {
		t.Errorf("summary while running = %q", got)
	}
	if got := r.s.gmList(t0.Add(time.Minute)); !strings.Contains(got, "secret [hidden]") {
		t.Errorf("the GM list leaves out the hidden event: %q", got)
	}
	if got := newRig(t).s.summary(t0); got != "Nothing is scheduled right now." {
		t.Errorf("empty summary = %q", got)
	}
}
