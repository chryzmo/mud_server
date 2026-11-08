package worldfile

import (
	"fmt"
	"regexp"
	"strings"
)

/*
Rules and gates (plans/interactive-world.md sections 3 and 5): the declarative core of the
puzzles. A rule says "when this happens, if these things are true, do these things"; a gate is
an exit that is not always open. There is no scripting: every condition and effect is one of a
fixed set, and the validator checks every reference before a deploy.
*/

// RuleVerbs are the verbs a rule can listen for. SAY is a trigger of its own (the words said).
var RuleVerbs = []string{"TURN", "PULL", "PUSH", "PRESS", "RING", "LIGHT", "OPEN", "SEARCH", "READ", "USE", "GIVE", "PUT", "THROW", "SET"}

// Limits that keep a room's rules small and a puzzle's state bounded.
const (
	MaxRules          = 40 // per room
	MaxEffects        = 12 // per rule
	MaxRunDepth       = 4  // a rule may run another, to this depth
	MaxHoldSeconds    = 3600
	MaxSequenceLength = 12
)

// Rule: when On happens in this room, if every If holds and no Unless does, do each of Do in
// order. The first matching rule in a room is the one that runs. A rule with no On is only run
// by another rule (`run: <id>`) and must have an ID.
type Rule struct {
	ID     string      `yaml:"id" json:"id,omitempty"`
	On     *Trigger    `yaml:"on" json:"on,omitempty"`
	If     []Condition `yaml:"if" json:"if,omitempty"`
	Unless []Condition `yaml:"unless" json:"unless,omitempty"`
	Do     []Effect    `yaml:"do" json:"do"`
}

// Trigger is one of: a verb used on a target (with another thing, or a value), the words said,
// or arriving in the room. Targets are feature, person or item keys; With is an item key.
type Trigger struct {
	Verb   string `yaml:"verb" json:"verb,omitempty"`
	Target string `yaml:"target" json:"target,omitempty"`
	With   string `yaml:"with" json:"with,omitempty"`
	Value  string `yaml:"value" json:"value,omitempty"` // SET <target> TO <value>
	Say    string `yaml:"say" json:"say,omitempty"`
	Enter  bool   `yaml:"enter" json:"enter,omitempty"`
}

// Condition has exactly one field set.
type Condition struct {
	Flag                 string              `yaml:"flag" json:"flag,omitempty"`
	NotFlag              string              `yaml:"not_flag" json:"not_flag,omitempty"`
	HasItem              string              `yaml:"has_item" json:"has_item,omitempty"`
	KnowsSpell           string              `yaml:"knows_spell" json:"knows_spell,omitempty"` // the player has learned this spell (a key)
	RoomFlag             string              `yaml:"room_flag" json:"room_flag,omitempty"`   // a held key in this room (see hold)
	WorldFlag            string              `yaml:"world_flag" json:"world_flag,omitempty"` // set for everyone by an event or the GM
	Counter              map[string]int      `yaml:"counter" json:"counter,omitempty"`       // every named counter equals the value
	CounterAtLeast       map[string]int      `yaml:"counter_at_least" json:"counter_at_least,omitempty"`
	Sequence             map[string][]string `yaml:"sequence" json:"sequence,omitempty"` // the recorded sequence equals the list
	SequenceLongerThan   map[string]int      `yaml:"sequence_longer_than" json:"sequence_longer_than,omitempty"`
	PlayersInRoomAtLeast *int                `yaml:"players_in_room_at_least" json:"players_in_room_at_least,omitempty"`
}

// Effect has exactly one field set.
type Effect struct {
	Say       *string       `yaml:"say" json:"say,omitempty"`           // to the player
	RoomSay   *string       `yaml:"room_say" json:"room_say,omitempty"` // to everyone in the room, {player} is the actor
	SetFlag   *string       `yaml:"set_flag" json:"set_flag,omitempty"`
	ClearFlag *string       `yaml:"clear_flag" json:"clear_flag,omitempty"`
	Add       *AddEffect    `yaml:"add" json:"add,omitempty"`
	Set       *SetEffect    `yaml:"set" json:"set,omitempty"`
	Append    *AppendEffect `yaml:"append" json:"append,omitempty"`
	Reset     *ResetEffect  `yaml:"reset" json:"reset,omitempty"`
	GiveItem  *string       `yaml:"give_item" json:"give_item,omitempty"`
	TakeItem  *string       `yaml:"take_item" json:"take_item,omitempty"`
	Open      *GateEffect   `yaml:"open" json:"open,omitempty"`
	Close     *GateEffect   `yaml:"close" json:"close,omitempty"`
	Hold      *HoldEffect   `yaml:"hold" json:"hold,omitempty"`
	Teleport  *string       `yaml:"teleport" json:"teleport,omitempty"` // a room key
	Stop      *bool         `yaml:"stop" json:"stop,omitempty"`         // end the rule here
	Run       *string       `yaml:"run" json:"run,omitempty"`           // another rule of this room, by id
	Unlock    *UnlockEffect `yaml:"unlock" json:"unlock,omitempty"`     // a quest reward: a name colour or badge
	Teach     *string       `yaml:"teach" json:"teach,omitempty"`       // a quest reward: a spell (a key)
}

