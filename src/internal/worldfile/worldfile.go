// Package worldfile reads and validates the world content files: rooms, items and
// NPCs written as YAML. It knows nothing about the game or the database, so the
// content repository's CI can run the validator on its own (mud_server
// -validate-world DIR). Applying content to the database is a separate step.
//
// Layout: every *.yaml and *.yml file under the directory is merged. Keys are
// global per kind (a room key is unique across all files), so authors can split
// an area per file but exits may point at any room.
package worldfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Directions are the exits a room can have (see MOVE).
var Directions = []string{"N", "S", "E", "W", "U", "D"}

var opposite = map[string]string{"N": "S", "S": "N", "E": "W", "W": "E", "U": "D", "D": "U"}

// Slots are the equipment slots an item can fill.
var Slots = []string{"weapon", "shield", "helmet", "chest", "legs", "hands", "ring", "neck"}

// Behaviors are the NPC turn functions the server implements.
var Behaviors = []string{"orc"}

// keyPattern is what a key may look like: lower case words joined by dashes.
var keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// MaxDescription is the longest room or item description the validator accepts without a warning.
const MaxDescription = 1500

// MaxFeatures is the most features one room may have, and MaxFeatureName the longest name or alias.
const (
	MaxFeatures    = 24
	MaxFeatureName = 40
	MaxPeople      = 8  // per room
	MaxTopics      = 12 // per person
)

// File is one content file as written by an author.
type File struct {
	StartRoom string            `yaml:"start_room"`
	Rooms     map[string]Room   `yaml:"rooms"`
	Items     map[string]Item   `yaml:"items"`
	NPCs      map[string]NPC    `yaml:"npcs"`
	Areas     map[string]Area   `yaml:"areas"`
	Puzzles   map[string]Puzzle `yaml:"puzzles"`
	Events    map[string]Event  `yaml:"events"`
	Codes     map[string]Code   `yaml:"codes"`
	// Progression tunes XP and levels; at most one file may set it.
	Progression *Progression `yaml:"progression"`

	// Onboarding turns on the guided first steps; at most one file may set it.
	Onboarding *Onboarding `yaml:"onboarding"`
}

// Area is a set of rooms that copy together when a crowd arrives (see crowd control):
// players are spread over copies of the area instead of piling into one room.
type Area struct {
	Rooms []string `yaml:"rooms"`
}

// Room is a place. Exits map a direction to another room's key.
type Room struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description"`
	Exits       map[string]string `yaml:"exits"`
	// Items lie on the floor when the room is first created: item key to quantity.
	Items    map[string]int `yaml:"items"`
	Spawners []Spawner      `yaml:"spawners"`
	// Features are scenery to EXAMINE: key to its names and its text.
	Features map[string]Feature `yaml:"features"`
	// People are friendly characters to TALK to and ASK: key to the person.
	People map[string]Person `yaml:"people"`
	// Ambient lines sent to the room while players are in it, and the notes LOOK adds by time of day (ambient.go).
	Ambient  []Ambient         `yaml:"ambient"`
	DayNotes map[string]string `yaml:"day_notes"`
	// Rules are what happens when a player acts on things here; Gates are exits that rules open.
	Rules []Rule          `yaml:"rules"`
	Gates map[string]Gate `yaml:"gates"`
	// Capacity is the most players the room holds, and QuietAt the audience at which talk is limited (capacity.go).
	Capacity int `yaml:"capacity"`
	QuietAt  int `yaml:"quiet_at"`
	// Region groups rooms for MAP ("Atrium", "Clockwork Wing"); rooms without one are "Elsewhere".
	Region string `yaml:"region"`
}

// Person is a friendly character: not a monster, never fights, never moves. Everything they
// say is written here. The first name is what they are called; the rest are aliases.
type Person struct {
	Names     []string         `yaml:"names"`
	Examine   string           `yaml:"examine"`    // what EXAMINE shows
	Greeting  string           `yaml:"greeting"`   // what TALK shows first
	Shrug     string           `yaml:"shrug"`      // the answer to a topic they do not know (optional)
	Idle      []string         `yaml:"idle"`       // lines they murmur to the room while players are there (optional)
	IdleEvery []int            `yaml:"idle_every"` // [least, most] seconds between them; default [120, 300]
	Topics    map[string]Topic `yaml:"topics"`
}

