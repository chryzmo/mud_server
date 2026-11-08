package worldfile

import (
	"strings"
	"testing"
	"time"
)

const goodEvents = `events:
  opening:
    title: Opening
    at: "2027-01-07 12:00 America/New_York"
    duration: 30m
    steps:
      - {announce: "The Hall is open."}
      - {after: 5m, set_flag: con-open}
      - {after: 10m, room_say: {room: yard, text: "The yard hums."}}
    finish:
      - {clear_flag: con-open}
  nightly:
    title: Nightly bell
    at: "2027-01-07 21:00 America/New_York"
    repeat: {every: 24h, count: 3}
    steps:
      - {announce: "The bell tolls."}
`

func withEvents(events string) map[string]string {
	files := ruled(goodRules, "")
	files["e.yaml"] = events
	return files
}

func TestGoodEventsAreCleanAndResolve(t *testing.T) {
	files := withEvents(goodEvents)
	w, res, err := ValidateDir(write(t, files))
	if err != nil || len(res.Errors) != 0 || len(res.Warnings) != 0 {
		t.Fatalf("err %v errors %v warnings %v", err, res.Errors, res.Warnings)
	}
	evs := w.ResolveEvents()
	if len(evs) != 2 || evs[0].Key != "nightly" || evs[1].Key != "opening" {
		t.Fatalf("events = %v", evs)
	}
	open := evs[1]
	if want := time.Date(2027, 1, 7, 17, 0, 0, 0, time.UTC); !open.Start.Equal(want) { // EST is UTC-5
		t.Errorf("start = %v, want %v", open.Start, want)
	}
	if open.Duration != 30*time.Minute || len(open.Steps) != 3 || open.Steps[1].Offset != 5*time.Minute || len(open.Finish) != 1 {
		t.Errorf("opening = %+v", open)
	}
	nightly := evs[0]
	if nightly.Count != 3 || nightly.Every != 24*time.Hour || nightly.Duration != time.Minute {
		t.Errorf("nightly = %+v", nightly)
	}
	if got, want := nightly.StartOf(2), time.Date(2027, 1, 10, 2, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("third night = %v, want %v", got, want)
	}
}

func TestDaylightSavingIsHonoured(t *testing.T) {
	got, err := ParseAt("2027-07-01 12:00 America/New_York")
	if err != nil || !got.Equal(time.Date(2027, 7, 1, 16, 0, 0, 0, time.UTC)) { // EDT is UTC-4
		t.Errorf("got %v, %v", got, err)
	}
}

func TestEventProblems(t *testing.T) {
	cases := []struct{ name, events, want string }{
		{"bad zone", strings.Replace(goodEvents, "America/New_York", "Mars/Olympus", 1), "not a time zone name"},
		{"bad time", strings.Replace(goodEvents, "12:00", "25:00", 1), "at must look like"},
		{"no zone", strings.Replace(goodEvents, " America/New_York", "", 1), "at must look like"},
		{"no title", strings.Replace(goodEvents, "title: Opening", "title: ''", 1), "a title of 1 to 60"},
		{"no steps", strings.Replace(goodEvents, "steps:\n      - {announce: \"The bell tolls.\"}", "steps: []", 1), "0 steps"},
		{"two actions", strings.Replace(goodEvents, `{announce: "The Hall is open."}`, `{announce: "x", set_flag: y}`, 1), "2 things set"},
		{"empty step", strings.Replace(goodEvents, `{announce: "The Hall is open."}`, `{after: 1m}`, 1), "has nothing set"},
		{"bad after", strings.Replace(goodEvents, "after: 5m", "after: soon", 1), `"soon" is not a time`},
		{"after past the end", strings.Replace(goodEvents, "after: 10m", "after: 45m", 1), "ends (30m0s) before its last step"},
		{"unknown room", strings.Replace(goodEvents, "room: yard", "room: moat", 1), `"moat" is not a room`},
		{"bad flag", strings.Replace(goodEvents, "set_flag: con-open", "set_flag: Con Open", 1), "world flag name"},
		{"finish takes no after", strings.Replace(goodEvents, "{clear_flag: con-open}", "{after: 1m, clear_flag: con-open}", 1), "take no after"},
		{"repeat too fast", strings.Replace(goodEvents, "every: 24h", "every: 10m", 1), "repeat every between"},
		{"repeat once", strings.Replace(goodEvents, "count: 3", "count: 1", 1), "between 2 and 10"},
		{"repeat overlaps itself", strings.Replace(goodEvents, "repeat: {every: 24h, count: 3}", "repeat: {every: 1h, count: 3}\n    duration: 90m", 1), "lasts longer"},
		{"long announce", strings.Replace(goodEvents, "The Hall is open.", strings.Repeat("x", 400), 1), "at most 300"},
		{"bad key", strings.Replace(goodEvents, "  opening:", "  Opening Night:", 1), "lower case words"},
	}
	for _, c := range cases {
		res := validate(t, withEvents(c.events))
		if !has(res.Errors, c.want) {
			t.Errorf("%s: want an error containing %q, got %v", c.name, c.want, res.Errors)
		}
	}
	files := withEvents(goodEvents)
	files["f.yaml"] = goodEvents
	if res := validate(t, files); !has(res.Errors, "duplicate event key") {
		t.Errorf("errors %v", res.Errors)
	}
}