// UnlockEffect grants a cosmetic from the fixed palette (Colors, Badges); exactly one is set.
type UnlockEffect struct {
	Color string `yaml:"color" json:"color,omitempty"`
	Badge string `yaml:"badge" json:"badge,omitempty"`
}

// AddEffect changes a counter by By; with Wrap [min, max] it stays in that range and wraps around.
type AddEffect struct {
	Counter string `yaml:"counter" json:"counter"`
	By      int    `yaml:"by" json:"by"`
	Wrap    []int  `yaml:"wrap" json:"wrap,omitempty"`
}

// SetEffect sets a counter.
type SetEffect struct {
	Counter string `yaml:"counter" json:"counter"`
	Value   int    `yaml:"value" json:"value"`
}

// AppendEffect adds a value to the end of a named sequence the player is building.
type AppendEffect struct {
	Sequence string `yaml:"sequence" json:"sequence"`
	Value    string `yaml:"value" json:"value"`
}

// ResetEffect empties a sequence.
type ResetEffect struct {
	Sequence string `yaml:"sequence" json:"sequence"`
}

// GateEffect opens or closes a gate for the player (For "room": for everyone in the room now).
// Room names the room the gate is in; empty means this room.
type GateEffect struct {
	Gate string `yaml:"gate" json:"gate"`
	Room string `yaml:"room" json:"room,omitempty"`
	For  string `yaml:"for" json:"for,omitempty"`
}

// HoldEffect marks a key as held in this room (this copy of it) for Seconds: a pressure plate.
type HoldEffect struct {
	Key     string `yaml:"key" json:"key"`
	Seconds int    `yaml:"seconds" json:"seconds"`
}

// Gate is an exit that is not always there. It is opened for a player by a rule. Hidden gates are
// invisible until opened; locked ones are listed as sealed and refuse with Message.
type Gate struct {
	Exit    GateExit `yaml:"exit" json:"exit"`
	Mode    string   `yaml:"mode" json:"mode"`
	Message string   `yaml:"message" json:"message,omitempty"`
	// OpenWhen is a world flag (set by an event) that opens this gate for everyone while it is set.
	OpenWhen string `yaml:"open_when" json:"open_when,omitempty"`
}

// GateExit is where a gate leads: a direction and a room key.
type GateExit struct {
	Dir string `yaml:"dir" json:"dir"`
	To  string `yaml:"to" json:"to"`
}

var placeholder = regexp.MustCompile(`\{([^{}]*)\}`)

// Placeholders returns the names inside {braces} in text.
func Placeholders(text string) []string {
	var out []string
	for _, m := range placeholder.FindAllStringSubmatch(text, -1) {
		out = append(out, m[1])
	}
	return out
}

func set1(n int, what string) (string, bool) {
	if n == 1 {
		return "", true
	}
	if n == 0 {
		return "has nothing set", false
	}
	return fmt.Sprintf("has %d things set; use one %s per entry", n, what), false
}

func (c Condition) kind() (name string, n int) {
	add := func(k string, set bool) {
		if set {
			name = k
			n++
		}
	}
	add("flag", c.Flag != "")
	add("not_flag", c.NotFlag != "")
	add("has_item", c.HasItem != "")
	add("knows_spell", c.KnowsSpell != "")
	add("room_flag", c.RoomFlag != "")
	add("world_flag", c.WorldFlag != "")
	add("counter", len(c.Counter) > 0)
	add("counter_at_least", len(c.CounterAtLeast) > 0)
	add("sequence", len(c.Sequence) > 0)
	add("sequence_longer_than", len(c.SequenceLongerThan) > 0)
	add("players_in_room_at_least", c.PlayersInRoomAtLeast != nil)
	return
}