// Topic is something a person can be asked about. Keywords are what a player may type after
// ASK <person> ABOUT; the first is shown in the list TALK prints.
type Topic struct {
	Keywords []string `yaml:"keywords"`
	Say      string   `yaml:"say"`
}

// Feature is something in a room that cannot be picked up but can be looked at. The first
// name is what it is called ("first clock"); the others are aliases a player may type.
type Feature struct {
	Names    []string `yaml:"names"`
	Examine  string   `yaml:"examine"`
	Unlisted bool     `yaml:"unlisted"` // left out of the "You notice" line: examinable, but for the player to find
}

// Spawner keeps up to Count of an NPC in its room, adding one every TickMS.
type Spawner struct {
	NPC    string `yaml:"npc"`
	Count  int    `yaml:"count"`
	TickMS int    `yaml:"tick_ms"`
}

// Item is something a player can carry; with Equipment it can be worn.
type Item struct {
	Name        string     `yaml:"name"`
	Description string     `yaml:"description"`
	Persistent  bool       `yaml:"persistent"` // stays in the room after being picked up (a fixture)
	Bound       bool       `yaml:"bound"`      // a puzzle token: cannot be dropped or given away; some rule must give it
	Equipment   *Equipment `yaml:"equipment"`
}

// Equipment is what an item does while worn.
type Equipment struct {
	Slot    string `yaml:"slot"`
	HP      int    `yaml:"hp"`
	MaxHP   int    `yaml:"max_hp"`
	MP      int    `yaml:"mp"`
	MaxMP   int    `yaml:"max_mp"`
	Attack  int    `yaml:"attack"`
	Defense int    `yaml:"defense"`
}

// NPC is a template for a monster.
type NPC struct {
	Race         string `yaml:"race"`
	HP           int    `yaml:"hp"`
	MP           int    `yaml:"mp"`
	Attack       int    `yaml:"attack"`
	Accuracy     int    `yaml:"accuracy"`
	Defense      int    `yaml:"defense"`
	CombatTickMS int    `yaml:"combat_tick_ms"`
	Behavior     string `yaml:"behavior"`
	LootGoldMin  int    `yaml:"loot_gold_min"`
	LootGoldMax  int    `yaml:"loot_gold_max"`
}

// World is every file merged, with where each key came from.
type World struct {
	StartRoom   string
	Rooms       map[string]Room
	Items       map[string]Item
	NPCs        map[string]NPC
	Areas       map[string]Area
	Puzzles     map[string]Puzzle
	Events      map[string]Event
	Codes       map[string]Code
	Progression *Progression
	Onboarding  *Onboarding
	source      map[string]string // "room:lobby" -> file it was defined in
}

// Problem is one thing wrong with the content.
type Problem struct {
	File    string
	Path    string // for example "rooms.lobby.exits.N"
	Message string
}

func (p Problem) String() string {
	switch {
	case p.File != "" && p.Path != "":
		return fmt.Sprintf("%s: %s: %s", p.File, p.Path, p.Message)
	case p.File != "":
		return fmt.Sprintf("%s: %s", p.File, p.Message)
	case p.Path != "":
		return fmt.Sprintf("%s: %s", p.Path, p.Message)
	}
	return p.Message
}

// Result is what validation found. Errors stop a deploy; Warnings are worth reading.
type Result struct {
	Errors   []Problem
	Warnings []Problem
}

func (r *Result) errorf(file, path, format string, args ...any) {
	r.Errors = append(r.Errors, Problem{File: file, Path: path, Message: fmt.Sprintf(format, args...)})
}

func (r *Result) warnf(file, path, format string, args ...any) {
	r.Warnings = append(r.Warnings, Problem{File: file, Path: path, Message: fmt.Sprintf(format, args...)})
}

