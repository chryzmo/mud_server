package worldfile

import (
	"strings"
	"testing"
)

// base is a tiny world with one feature, one item and one other room; each case adds rules or gates to the hall.
const rulesBase = `
start_room: hall
rooms:
  hall:
    name: Hall
    description: A hall.
    exits: {E: yard}
    features:
      lever: {names: [lever], examine: "The lever is at {pulls}."}
    people:
      guard: {names: [Guard], examine: x, greeting: x}
%s
  yard:
    name: Yard
    description: A yard.
    exits: {W: hall}
items:
  key: {name: KEY, description: A key.}
%s`

func ruled(extraHall, extraItems string) map[string]string {
	return map[string]string{"w.yaml": strings.Replace(strings.Replace(rulesBase, "%s", extraHall, 1), "%s", extraItems, 1)}
}

const goodRules = `    rules:
      - id: pull
        on: {verb: PULL, target: lever}
        do:
          - add: {counter: pulls, by: 1, wrap: [1, 3]}
          - say: "Click, {player}."
          - run: after
      - id: after
        if: [{counter_at_least: {pulls: 2}}]
        unless: [{flag: done}]
        do:
          - set_flag: done
          - give_item: key
          - open: {gate: trap}
    gates:
      trap: {exit: {dir: D, to: cellar}, mode: locked, message: "Shut."}
  cellar:
    name: Cellar
    description: A cellar.
    exits: {U: hall}
`

func TestAGoodPuzzleIsClean(t *testing.T) {
	res := validate(t, ruled(goodRules, ""))
	if len(res.Errors) != 0 || len(res.Warnings) != 0 {
		t.Fatalf("errors %v warnings %v", res.Errors, res.Warnings)
	}
}