func (e Effect) kind() (name string, n int) {
	add := func(k string, set bool) {
		if set {
			name = k
			n++
		}
	}
	add("say", e.Say != nil)
	add("room_say", e.RoomSay != nil)
	add("set_flag", e.SetFlag != nil)
	add("clear_flag", e.ClearFlag != nil)
	add("add", e.Add != nil)
	add("set", e.Set != nil)
	add("append", e.Append != nil)
	add("reset", e.Reset != nil)
	add("give_item", e.GiveItem != nil)
	add("take_item", e.TakeItem != nil)
	add("open", e.Open != nil)
	add("close", e.Close != nil)
	add("hold", e.Hold != nil)
	add("teleport", e.Teleport != nil)
	add("stop", e.Stop != nil)
	add("run", e.Run != nil)
	add("unlock", e.Unlock != nil)
	add("teach", e.Teach != nil)
	return
}

// ruleFacts is what the validator gathers from every rule in the world, for the checks that need
// to see all of it at once (a flag read here must be set somewhere).
type ruleFacts struct {
	flagsSet, flagsCleared map[string]bool
	flagsRead, notRead     map[string]string // flag -> a path that reads it
	worldRead              map[string]string // world flag -> a path that reads it
	counters               map[string]bool   // counters that are added to or set
	opens                  map[string]bool   // "room/gate" that some rule opens
	gives                  map[string]bool   // item keys some rule gives
	ran                    map[string]bool   // "room/rule-id" that some rule runs
}

func contains2(list []string, s string) bool { return contains(list, strings.ToUpper(s)) }