// Load reads every YAML file under dir and merges them. Problems found while
// reading (bad YAML, unknown fields, duplicate keys) are in the Result; the World
// is still returned so validation can report as much as possible in one run.
func Load(dir string) (*World, *Result, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != dir {
				return filepath.SkipDir
			}
			return nil
		}
		if ext := strings.ToLower(filepath.Ext(d.Name())); ext == ".yaml" || ext == ".yml" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("no .yaml or .yml files under %s", dir)
	}
	sort.Strings(files)

	w := &World{Rooms: map[string]Room{}, Items: map[string]Item{}, NPCs: map[string]NPC{}, Areas: map[string]Area{}, Puzzles: map[string]Puzzle{}, Events: map[string]Event{}, Codes: map[string]Code{}, source: map[string]string{}}
	res := &Result{}
	for _, path := range files {
		rel, _ := filepath.Rel(dir, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, err
		}
		var f File
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true) // a misspelled field is an error, not silently ignored
		if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
			res.errorf(rel, "", "%v", err)
			continue
		}
		w.merge(rel, &f, res)
	}
	return w, res, nil
}

func (w *World) merge(file string, f *File, res *Result) {
	if f.StartRoom != "" {
		if w.StartRoom != "" {
			res.errorf(file, "start_room", "start_room is already set (to %q in %s)", w.StartRoom, w.source["start_room"])
		} else {
			w.StartRoom = f.StartRoom
			w.source["start_room"] = file
		}
	}
	for k, v := range f.Rooms {
		if prev, dup := w.source["room:"+k]; dup {
			res.errorf(file, "rooms."+k, "duplicate room key (also in %s)", prev)
			continue
		}
		w.Rooms[k] = v
		w.source["room:"+k] = file
	}
	for k, v := range f.Items {
		if prev, dup := w.source["item:"+k]; dup {
			res.errorf(file, "items."+k, "duplicate item key (also in %s)", prev)
			continue
		}
		w.Items[k] = v
		w.source["item:"+k] = file
	}
	for k, v := range f.Areas {
		if prev, dup := w.source["area:"+k]; dup {
			res.errorf(file, "areas."+k, "duplicate area key (also in %s)", prev)
			continue
		}
		w.Areas[k] = v
		w.source["area:"+k] = file
	}
	for k, v := range f.Puzzles {
		if prev, dup := w.source["puzzle:"+k]; dup {
			res.errorf(file, "puzzles."+k, "duplicate puzzle key (also in %s)", prev)
			continue
		}
		w.Puzzles[k] = v
		w.source["puzzle:"+k] = file
	}
	for k, v := range f.Events {
		if prev, dup := w.source["event:"+k]; dup {
			res.errorf(file, "events."+k, "duplicate event key (also in %s)", prev)
			continue
		}
		w.Events[k] = v
		w.source["event:"+k] = file
	}
	if f.Progression != nil {
		if prev, dup := w.source["progression"]; dup {
			res.errorf(file, "progression", "progression is already set in %s", prev)
		} else {
			w.Progression = f.Progression
			w.source["progression"] = file
		}
	}
	if f.Onboarding != nil {
		if prev, dup := w.source["onboarding"]; dup {
			res.errorf(file, "onboarding", "onboarding is already set in %s", prev)
		} else {
			w.Onboarding = f.Onboarding
			w.source["onboarding"] = file
		}
	}
	for k, v := range f.Codes {
		if prev, dup := w.source["code:"+k]; dup {
			res.errorf(file, "codes."+k, "duplicate code key (also in %s)", prev)
			continue
		}
		w.Codes[k] = v
		w.source["code:"+k] = file
	}
	for k, v := range f.NPCs {
		if prev, dup := w.source["npc:"+k]; dup {
			res.errorf(file, "npcs."+k, "duplicate npc key (also in %s)", prev)
			continue
		}
		w.NPCs[k] = v
		w.source["npc:"+k] = file
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Validate checks the merged world and adds what it finds to res.
func (w *World) Validate(res *Result) {
	file := func(kind, key string) string { return w.source[kind+":"+key] }

	if len(w.Rooms) == 0 {
		res.errorf("", "rooms", "there are no rooms")
	}
	switch {
	case w.StartRoom == "":
		res.errorf("", "start_room", "no start_room: say which room new players begin in")
	default:
		if _, ok := w.Rooms[w.StartRoom]; !ok {
			res.errorf(w.source["start_room"], "start_room", "start_room %q is not a room", w.StartRoom)
		}
	}

	itemNames := map[string]string{} // upper-cased display name -> key
	for _, key := range sortedKeys(w.Items) {
		it := w.Items[key]
		f, p := file("item", key), "items."+key
		if !keyPattern.MatchString(key) {
			res.errorf(f, p, "key must be lower case letters, digits and dashes, starting with a letter or digit, at most 40 characters")
		}
		name := strings.TrimSpace(it.Name)
		switch {
		case name == "":
			res.errorf(f, p+".name", "an item needs a name")
		case name != strings.ToUpper(name):
			res.warnf(f, p+".name", "item names are shown and typed in capitals (%q); use %q", name, strings.ToUpper(name))
		}
		if up := strings.ToUpper(name); name != "" {
			if other, dup := itemNames[up]; dup {
				res.errorf(f, p+".name", "name %q is already used by item %q; players type names, so they must differ", name, other)
			} else {
				itemNames[up] = key
			}
		}
		if strings.TrimSpace(it.Description) == "" {
			res.errorf(f, p+".description", "an item needs a description")
		}
		if len(it.Description) > MaxDescription {
			res.warnf(f, p+".description", "description is %d characters; over %d reads badly on a phone", len(it.Description), MaxDescription)
		}
		if e := it.Equipment; e != nil {
			if !contains(Slots, e.Slot) {
				res.errorf(f, p+".equipment.slot", "slot %q is not one of %s", e.Slot, strings.Join(Slots, ", "))
			}
			for name, v := range map[string]int{"hp": e.HP, "max_hp": e.MaxHP, "mp": e.MP, "max_mp": e.MaxMP, "attack": e.Attack, "defense": e.Defense} {
				if v < 0 || v > 1000 {
					res.errorf(f, p+".equipment."+name, "%d is outside 0..1000", v)
				}
			}
		}
	}

	for _, key := range sortedKeys(w.NPCs) {
		n := w.NPCs[key]
		f, p := file("npc", key), "npcs."+key
		if !keyPattern.MatchString(key) {
			res.errorf(f, p, "key must be lower case letters, digits and dashes, starting with a letter or digit, at most 40 characters")
		}
		if strings.TrimSpace(n.Race) == "" {
			res.errorf(f, p+".race", "an npc needs a race (the name players see)")
		}
		if n.HP < 1 {
			res.errorf(f, p+".hp", "hp must be at least 1")
		}
		for name, v := range map[string]int{"mp": n.MP, "attack": n.Attack, "accuracy": n.Accuracy, "defense": n.Defense} {
			if v < 0 || v > 100000 {
				res.errorf(f, p+"."+name, "%d is outside 0..100000", v)
			}
		}
		if n.CombatTickMS < 250 {
			res.errorf(f, p+".combat_tick_ms", "combat_tick_ms must be at least 250 (the world ticks every 250 ms)")
		}
		if !contains(Behaviors, n.Behavior) {
			res.errorf(f, p+".behavior", "behavior %q is not one of %s", n.Behavior, strings.Join(Behaviors, ", "))
		}
		if n.LootGoldMin < 0 || n.LootGoldMax < n.LootGoldMin {
			res.errorf(f, p+".loot_gold_max", "loot_gold_max (%d) must not be below loot_gold_min (%d), and neither may be negative", n.LootGoldMax, n.LootGoldMin)
		}
	}

	for _, key := range sortedKeys(w.Rooms) {
		r := w.Rooms[key]
		f, p := file("room", key), "rooms."+key
		if !keyPattern.MatchString(key) {
			res.errorf(f, p, "key must be lower case letters, digits and dashes, starting with a letter or digit, at most 40 characters")
		}
		if strings.TrimSpace(r.Name) == "" {
			res.errorf(f, p+".name", "a room needs a name")
		}
		desc := strings.TrimSpace(r.Description)
		if desc == "" {
			res.errorf(f, p+".description", "a room needs a description")
		}
		if len(desc) > MaxDescription {
			res.warnf(f, p+".description", "description is %d characters; over %d reads badly on a phone", len(desc), MaxDescription)
		}
		for _, dir := range sortedKeys(r.Exits) {
			to := r.Exits[dir]
			ep := p + ".exits." + dir
			if !contains(Directions, dir) {
				res.errorf(f, ep, "%q is not a direction (use %s)", dir, strings.Join(Directions, ", "))
				continue
			}
			dest, ok := w.Rooms[to]
			if !ok {
				res.errorf(f, ep, "leads to %q, which is not a room", to)
				continue
			}
			if to == key {
				res.warnf(f, ep, "leads back to the same room")
				continue
			}
			if back, has := dest.Exits[opposite[dir]]; !has {
				gated := false
				for _, g := range dest.Gates { // a gate back counts: it opens when its puzzle is solved
					if g.Exit.Dir == opposite[dir] && g.Exit.To == key {
						gated = true
					}
				}
				if !gated {
					res.warnf(f, ep, "one-way: %q has no %s exit back to %q", to, opposite[dir], key)
				}
			} else if back != key {
				res.warnf(f, ep, "%q's %s exit goes to %q, not back to %q", to, opposite[dir], back, key)
			}
		}
		if len(r.Features) > MaxFeatures {
			res.errorf(f, p+".features", "%d features; at most %d per room", len(r.Features), MaxFeatures)
		}
		taken := map[string]string{} // upper-cased name or alias -> feature key
		floor := map[string]bool{}
		for ik := range r.Items {
			if it, ok := w.Items[ik]; ok {
				floor[strings.ToUpper(strings.TrimSpace(it.Name))] = true
			}
		}
		for _, fk := range sortedKeys(r.Features) {
			ft := r.Features[fk]
			fp := p + ".features." + fk
			if !keyPattern.MatchString(fk) {
				res.errorf(f, fp, "key must be lower case letters, digits and dashes, starting with a letter or digit, at most 40 characters")
			}
			if len(ft.Names) == 0 {
				res.errorf(f, fp+".names", "a feature needs at least one name (the first is what it is called, the rest are aliases)")
			}
			for i, n := range ft.Names {
				np := fmt.Sprintf("%s.names[%d]", fp, i)
				name := strings.ToUpper(strings.TrimSpace(n))
				switch {
				case name == "":
					res.errorf(f, np, "a name cannot be empty")
					continue
				case len(name) > MaxFeatureName:
					res.errorf(f, np, "%q is over %d characters", n, MaxFeatureName)
				}
				// Features may share an alias ("clock" for two clocks): EXAMINE then asks which. What they are
				// called (the first name) must differ, or the question could not be answered.
				if i == 0 {
					if other, dup := taken[name]; dup && other != fk {
						res.errorf(f, np, "%q is also what feature %q is called in this room; the first name of each feature must differ", n, other)
					}
					taken[name] = fk
				}
				if floor[name] {
					res.warnf(f, np, "%q is also the name of an item lying in this room; EXAMINE would be ambiguous", n)
				}
			}
			ex := strings.TrimSpace(ft.Examine)
			if ex == "" {
				res.errorf(f, fp+".examine", "a feature needs examine text")
			}
			if len(ex) > MaxDescription {
				res.warnf(f, fp+".examine", "text is %d characters; over %d reads badly on a phone", len(ex), MaxDescription)
			}
		}
		if len(r.People) > MaxPeople {
			res.errorf(f, p+".people", "%d people; at most %d per room", len(r.People), MaxPeople)
		}
		for _, pk := range sortedKeys(r.People) {
			pe := r.People[pk]
			pp := p + ".people." + pk
			if !keyPattern.MatchString(pk) {
				res.errorf(f, pp, "key must be lower case letters, digits and dashes, starting with a letter or digit, at most 40 characters")
			}
			if len(pe.Names) == 0 {
				res.errorf(f, pp+".names", "a person needs at least one name (the first is what they are called, the rest are aliases)")
			}
			for i, n := range pe.Names {
				np := fmt.Sprintf("%s.names[%d]", pp, i)
				name := strings.ToUpper(strings.TrimSpace(n))
				switch {
				case name == "":
					res.errorf(f, np, "a name cannot be empty")
					continue
				case len(name) > MaxFeatureName:
					res.errorf(f, np, "%q is over %d characters", n, MaxFeatureName)
				}
				if i == 0 { // what they are called shares EXAMINE's namespace with the features
					if other, dup := taken[name]; dup && other != pk {
						res.errorf(f, np, "%q is also what %q is called in this room; the first name of each thing must differ", n, other)
					}
					taken[name] = pk
				}
				if floor[name] {
					res.warnf(f, np, "%q is also the name of an item lying in this room; EXAMINE would be ambiguous", n)
				}
			}
			for _, field := range []struct{ name, text string }{{"examine", pe.Examine}, {"greeting", pe.Greeting}} {
				if strings.TrimSpace(field.text) == "" {
					res.errorf(f, pp+"."+field.name, "a person needs %s text", field.name)
				}
				if len(field.text) > MaxDescription {
					res.warnf(f, pp+"."+field.name, "text is %d characters; over %d reads badly on a phone", len(field.text), MaxDescription)
				}
			}
			if len(pe.Topics) > MaxTopics {
				res.errorf(f, pp+".topics", "%d topics; at most %d per person", len(pe.Topics), MaxTopics)
			}
			word := map[string]string{} // upper-cased keyword -> topic key
			for _, tk := range sortedKeys(pe.Topics) {
				tp := pp + ".topics." + tk
				t := pe.Topics[tk]
				if !keyPattern.MatchString(tk) {
					res.errorf(f, tp, "key must be lower case letters, digits and dashes, starting with a letter or digit, at most 40 characters")
				}
				if len(t.Keywords) == 0 {
					res.errorf(f, tp+".keywords", "a topic needs at least one keyword")
				}
				for i, kw := range t.Keywords {
					k := strings.ToUpper(strings.TrimSpace(kw))
					if k == "" || len(k) > MaxFeatureName {
						res.errorf(f, fmt.Sprintf("%s.keywords[%d]", tp, i), "a keyword must be 1 to %d characters", MaxFeatureName)
						continue
					}
					if other, dup := word[k]; dup && other != tk {
						res.errorf(f, fmt.Sprintf("%s.keywords[%d]", tp, i), "%q is also a keyword of topic %q; ASK could not tell them apart", kw, other)
					}
					word[k] = tk
				}
				if strings.TrimSpace(t.Say) == "" {
					res.errorf(f, tp+".say", "a topic needs something to say")
				}
				if len(t.Say) > MaxDescription {
					res.warnf(f, tp+".say", "text is %d characters; over %d reads badly on a phone", len(t.Say), MaxDescription)
				}
			}
		}
		for _, ik := range sortedKeys(r.Items) {
			if _, ok := w.Items[ik]; !ok {
				res.errorf(f, p+".items."+ik, "%q is not an item", ik)
			}
			if q := r.Items[ik]; q < 1 || q > 10000 {
				res.errorf(f, p+".items."+ik, "quantity %d is outside 1..10000", q)
			}
		}
		for i, s := range r.Spawners {
			sp := fmt.Sprintf("%s.spawners[%d]", p, i)
			if _, ok := w.NPCs[s.NPC]; !ok {
				res.errorf(f, sp+".npc", "%q is not an npc", s.NPC)
			}
			if s.Count < 1 || s.Count > 1000 {
				res.errorf(f, sp+".count", "count %d is outside 1..1000", s.Count)
			}
			if s.TickMS < 250 {
				res.errorf(f, sp+".tick_ms", "tick_ms must be at least 250")
			}
		}
	}

	inArea := map[string]string{}
	for _, name := range sortedKeys(w.Areas) {
		f, p := file("area", name), "areas."+name
		if !keyPattern.MatchString(name) {
			res.errorf(f, p, "key must be lower case letters, digits and dashes, starting with a letter or digit, at most 40 characters")
		}
		if len(w.Areas[name].Rooms) == 0 {
			res.errorf(f, p+".rooms", "an area needs at least one room")
		}
		for i, rk := range w.Areas[name].Rooms {
			rp := fmt.Sprintf("%s.rooms[%d]", p, i)
			if _, ok := w.Rooms[rk]; !ok {
				res.errorf(f, rp, "%q is not a room", rk)
			} else if other, dup := inArea[rk]; dup {
				res.errorf(f, rp, "room %q is already in area %q; a room can be in only one", rk, other)
			} else {
				inArea[rk] = name
			}
		}
	}

	w.validateRules(res)
	w.validateCodes(res)
	w.validateProgression(res)
	w.validateAmbient(res)
	w.validateOnboarding(res)
	w.validateCapacity(res)

	// Rooms a player can never reach from the start are almost always a mistake.
	if _, ok := w.Rooms[w.StartRoom]; ok {
		seen := map[string]bool{w.StartRoom: true}
		queue := []string{w.StartRoom}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, to := range w.Rooms[cur].Exits {
				if _, ok := w.Rooms[to]; ok && !seen[to] {
					seen[to] = true
					queue = append(queue, to)
				}
			}
			for _, g := range w.Rooms[cur].Gates { // reachable once the gates are open
				if _, ok := w.Rooms[g.Exit.To]; ok && !seen[g.Exit.To] {
					seen[g.Exit.To] = true
					queue = append(queue, g.Exit.To)
				}
			}
		}
		for _, key := range sortedKeys(w.Rooms) {
			if !seen[key] {
				res.warnf(file("room", key), "rooms."+key, "cannot be reached from the start room %q", w.StartRoom)
			}
		}
	}
}

// ValidateDir loads and validates a content directory.
func ValidateDir(dir string) (*World, *Result, error) {
	w, res, err := Load(dir)
	if err != nil {
		return nil, nil, err
	}
	w.Validate(res)
	sortProblems(res.Errors)
	sortProblems(res.Warnings)
	return w, res, nil
}

func sortProblems(ps []Problem) {
	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].File != ps[j].File {
			return ps[i].File < ps[j].File
		}
		return ps[i].Path < ps[j].Path
	})
}

