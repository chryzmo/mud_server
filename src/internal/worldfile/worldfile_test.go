package worldfile

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write makes a content directory from file name to text.
func write(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func validate(t *testing.T, files map[string]string) *Result {
	t.Helper()
	_, res, err := ValidateDir(write(t, files))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// has reports whether any problem's text contains want.
func has(ps []Problem, want string) bool {
	for _, p := range ps {
		if strings.Contains(p.String(), want) {
			return true
		}
	}
	return false
}

const twoRooms = `
start_room: hall
rooms:
  hall:
    name: Hall
    description: A hall.
    exits: {N: yard}
  yard:
    name: Yard
    description: A yard.
    exits: {S: hall}
`

func TestTheSampleWorldIsClean(t *testing.T) {
	w, res, err := ValidateDir(filepath.Join("..", "..", "..", "world-sample"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 || len(res.Warnings) != 0 {
		t.Fatalf("errors %v warnings %v", res.Errors, res.Warnings)
	}
	if len(w.Rooms) != 3 || len(w.Items) != 10 || len(w.NPCs) != 1 || w.StartRoom != "start-room" {
		t.Errorf("rooms %d items %d npcs %d start %q", len(w.Rooms), len(w.Items), len(w.NPCs), w.StartRoom)
	}
}

func TestAMinimalWorldIsValid(t *testing.T) {
	res := validate(t, map[string]string{"w.yaml": twoRooms})
	if len(res.Errors) != 0 || len(res.Warnings) != 0 {
		t.Fatalf("errors %v warnings %v", res.Errors, res.Warnings)
	}
}

func TestFilesMergeAcrossDirectories(t *testing.T) {
	res := validate(t, map[string]string{
		"world.yaml":      "start_room: hall\nrooms:\n  hall:\n    name: Hall\n    description: A hall.\n    exits: {N: yard}\n",
		"areas/yard.yml":  "rooms:\n  yard:\n    name: Yard\n    description: A yard.\n    exits: {S: hall}\n",
		".hidden/zzz.yml": "this is not even yaml: [",
		"notes.txt":       "ignored",
	})
	if len(res.Errors) != 0 {
		t.Fatalf("errors %v", res.Errors)
	}
}

func TestProblemsAreReported(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
		warn  bool // a warning, not an error
	}{
		{"unknown field", map[string]string{"w.yaml": strings.Replace(twoRooms, "description: A yard.", "description: A yard.\n    bogus: 1", 1)}, "bogus", false},
		{"bad yaml", map[string]string{"w.yaml": "rooms: [\n"}, "w.yaml", false},
		{"no start room", map[string]string{"w.yaml": strings.Replace(twoRooms, "start_room: hall\n", "", 1)}, "no start_room", false},
		{"start room missing", map[string]string{"w.yaml": strings.Replace(twoRooms, "start_room: hall", "start_room: nowhere", 1)}, `"nowhere" is not a room`, false},
		{"two start rooms", map[string]string{"a.yaml": twoRooms, "b.yaml": "start_room: yard\n"}, "already set", false},
		{"duplicate room key", map[string]string{"a.yaml": twoRooms, "b.yaml": "rooms:\n  hall:\n    name: Again\n    description: x\n"}, "duplicate room key", false},
		{"bad key", map[string]string{"w.yaml": strings.ReplaceAll(twoRooms, "yard", "The_Yard")}, "key must be lower case", false},
		{"exit to nowhere", map[string]string{"w.yaml": strings.Replace(twoRooms, "{N: yard}", "{N: pit}", 1)}, `"pit", which is not a room`, false},
		{"bad direction", map[string]string{"w.yaml": strings.Replace(twoRooms, "{N: yard}", "{X: yard}", 1)}, "not a direction", false},
		{"missing description", map[string]string{"w.yaml": strings.Replace(twoRooms, "description: A yard.", "description: ''", 1)}, "needs a description", false},
		{"item on the floor missing", map[string]string{"w.yaml": strings.Replace(twoRooms, "    exits: {N: yard}\n", "    exits: {N: yard}\n    items: {gem: 1}\n", 1)}, `"gem" is not an item`, false},
		{"spawner npc missing", map[string]string{"w.yaml": strings.Replace(twoRooms, "    exits: {N: yard}\n", "    exits: {N: yard}\n    spawners: [{npc: ghost, count: 1, tick_ms: 5000}]\n", 1)}, `"ghost" is not an npc`, false},
		{"one way exit", map[string]string{"w.yaml": strings.Replace(twoRooms, "exits: {S: hall}", "exits: {}", 1)}, "one-way", true},
		{"exit back elsewhere", map[string]string{"w.yaml": twoRooms + "  cellar:\n    name: Cellar\n    description: A cellar.\n    exits: {S: hall}\n"}, "not back to \"cellar\"", true},
		{"unreachable room", map[string]string{"w.yaml": twoRooms + "  attic:\n    name: Attic\n    description: An attic.\n"}, "cannot be reached", true},
		{"lower case item name", map[string]string{"w.yaml": twoRooms + "items:\n  key:\n    name: Key\n    description: A key.\n"}, "in capitals", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := validate(t, tc.files)
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

func TestItemsAndNPCsAreChecked(t *testing.T) {
	res := validate(t, map[string]string{"w.yaml": twoRooms + `
items:
  sword:
    name: SWORD
    description: Sharp.
    equipment: {slot: elbow, attack: -1}
  sword2:
    name: sword
    description: Also sharp.
  nameless:
    description: No name.
npcs:
  rat:
    race: RAT
    hp: 0
    combat_tick_ms: 100
    behavior: dance
    loot_gold_min: 5
    loot_gold_max: 1
`})
	for _, want := range []string{
		`slot "elbow"`, "-1 is outside", `already used by item "sword"`, "an item needs a name",
		"hp must be at least 1", "combat_tick_ms must be at least 250", `behavior "dance"`, "loot_gold_max (1) must not be below",
	} {
		if !has(res.Errors, want) {
			t.Errorf("missing error containing %q in %v", want, res.Errors)
		}
	}
}

func TestAreasAreChecked(t *testing.T) {
	res := validate(t, map[string]string{"w.yaml": twoRooms + `
areas:
  start: {rooms: [hall, yard]}
  empty: {rooms: []}
  clash: {rooms: [yard, nowhere]}
`})
	for _, want := range []string{`area needs at least one room`, `"nowhere" is not a room`, `room "yard" is already in area "clash"`} {
		if !has(res.Errors, want) {
			t.Errorf("missing error containing %q in %v", want, res.Errors)
		}
	}
	ok := validate(t, map[string]string{"w.yaml": twoRooms + "areas:\n  start: {rooms: [hall, yard]}\n"})
	if len(ok.Errors) != 0 {
		t.Errorf("a good area is an error: %v", ok.Errors)
	}
}

func TestSpawnerSettingsAreChecked(t *testing.T) {
	res := validate(t, map[string]string{"w.yaml": strings.Replace(twoRooms, "    exits: {N: yard}\n", `    exits: {N: yard}
    spawners: [{npc: orc, count: 0, tick_ms: 100}]
`, 1) + "npcs:\n  orc: {race: ORC, hp: 5, combat_tick_ms: 5000, behavior: orc}\n"})
	if !has(res.Errors, "count 0 is outside") || !has(res.Errors, "tick_ms must be at least 250") {
		t.Errorf("errors %v", res.Errors)
	}
}

func TestRunExitCodesAndOutput(t *testing.T) {
	good := write(t, map[string]string{"w.yaml": twoRooms})
	oneWay := write(t, map[string]string{"w.yaml": strings.Replace(twoRooms, "exits: {S: hall}", "exits: {}", 1)})
	broken := write(t, map[string]string{"w.yaml": strings.Replace(twoRooms, "{N: yard}", "{N: pit}", 1)})

	run := func(args ...string) (int, string) {
		var out, errOut bytes.Buffer
		code := Run(args, &out, &errOut)
		return code, out.String() + errOut.String()
	}
	if code, out := run(good); code != 0 || !strings.Contains(out, "2 rooms") {
		t.Errorf("good: %d %q", code, out)
	}
	if code, out := run(oneWay); code != 0 || !strings.Contains(out, "warning:") {
		t.Errorf("warnings are not failures: %d %q", code, out)
	}
	if code, _ := run("-strict", oneWay); code != 1 {
		t.Errorf("-strict makes warnings fail: %d", code)
	}
	if code, out := run(broken); code != 1 || !strings.Contains(out, "error:") {
		t.Errorf("broken: %d %q", code, out)
	}
	if code, _ := run(); code != 2 {
		t.Errorf("no directory: %d", code)
	}
	if code, _ := run(filepath.Join(good, "missing")); code != 2 {
		t.Errorf("missing directory: %d", code)
	}
	empty := t.TempDir()
	if code, out := run(empty); code != 2 || !strings.Contains(out, "no .yaml") {
		t.Errorf("empty directory: %d %q", code, out)
	}
}

func TestFeaturesAreChecked(t *testing.T) {
	good := validate(t, map[string]string{"w.yaml": strings.Replace(twoRooms, "    exits: {N: yard}\n", `    exits: {N: yard}
    features:
      clock:
        names: [first clock, clock 1]
        examine: A tall clock.
      clock-two:
        names: [second clock, clock 2]
        examine: Another clock.
        unlisted: true
`, 1)})
	if len(good.Errors) != 0 || len(good.Warnings) != 0 {
		t.Fatalf("good features: errors %v warnings %v", good.Errors, good.Warnings)
	}

	res := validate(t, map[string]string{"w.yaml": strings.Replace(twoRooms, "    exits: {N: yard}\n", `    exits: {N: yard}
    items: {key: 1}
    features:
      Bad_Key:
        names: [odd]
        examine: x
      nameless:
        examine: x
      blank:
        names: ["  "]
        examine: x
      long:
        names: ["`+strings.Repeat("a", 41)+`"]
        examine: x
      one:
        names: [lever, KEY]
        examine: x
      two:
        names: [Lever]
        examine: x
      silent:
        names: [mute]
`, 1) + "items:\n  key: {name: KEY, description: A key.}\n"})
	for _, want := range []string{
		"key must be lower case", "needs at least one name", "a name cannot be empty", "over 40 characters",
		`also what feature "one" is called in this room`, "needs examine text",
	} {
		if !has(res.Errors, want) {
			t.Errorf("missing error containing %q in %v", want, res.Errors)
		}
	}
	// Two clocks may share the alias "clock"; EXAMINE asks which.
	shared := validate(t, map[string]string{"w.yaml": strings.Replace(twoRooms, "    exits: {N: yard}\n", `    exits: {N: yard}
    features:
      a: {names: [first clock, clock], examine: x}
      b: {names: [second clock, clock], examine: x}
`, 1)})
	if len(shared.Errors) != 0 {
		t.Errorf("a shared alias is an error: %v", shared.Errors)
	}
	if !has(res.Warnings, `also the name of an item lying in this room`) {
		t.Errorf("a feature named like a floor item should warn: %v", res.Warnings)
	}

	var many strings.Builder
	many.WriteString("    features:\n")
	for i := 0; i <= MaxFeatures; i++ {
		fmt.Fprintf(&many, "      f%d: {names: [thing %d], examine: x}\n", i, i)
	}
	tooMany := validate(t, map[string]string{"w.yaml": strings.Replace(twoRooms, "    exits: {N: yard}\n", "    exits: {N: yard}\n"+many.String(), 1)})
	if !has(tooMany.Errors, "at most 24 per room") {
		t.Errorf("too many features: %v", tooMany.Errors)
	}
}

const porterPeople = `    people:
      porter:
        names: [Porter Bellweather, porter, bellweather]
        examine: A very tall porter.
        greeting: "Mind your head."
        shrug: "He shrugs."
        topics:
          ticket: {keywords: [ticket, claim], say: "Hook 42 is empty."}
          map: {keywords: [map], say: "Take this."}
`

func TestPeopleAreChecked(t *testing.T) {
	good := validate(t, map[string]string{"w.yaml": strings.Replace(twoRooms, "    exits: {N: yard}\n", "    exits: {N: yard}\n"+porterPeople, 1)})
	if len(good.Errors) != 0 || len(good.Warnings) != 0 {
		t.Fatalf("good people: errors %v warnings %v", good.Errors, good.Warnings)
	}

	res := validate(t, map[string]string{"w.yaml": strings.Replace(twoRooms, "    exits: {N: yard}\n", `    exits: {N: yard}
    features:
      statue: {names: [Marble Statue], examine: x}
    people:
      Bad_Key: {names: [odd], examine: x, greeting: x}
      nameless: {examine: x, greeting: x}
      blankname: {names: ["  "], examine: x, greeting: x}
      mute: {names: [Mute One], greeting: x}
      silent: {names: [Silent One], examine: x}
      twin: {names: [marble statue], examine: x, greeting: x}
      chatty:
        names: [Chatty]
        examine: x
        greeting: x
        topics:
          Bad_Topic: {keywords: [one], say: x}
          wordless: {say: x}
          blank: {keywords: ["  "], say: x}
          mutetopic: {keywords: [quiet]}
          first: {keywords: [shared], say: x}
          second: {keywords: [SHARED], say: x}
`, 1)})
	for _, want := range []string{
		"key must be lower case", "needs at least one name", "a name cannot be empty", "needs examine text", "needs greeting text",
		`also what "statue" is called in this room`, "needs at least one keyword", "a keyword must be 1 to 40",
		"needs something to say", `also a keyword of topic "first"`,
	} {
		if !has(res.Errors, want) {
			t.Errorf("missing error containing %q in %v", want, res.Errors)
		}
	}

	var many strings.Builder
	many.WriteString("    people:\n")
	for i := 0; i <= MaxPeople; i++ {
		fmt.Fprintf(&many, "      p%d: {names: [person %d], examine: x, greeting: x}\n", i, i)
	}
	tooMany := validate(t, map[string]string{"w.yaml": strings.Replace(twoRooms, "    exits: {N: yard}\n", "    exits: {N: yard}\n"+many.String(), 1)})
	if !has(tooMany.Errors, "at most 8 per room") {
		t.Errorf("too many people: %v", tooMany.Errors)
	}
	var topics strings.Builder
	topics.WriteString("    people:\n      chatty:\n        names: [Chatty]\n        examine: x\n        greeting: x\n        topics:\n")
	for i := 0; i <= MaxTopics; i++ {
		fmt.Fprintf(&topics, "          t%d: {keywords: [word%d], say: x}\n", i, i)
	}
	tooManyTopics := validate(t, map[string]string{"w.yaml": strings.Replace(twoRooms, "    exits: {N: yard}\n", "    exits: {N: yard}\n"+topics.String(), 1)})
	if !has(tooManyTopics.Errors, "at most 12 per person") {
		t.Errorf("too many topics: %v", tooManyTopics.Errors)
	}
}

func TestGearSlotsAreFiveAndTheOldNamesWarn(t *testing.T) {
	items := func(equipment string) map[string]string {
		return map[string]string{"w.yaml": twoRooms + "\nitems:\n  thing:\n    name: THING\n    description: A thing.\n    equipment: " + equipment + "\n"}
	}
	for _, slot := range []string{"weapon", "armor", "shield", "boots"} {
		if res := validate(t, items("{slot: "+slot+", defense: 1}")); len(res.Errors) != 0 || len(res.Warnings) != 0 {
			t.Errorf("slot %s: errors %v warnings %v", slot, res.Errors, res.Warnings)
		}
	}
	if res := validate(t, items("{slot: cosmetic}")); len(res.Errors) != 0 || len(res.Warnings) != 0 {
		t.Errorf("a plain cosmetic: errors %v warnings %v", res.Errors, res.Warnings)
	}
	if res := validate(t, items("{slot: cosmetic, defense: 1}")); !has(res.Errors, "a cosmetic is for looks only") {
		t.Errorf("a cosmetic with a bonus is accepted: %v", res.Errors)
	}
	for _, old := range []string{"helmet", "chest", "legs", "hands", "ring", "neck"} {
		res := validate(t, items("{slot: "+old+", defense: 1}"))
		if len(res.Errors) != 0 || !has(res.Warnings, `slot "`+old+`" is retired`) {
			t.Errorf("old slot %s: errors %v warnings %v", old, res.Errors, res.Warnings)
		}
	}
}

func TestDropTablesAreChecked(t *testing.T) {
	npc := func(drops string) map[string]string {
		return map[string]string{"w.yaml": twoRooms + "\nitems:\n  charm:\n    name: CHARM\n    description: A charm.\nnpcs:\n  rat:\n    race: RAT\n    hp: 5\n    attack: 1\n    accuracy: 50\n    combat_tick_ms: 500\n    behavior: orc\n    drops: " + drops + "\n"}
	}
	good := validate(t, npc("[{item: charm, chance: 50}, {badge: flame, chance: 100}, {color: gold, chance: 1}]"))
	if len(good.Errors) != 0 {
		t.Errorf("a good table is an error: %v", good.Errors)
	}
	for drops, want := range map[string]string{
		"[{item: nothing, chance: 5}]":             `"nothing" is not an item`,
		"[{badge: skull, chance: 5}]":              `"skull" is not one of`,
		"[{color: plaid, chance: 5}]":              `"plaid" is not one of`,
		"[{item: charm, badge: flame, chance: 5}]": "exactly one of item, color or badge",
		"[{chance: 5}]":                            "exactly one of item, color or badge",
		"[{item: charm, chance: 0}]":               "chance must be from 1 to 100",
		"[{item: charm, chance: 101}]":             "chance must be from 1 to 100",
		"[{item: charm, chance: 5},{item: charm, chance: 5},{item: charm, chance: 5},{item: charm, chance: 5},{item: charm, chance: 5},{item: charm, chance: 5},{item: charm, chance: 5},{item: charm, chance: 5},{item: charm, chance: 5}]": "at most 8",
	} {
		if res := validate(t, npc(drops)); !has(res.Errors, want) {
			t.Errorf("drops %s: want an error containing %q, got %v", drops, want, res.Errors)
		}
	}
}