// validateRules checks every room's rules and gates. res gets the problems.
func (w *World) validateRules(res *Result) {
	facts := &ruleFacts{
		flagsSet: map[string]bool{}, flagsCleared: map[string]bool{}, flagsRead: map[string]string{}, notRead: map[string]string{}, worldRead: map[string]string{},
		counters: map[string]bool{}, opens: map[string]bool{}, gives: map[string]bool{}, ran: map[string]bool{},
	}
	file := func(key string) string { return w.source["room:"+key] }
	// First pass: counters (so placeholders can be checked in any order).
	for _, rk := range sortedKeys(w.Rooms) {
		for _, r := range w.Rooms[rk].Rules {
			for _, e := range r.Do {
				if e.Add != nil {
					facts.counters[e.Add.Counter] = true
				}
				if e.Set != nil {
					facts.counters[e.Set.Counter] = true
				}
			}
		}
	}
	text := func(f, path, s string) {
		for _, name := range Placeholders(s) {
			if name != "player" && !facts.counters[name] {
				res.errorf(f, path, "{%s} is not a counter any rule changes (placeholders are {player} and counter names)", name)
			}
		}
	}

	for _, rk := range sortedKeys(w.Rooms) {
		r := w.Rooms[rk]
		f, p := file(rk), "rooms."+rk
		if len(r.Rules) > MaxRules {
			res.errorf(f, p+".rules", "%d rules; at most %d per room", len(r.Rules), MaxRules)
		}
		targets := map[string]bool{} // keys a rule in this room may act on
		for k := range r.Features {
			targets[k] = true
		}
		for k := range r.People {
			targets[k] = true
		}
		for k := range w.Items {
			targets[k] = true
		}
		ids := map[string]int{}
		for i, rule := range r.Rules {
			if rule.ID == "" {
				continue
			}
			if !keyPattern.MatchString(rule.ID) {
				res.errorf(f, fmt.Sprintf("%s.rules[%d].id", p, i), "id must be lower case letters, digits and dashes")
			}
			if _, dup := ids[rule.ID]; dup {
				res.errorf(f, fmt.Sprintf("%s.rules[%d].id", p, i), "id %q is used twice in this room", rule.ID)
			}
			ids[rule.ID] = i
		}
		for ft := range r.Features {
			if t := r.Features[ft]; strings.Contains(t.Examine, "{") {
				text(f, p+".features."+ft+".examine", t.Examine)
			}
		}

		var seenOn []string
		for i, rule := range r.Rules {
			rp := fmt.Sprintf("%s.rules[%d]", p, i)
			if rule.ID != "" {
				rp = fmt.Sprintf("%s.rules[%d] (%s)", p, i, rule.ID)
			}
			if len(rule.Do) == 0 {
				res.errorf(f, rp+".do", "a rule needs at least one effect")
			}
			if len(rule.Do) > MaxEffects {
				res.errorf(f, rp+".do", "%d effects; at most %d per rule", len(rule.Do), MaxEffects)
			}

			// Trigger.
			if rule.On == nil {
				if rule.ID == "" {
					res.errorf(f, rp+".on", "a rule with no `on` is only run by another rule, so it needs an id")
				}
			} else {
				t := rule.On
				kinds := 0
				if t.Verb != "" {
					kinds++
				}
				if t.Say != "" {
					kinds++
				}
				if t.Enter {
					kinds++
				}
				switch {
				case kinds != 1:
					res.errorf(f, rp+".on", "needs exactly one of verb, say or enter")
				case t.Verb != "":
					if !contains2(RuleVerbs, t.Verb) {
						res.errorf(f, rp+".on.verb", "%q is not a verb a rule can use (%s)", t.Verb, strings.Join(RuleVerbs, ", "))
					}
					if !targets[t.Target] {
						res.errorf(f, rp+".on.target", "%q is not a feature or person of this room, or an item", t.Target)
					}
					if t.With != "" {
						if _, ok := w.Items[t.With]; !ok {
							res.errorf(f, rp+".on.with", "%q is not an item", t.With)
						}
					}
					if strings.EqualFold(t.Verb, "SET") && t.Value == "" {
						res.errorf(f, rp+".on.value", "SET needs a value (SET <thing> TO <value>)")
					}
					if !strings.EqualFold(t.Verb, "SET") && t.Value != "" {
						res.errorf(f, rp+".on.value", "value is only for SET")
					}
				default:
					if t.Target != "" || t.With != "" || t.Value != "" {
						res.errorf(f, rp+".on", "target, with and value go with a verb")
					}
				}
				if len(rule.If) == 0 && len(rule.Unless) == 0 {
					sig := fmt.Sprintf("%v|%v|%v|%v|%v|%v", t.Verb, t.Target, t.With, t.Value, t.Say, t.Enter)
					for _, prev := range seenOn {
						if prev == sig {
							res.warnf(f, rp, "an earlier rule with the same trigger and no conditions always runs first; this one can never run")
						}
					}
					seenOn = append(seenOn, sig)
				}
			}

			// Conditions.
			conds := func(list []Condition, kind string) {
				for ci, c := range list {
					cp := fmt.Sprintf("%s.%s[%d]", rp, kind, ci)
					name, n := c.kind()
					if msg, ok := set1(n, "condition"); !ok {
						res.errorf(f, cp, "%s", msg)
						continue
					}
					switch name {
					case "flag":
						if _, seen := facts.flagsRead[c.Flag]; !seen {
							facts.flagsRead[c.Flag] = f + ": " + cp
						}
					case "world_flag":
						if _, seen := facts.worldRead[c.WorldFlag]; !seen {
							facts.worldRead[c.WorldFlag] = f + ": " + cp
						}
					case "not_flag":
						if _, seen := facts.notRead[c.NotFlag]; !seen {
							facts.notRead[c.NotFlag] = f + ": " + cp
						}
					case "has_item":
						if _, ok := w.Items[c.HasItem]; !ok {
							res.errorf(f, cp+".has_item", "%q is not an item", c.HasItem)
						}
					case "knows_spell":
						if _, ok := w.Spells[c.KnowsSpell]; !ok {
							res.errorf(f, cp+".knows_spell", "%q is not a spell", c.KnowsSpell)
						}
					case "counter":
						for k := range c.Counter {
							if !facts.counters[k] {
								res.errorf(f, cp+".counter", "counter %q is never changed by any rule", k)
							}
						}
					case "counter_at_least":
						for k := range c.CounterAtLeast {
							if !facts.counters[k] {
								res.errorf(f, cp+".counter_at_least", "counter %q is never changed by any rule", k)
							}
						}
					case "sequence", "sequence_longer_than":
						// Sequences are checked against appends below.
					case "players_in_room_at_least":
						if *c.PlayersInRoomAtLeast < 1 {
							res.errorf(f, cp+".players_in_room_at_least", "must be at least 1")
						}
					}
				}
			}
			conds(rule.If, "if")
			conds(rule.Unless, "unless")

			// Effects.
			for ei, e := range rule.Do {
				ep := fmt.Sprintf("%s.do[%d]", rp, ei)
				name, n := e.kind()
				if msg, ok := set1(n, "effect"); !ok {
					res.errorf(f, ep, "%s", msg)
					continue
				}
				switch name {
				case "say":
					text(f, ep+".say", *e.Say)
				case "room_say":
					text(f, ep+".room_say", *e.RoomSay)
				case "set_flag":
					facts.flagsSet[*e.SetFlag] = true
				case "clear_flag":
					facts.flagsCleared[*e.ClearFlag] = true
				case "add":
					a := e.Add
					if a.By == 0 {
						res.errorf(f, ep+".add.by", "by cannot be 0")
					}
					if len(a.Wrap) != 0 && (len(a.Wrap) != 2 || a.Wrap[0] >= a.Wrap[1]) {
						res.errorf(f, ep+".add.wrap", "wrap is [min, max] with min below max")
					}
				case "append":
					if e.Append.Sequence == "" || e.Append.Value == "" {
						res.errorf(f, ep+".append", "needs a sequence and a value")
					}
				case "unlock":
					switch u := e.Unlock; {
					case (u.Color == "") == (u.Badge == ""):
						res.errorf(f, ep+".unlock", "set exactly one of color and badge")
					case u.Color != "" && !contains(Colors, u.Color):
						res.errorf(f, ep+".unlock.color", "%q is not a colour (%s)", u.Color, strings.Join(Colors, ", "))
					case u.Badge != "" && !contains(Badges, u.Badge):
						res.errorf(f, ep+".unlock.badge", "%q is not a badge (%s)", u.Badge, strings.Join(Badges, ", "))
					}
				case "teach":
					if _, ok := w.Spells[*e.Teach]; !ok {
						res.errorf(f, ep+".teach", "%q is not a spell", *e.Teach)
					}
				case "give_item":
					if _, ok := w.Items[*e.GiveItem]; !ok {
						res.errorf(f, ep+".give_item", "%q is not an item", *e.GiveItem)
					}
					facts.gives[*e.GiveItem] = true
				case "take_item":
					if _, ok := w.Items[*e.TakeItem]; !ok {
						res.errorf(f, ep+".take_item", "%q is not an item", *e.TakeItem)
					}
				case "open", "close":
					g := e.Open
					if name == "close" {
						g = e.Close
					}
					room := g.Room
					if room == "" {
						room = rk
					}
					target, ok := w.Rooms[room]
					if !ok {
						res.errorf(f, ep+"."+name+".room", "%q is not a room", room)
					} else if _, ok := target.Gates[g.Gate]; !ok {
						res.errorf(f, ep+"."+name+".gate", "room %q has no gate %q", room, g.Gate)
					}
					if g.For != "" && g.For != "room" {
						res.errorf(f, ep+"."+name+".for", "for is \"room\" (everyone present) or left out")
					}
					if name == "open" {
						facts.opens[room+"/"+g.Gate] = true
					}
				case "hold":
					h := e.Hold
					if h.Key == "" || h.Seconds < 1 || h.Seconds > MaxHoldSeconds {
						res.errorf(f, ep+".hold", "needs a key and seconds from 1 to %d", MaxHoldSeconds)
					}
				case "teleport":
					if _, ok := w.Rooms[*e.Teleport]; !ok {
						res.errorf(f, ep+".teleport", "%q is not a room", *e.Teleport)
					}
				case "stop":
					if !*e.Stop {
						res.errorf(f, ep+".stop", "use `stop: true`")
					}
				case "run":
					facts.ran[rk+"/"+*e.Run] = true
					if _, ok := ids[*e.Run]; !ok {
						res.errorf(f, ep+".run", "this room has no rule with the id %q", *e.Run)
					}
				}
			}
		}

		// run cycles and depth, within this room.
		var visit func(id string, depth int, path []string)
		visit = func(id string, depth int, path []string) {
			i, ok := ids[id]
			if !ok {
				return
			}
			for _, e := range r.Rules[i].Do {
				if e.Run == nil {
					continue
				}
				next := *e.Run
				for _, seen := range path {
					if seen == next {
						res.errorf(f, p+".rules", "rules run each other in a cycle: %s", strings.Join(append(append([]string{}, path...), next), " -> "))
						return
					}
				}
				if depth+1 > MaxRunDepth {
					res.errorf(f, p+".rules", "rule %q runs rules more than %d deep", path[0], MaxRunDepth)
					return
				}
				visit(next, depth+1, append(append([]string{}, path...), next))
			}
		}
		for _, id := range sortedKeys(ids) {
			visit(id, 1, []string{id})
		}

		// Gates.
		usedDir := map[string]string{}
		for _, gk := range sortedKeys(r.Gates) {
			g := r.Gates[gk]
			gp := p + ".gates." + gk
			if !keyPattern.MatchString(gk) {
				res.errorf(f, gp, "key must be lower case letters, digits and dashes, starting with a letter or digit, at most 40 characters")
			}
			if !contains(Directions, g.Exit.Dir) {
				res.errorf(f, gp+".exit.dir", "%q is not a direction (use %s)", g.Exit.Dir, strings.Join(Directions, ", "))
			} else {
				if _, plain := r.Exits[g.Exit.Dir]; plain {
					res.errorf(f, gp+".exit.dir", "this room already has an ordinary %s exit", g.Exit.Dir)
				}
				if other, dup := usedDir[g.Exit.Dir]; dup {
					res.errorf(f, gp+".exit.dir", "gate %q already uses %s", other, g.Exit.Dir)
				}
				usedDir[g.Exit.Dir] = gk
			}
			dest, ok := w.Rooms[g.Exit.To]
			if !ok {
				res.errorf(f, gp+".exit.to", "%q is not a room", g.Exit.To)
			} else if contains(Directions, g.Exit.Dir) {
				back := opposite[g.Exit.Dir]
				_, plain := dest.Exits[back]
				gated := false
				for _, dg := range dest.Gates {
					if dg.Exit.Dir == back && dg.Exit.To == rk {
						gated = true
					}
				}
				if !plain && !gated {
					res.warnf(f, gp, "one-way: %q has no %s exit or gate back to %q", g.Exit.To, back, rk)
				}
			}
			switch g.Mode {
			case "hidden":
			case "locked":
				if strings.TrimSpace(g.Message) == "" {
					res.errorf(f, gp+".message", "a locked gate needs a message for when someone tries it")
				}
			default:
				res.errorf(f, gp+".mode", "mode is hidden or locked")
			}
			if g.OpenWhen != "" {
				if !keyPattern.MatchString(g.OpenWhen) {
					res.errorf(f, gp+".open_when", "a world flag name is lower case words joined by dashes")
				}
				if _, seen := facts.worldRead[g.OpenWhen]; !seen {
					facts.worldRead[g.OpenWhen] = f + ": " + gp + ".open_when"
				}
			}
		}
	}

	// Checks that need every room's rules read.
	for _, rk := range sortedKeys(w.Rooms) {
		f := file(rk)
		for _, gk := range sortedKeys(w.Rooms[rk].Gates) {
			if !facts.opens[rk+"/"+gk] && w.Rooms[rk].Gates[gk].OpenWhen == "" {
				res.errorf(f, "rooms."+rk+".gates."+gk, "no rule opens this gate, so no one could ever use it")
			}
		}
		for i, rule := range w.Rooms[rk].Rules {
			if rule.On == nil && rule.ID != "" && !facts.ran[rk+"/"+rule.ID] {
				res.warnf(f, fmt.Sprintf("rooms.%s.rules[%d] (%s)", rk, i, rule.ID), "no rule runs this one")
			}
		}
	}
	for _, name := range sortedKeys(facts.flagsRead) {
		if !facts.flagsSet[name] {
			res.errorf("", "", "flag %q is read (%s) but no rule ever sets it", name, facts.flagsRead[name])
		}
	}
	for _, name := range sortedKeys(facts.notRead) {
		if !facts.flagsSet[name] {
			res.warnf("", "", "flag %q is tested with not_flag (%s) but no rule ever sets it, so the test is always true", name, facts.notRead[name])
		}
	}
	w.validatePuzzles(res, facts.flagsSet)
	w.validateEvents(res)
	eventFlags := w.eventFlags()
	for _, name := range sortedKeys(facts.worldRead) {
		if !eventFlags[name] {
			res.errorf("", "", "world flag %q is read (%s) but no event sets it", name, facts.worldRead[name])
		}
	}
	dropped := map[string]bool{}
	for _, n := range w.NPCs {
		for _, d := range n.Drops {
			dropped[d.Item] = true
		}
	}
	for _, ik := range sortedKeys(w.Items) {
		if w.Items[ik].Bound && !facts.gives[ik] && !dropped[ik] {
			res.errorf(w.source["item:"+ik], "items."+ik, "a bound item is a puzzle token, but no rule or monster drop gives it")
		}
	}
}