// Run is the -validate-world command: it prints the problems and returns the
// process exit code (0 clean, 1 errors, or warnings under -strict, 2 usage).
func Run(args []string, stdout, stderr io.Writer) int {
	strict := false
	var dir string
	for _, a := range args {
		switch {
		case a == "-strict" || a == "--strict":
			strict = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "unknown option %s\nusage: mud_server -validate-world [-strict] DIR\n", a)
			return 2
		case dir == "":
			dir = a
		default:
			fmt.Fprintln(stderr, "usage: mud_server -validate-world [-strict] DIR")
			return 2
		}
	}
	if dir == "" {
		fmt.Fprintln(stderr, "usage: mud_server -validate-world [-strict] DIR")
		return 2
	}
	w, res, err := ValidateDir(dir)
	if err != nil {
		fmt.Fprintln(stderr, "validate-world:", err)
		return 2
	}
	for _, p := range res.Errors {
		fmt.Fprintln(stdout, "error:  ", p)
	}
	for _, p := range res.Warnings {
		fmt.Fprintln(stdout, "warning:", p)
	}
	fmt.Fprintf(stdout, "%d rooms, %d items, %d npcs; %d errors, %d warnings\n", len(w.Rooms), len(w.Items), len(w.NPCs), len(res.Errors), len(res.Warnings))
	if len(res.Errors) > 0 || (strict && len(res.Warnings) > 0) {
		return 1
	}
	return 0
}