func TestRuleProblems(t *testing.T) {
	cases := []struct {
		name, rules, items, want string
		warn                     bool
	}{
		{"no effects", "    rules:\n      - on: {verb: PULL, target: lever}\n        do: []\n", "", "needs at least one effect", false},
		{"too many effects", "    rules:\n      - on: {verb: PULL, target: lever}\n        do: [" + strings.Repeat("{say: x}, ", MaxEffects+1) + "]\n", "", "at most 12 per rule", false},
		{"named rule without an id", "    rules:\n      - do: [{say: x}]\n", "", "needs an id", false},
		{"two triggers", "    rules:\n      - on: {verb: PULL, target: lever, say: hi}\n        do: [{say: x}]\n", "", "exactly one of verb, say, enter or examine", false},
		{"no trigger kind", "    rules:\n      - on: {target: lever}\n        do: [{say: x}]\n", "", "exactly one of verb, say, enter or examine", false},
		{"unknown verb", "    rules:\n      - on: {verb: DANCE, target: lever}\n        do: [{say: x}]\n", "", `"DANCE" is not a verb`, false},
		{"unknown target", "    rules:\n      - on: {verb: PULL, target: levr}\n        do: [{say: x}]\n", "", `"levr" is not a feature or person`, false},
		{"unknown with", "    rules:\n      - on: {verb: USE, target: lever, with: wand}\n        do: [{say: x}]\n", "", `"wand" is not an item`, false},
		{"set needs a value", "    rules:\n      - on: {verb: SET, target: lever}\n        do: [{say: x}]\n", "", "SET needs a value", false},
		{"value without set", "    rules:\n      - on: {verb: PULL, target: lever, value: '3'}\n        do: [{say: x}]\n", "", "value is only for SET", false},
		{"examine unknown target", "    rules:\n      - on: {examine: levr}\n        do: [{set_flag: seen}]\n", "", `"levr" is not a feature or person`, false},
		{"examine with a target", "    rules:\n      - on: {examine: lever, target: lever}\n        do: [{set_flag: seen}]\n", "", "go with a verb", false},
		{"examine and a verb", "    rules:\n      - on: {examine: lever, verb: PULL}\n        do: [{set_flag: seen}]\n", "", "exactly one of verb, say, enter or examine", false},
		{"say with a target", "    rules:\n      - on: {say: hi, target: lever}\n        do: [{say: x}]\n", "", "go with a verb", false},
		{"two things in a condition", "    rules:\n      - on: {verb: PULL, target: lever}\n        if: [{flag: a, not_flag: b}]\n        do: [{say: x}]\n", "", "2 things set", false},
		{"empty condition", "    rules:\n      - on: {verb: PULL, target: lever}\n        if: [{}]\n        do: [{say: x}]\n", "", "has nothing set", false},
		{"two effects in one", "    rules:\n      - on: {verb: PULL, target: lever}\n        do: [{say: x, set_flag: a}]\n", "", "2 things set", false},
		{"has_item unknown", "    rules:\n      - on: {verb: PULL, target: lever}\n        if: [{has_item: wand}]\n        do: [{say: x}]\n", "", `"wand" is not an item`, false},
		{"give_item unknown", "    rules:\n      - on: {verb: PULL, target: lever}\n        do: [{give_item: wand}]\n", "", `"wand" is not an item`, false},
		{"take_item unknown", "    rules:\n      - on: {verb: PULL, target: lever}\n        do: [{take_item: wand}]\n", "", `"wand" is not an item`, false},
		{"flag never set", "    rules:\n      - on: {verb: PULL, target: lever}\n        if: [{flag: nope}]\n        do: [{say: x}]\n", "", `flag "nope" is read`, false},
		{"not_flag never set", "    rules:\n      - on: {verb: PULL, target: lever}\n        unless: [{flag: x}]\n        if: [{not_flag: never}]\n        do: [{set_flag: x}]\n", "", `tested with not_flag`, true},
		{"counter never changed", "    rules:\n      - on: {verb: PULL, target: lever}\n        if: [{counter: {ghost: 1}}]\n        do: [{say: x}]\n", "", `counter "ghost" is never changed`, false},
		{"zero add", "    rules:\n      - on: {verb: PULL, target: lever}\n        do: [{add: {counter: pulls, by: 0}}]\n", "", "by cannot be 0", false},
		{"bad wrap", "    rules:\n      - on: {verb: PULL, target: lever}\n        do: [{add: {counter: pulls, by: 1, wrap: [5, 2]}}]\n", "", "wrap is [min, max]", false},
		{"empty append", "    rules:\n      - on: {verb: PULL, target: lever}\n        do: [{append: {sequence: s}}]\n", "", "needs a sequence and a value", false},
		{"bad hold", "    rules:\n      - on: {verb: PULL, target: lever}\n        do: [{hold: {key: plate, seconds: 0}}]\n", "", "seconds from 1 to 3600", false},
		{"teleport unknown", "    rules:\n      - on: {verb: PULL, target: lever}\n        do: [{teleport: moon}]\n", "", `"moon" is not a room`, false},
		{"stop false", "    rules:\n      - on: {verb: PULL, target: lever}\n        do: [{stop: false}]\n", "", "use `stop: true`", false},
		{"run unknown", "    rules:\n      - on: {verb: PULL, target: lever}\n        do: [{run: nothing}]\n", "", `no rule with the id "nothing"`, false},
		{"duplicate id", "    rules:\n      - id: a\n        do: [{say: x}]\n      - id: a\n        do: [{say: y}]\n", "", `id "a" is used twice`, false},
		{"cycle", "    rules:\n      - id: a\n        do: [{run: b}]\n      - id: b\n        do: [{run: a}]\n", "", "in a cycle", false},
		{"too deep", "    rules:\n      - id: a\n        do: [{run: b}]\n      - id: b\n        do: [{run: c}]\n      - id: c\n        do: [{run: d}]\n      - id: d\n        do: [{run: e}]\n      - id: e\n        do: [{run: f}]\n      - id: f\n        do: [{say: x}]\n", "", "more than 4 deep", false},
		{"unknown placeholder", "    rules:\n      - on: {verb: PULL, target: lever}\n        do: [{say: 'You have {gold} coins'}]\n", "", "{gold} is not a counter", false},
		{"unused named rule", "    rules:\n      - id: lonely\n        do: [{say: x}]\n", "", "no rule runs this one", true},
		{"shadowed rule", "    rules:\n      - on: {verb: PULL, target: lever}\n        do: [{say: x}]\n      - on: {verb: PULL, target: lever}\n        do: [{say: y}]\n", "", "can never run", true},
		{"bound item with no source", "", "  token: {name: TOKEN, description: A token., bound: true}\n", "no rule or monster drop gives it", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := validate(t, ruled(tc.rules, tc.items))
			list := res.Errors
			if tc.warn {
				list = res.Warnings
			}
			if !has(list, tc.want) {
				t.Errorf("want a problem containing %q; errors %v warnings %v", tc.want, res.Errors, res.Warnings)
			}
		})
	}
}

func TestAnExamineRuleIsClean(t *testing.T) {
	rules := "    rules:\n      - on: {verb: PULL, target: lever}\n        do: [{add: {counter: pulls, by: 1}}]\n      - on: {examine: lever}\n        do: [{set_flag: read-lever}]\n      - on: {examine: guard}\n        unless: [{flag: read-lever}]\n        do: [{set_flag: read-guard}]\n"
	res := validate(t, ruled(rules, ""))
	if len(res.Errors) != 0 {
		t.Errorf("errors: %v", res.Errors)
	}
}