// A rule or gate that reads a world flag needs an event that sets it.
func TestWorldFlagsMustBeSetByAnEvent(t *testing.T) {
	rule := "    rules:\n      - on: {verb: PULL, target: lever}\n        if: [{world_flag: con-open}]\n        do: [{add: {counter: pulls, by: 1}}]\n"
	if res := validate(t, ruled(rule, "")); !has(res.Errors, `world flag "con-open" is read`) {
		t.Errorf("without an event: %v", res.Errors)
	}
	files := ruled(rule, "")
	files["e.yaml"] = goodEvents
	if res := validate(t, files); len(res.Errors) != 0 {
		t.Errorf("with the event: %v", res.Errors)
	}

	gate := "    rules:\n      - on: {verb: PULL, target: lever}\n        do: [{add: {counter: pulls, by: 1}}]\n    gates:\n      door: {exit: {dir: D, to: yard}, mode: locked, message: Shut., open_when: con-open}\n"
	if res := validate(t, ruled(gate, "")); !has(res.Errors, `world flag "con-open" is read`) || has(res.Errors, "no rule opens this gate") {
		t.Errorf("gate without an event: %v", res.Errors)
	}
	files = ruled(gate, "")
	files["e.yaml"] = goodEvents
	if res := validate(t, files); len(res.Errors) != 0 {
		t.Errorf("gate with the event: %v", res.Errors)
	}
}

func TestRenderShowsEvents(t *testing.T) {
	out := render(t, withEvents(goodEvents))
	for _, want := range []string{"## Events", "Opening (`opening`)", "Starts Thu 2027-01-07 17:00 UTC; runs 30m0s.", `announce "The Hall is open."`, "+5m0s: set world flag `con-open`", "when it ends: clear world flag `con-open`", "3 times, every 24h0m0s"} {
		if !strings.Contains(out, want) {
			t.Errorf("overview is missing %q\n%s", want, out)
		}
	}
}

func TestSpawnStepsAndBossNamesAreCheckedAndRendered(t *testing.T) {
	world := func(step, npc string) map[string]string {
		return map[string]string{"w.yaml": twoRooms + "\nnpcs:\n  warden: {race: WARDEN, hp: 5, combat_tick_ms: 1000, behavior: orc" + npc + "}\nevents:\n  duel:\n    title: Duel\n    at: \"2099-01-07 12:00 America/New_York\"\n    steps:\n      - " + step + "\n"}
	}
	if res := validate(t, world("{spawn: {npc: warden, room: hall}}", ", boss: true, name: Old Grak")); len(res.Errors) != 0 {
		t.Errorf("a good spawn is an error: %v", res.Errors)
	}
	for step, want := range map[string]string{
		"{spawn: {npc: ghost, room: hall}}":                `"ghost" is not an npc`,
		"{spawn: {npc: warden, room: nowhere}}":            `"nowhere" is not a room`,
		"{spawn: {npc: warden, room: hall}, announce: hi}": "has 2 things set",
	} {
		if res := validate(t, world(step, "")); !has(res.Errors, want) {
			t.Errorf("step %s: want %q, got %v", step, want, res.Errors)
		}
	}
	if res := validate(t, world("{spawn: {npc: warden, room: hall}}", ", name: '"+strings.Repeat("x", 41)+"'")); !has(res.Errors, "at most 40 characters") {
		t.Errorf("a long name is accepted: %v", res.Errors)
	}
	if out := render(t, world("{spawn: {npc: warden, room: hall}}", "")); !strings.Contains(out, "spawn `warden` in `hall`") {
		t.Errorf("the overview lacks the spawn step\n%s", out)
	}
}