func TestTooManyRules(t *testing.T) {
	var b strings.Builder
	b.WriteString("    rules:\n")
	for i := 0; i <= MaxRules; i++ {
		b.WriteString("      - on: {verb: PULL, target: lever, value: ''}\n        do: [{say: x}]\n")
	}
	res := validate(t, ruled(b.String(), ""))
	if !has(res.Errors, "at most 40 per room") {
		t.Errorf("errors %v", res.Errors)
	}
}

func TestGateProblems(t *testing.T) {
	gateRules := "    rules:\n      - on: {verb: PULL, target: lever}\n        do: [{open: {gate: trap}}]\n"
	cellar := "  cellar:\n    name: Cellar\n    description: x\n    exits: {U: hall}\n"
	cases := []struct {
		name, hall, want string
		warn             bool
	}{
		{"bad dir", gateRules + "    gates:\n      trap: {exit: {dir: X, to: cellar}, mode: hidden}\n" + cellar, "not a direction", false},
		{"plain exit in the way", gateRules + "    gates:\n      trap: {exit: {dir: E, to: cellar}, mode: hidden}\n" + cellar, "already has an ordinary E exit", false},
		{"two gates one direction", gateRules + "    gates:\n      trap: {exit: {dir: D, to: cellar}, mode: hidden}\n      hatch: {exit: {dir: D, to: cellar}, mode: hidden}\n" + cellar, "already uses D", false},
		{"unknown room", gateRules + "    gates:\n      trap: {exit: {dir: D, to: pit}, mode: hidden}\n", `"pit" is not a room`, false},
		{"bad mode", gateRules + "    gates:\n      trap: {exit: {dir: D, to: cellar}, mode: open}\n" + cellar, "mode is hidden or locked", false},
		{"locked without message", gateRules + "    gates:\n      trap: {exit: {dir: D, to: cellar}, mode: locked}\n" + cellar, "needs a message", false},
		{"bad key", gateRules + "    gates:\n      Trap_1: {exit: {dir: D, to: cellar}, mode: hidden}\n" + cellar, "key must be lower case", false},
		{"nothing opens it", "    gates:\n      trap: {exit: {dir: D, to: cellar}, mode: hidden}\n" + cellar, "no rule opens this gate", false},
		{"open names no gate", "    rules:\n      - on: {verb: PULL, target: lever}\n        do: [{open: {gate: ghost}}]\n", `has no gate "ghost"`, false},
		{"open names no room", "    rules:\n      - on: {verb: PULL, target: lever}\n        do: [{open: {gate: g, room: moon}}]\n", `"moon" is not a room`, false},
		{"bad for", gateRules + "    gates:\n      trap: {exit: {dir: D, to: cellar}, mode: hidden}\n" + cellar, "", false}, // fine: for is absent
		{"one-way gate", gateRules + "    gates:\n      trap: {exit: {dir: D, to: cellar}, mode: hidden}\n  cellar:\n    name: Cellar\n    description: x\n    exits: {}\n", "one-way", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := validate(t, ruled(tc.hall, ""))
			list := res.Errors
			if tc.warn {
				list = res.Warnings
			}
			if tc.want != "" && !has(list, tc.want) {
				t.Errorf("want a problem containing %q; errors %v warnings %v", tc.want, res.Errors, res.Warnings)
			}
		})
	}
	// An invalid "for" on a gate effect.
	res := validate(t, ruled("    rules:\n      - on: {verb: PULL, target: lever}\n        do: [{open: {gate: trap, for: everyone}}]\n    gates:\n      trap: {exit: {dir: D, to: cellar}, mode: hidden}\n"+cellar, ""))
	if !has(res.Errors, `for is "room"`) {
		t.Errorf("errors %v", res.Errors)
	}
}

func TestAGatedRoomCountsAsReachable(t *testing.T) {
	res := validate(t, ruled(goodRules, ""))
	if has(res.Warnings, "cannot be reached") {
		t.Errorf("a room behind a gate should count as reachable: %v", res.Warnings)
	}
}

func TestRenderShowsRulesAndGates(t *testing.T) {
	out := render(t, ruled(goodRules, ""))
	for _, want := range []string{
		"**Gates:**", "- `trap`: D → Cellar (`cellar`), locked (\"Shut.\")",
		"**Rules:**", "1. `pull` when PULL lever: adds 1 to pulls (wraps 1 to 3); tells the player \"Click, {player}.\"; runs rule after",
		"2. `after` when (only when another rule runs it) and counters pulls&gt;=2 and unless flag done: sets flag done; gives key; opens gate trap",
		"-.->|D gate|",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("overview is missing %q\n%s", want, out)
		}
	}
}
