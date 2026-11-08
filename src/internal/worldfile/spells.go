package worldfile

import (
	"strings"
	"unicode/utf8"
)

// Spells (plans/event-systems.md section 12). A character learns any number and readies three. A spell is
// defined once in content; how it works is its Effect. Combat effects (bolt, mend, ward) change numbers; a
// fun effect only shows the lines. Learning comes from a scroll item (Item.Teaches, used with READ), the
// `teach` rule effect, or a monster's drops.

const (
	MaxSpells      = 30  // in a world
	MaxSpellLine   = 160 // characters in any line a spell shows
	MaxSpellCost   = 50
	MaxSpellPower  = 100
	MinFunCooldown = 3 // seconds: a fun spell is chatter, so it is never faster than this
	MaxCooldown    = 600
)

// SpellEffects are what a spell can do.
var SpellEffects = []string{"bolt", "mend", "ward", "fun"}

// Spell is one learnable spell.
type Spell struct {
	Name        string `yaml:"name"`
	Effect      string `yaml:"effect"`      // bolt, mend, ward or fun
	Cost        int    `yaml:"cost"`        // magic points
	Cooldown    int    `yaml:"cooldown"`    // seconds before it can be cast again
	Power       int    `yaml:"power"`       // bolt: damage added to the caster's attack; mend: health restored; ward: defence added
	Description string `yaml:"description"` // what SPELLS shows
	Cast        string `yaml:"cast"`        // shown to the caster
	Room        string `yaml:"room"`        // shown to everyone else in the room; {player} is the caster
}

func (w *World) validateSpells(res *Result) {
	if len(w.Spells) > MaxSpells {
		res.errorf("", "spells", "%d spells; at most %d", len(w.Spells), MaxSpells)
	}
	names := map[string]string{}
	for _, key := range sortedKeys(w.Spells) {
		s := w.Spells[key]
		f, p := w.source["spell:"+key], "spells."+key
		if !keyPattern.MatchString(key) {
			res.errorf(f, p, "key must be lower case letters, digits and dashes, starting with a letter or digit, at most 40 characters")
		}
		upper := strings.ToUpper(strings.TrimSpace(s.Name))
		switch {
		case upper == "" || utf8.RuneCountInString(s.Name) > 30 || strings.ContainsAny(s.Name, "\n\r\x1b"):
			res.errorf(f, p+".name", "a name of 1 to 30 characters on one line is required")
		case names[upper] != "":
			res.errorf(f, p+".name", "%q is already used by spell %q", s.Name, names[upper])
		default:
			names[upper] = key
		}
		if !contains(SpellEffects, s.Effect) {
			res.errorf(f, p+".effect", "effect %q is not one of %s", s.Effect, strings.Join(SpellEffects, ", "))
		}
		if s.Cost < 0 || s.Cost > MaxSpellCost {
			res.errorf(f, p+".cost", "cost is from 0 to %d magic points", MaxSpellCost)
		}
		if s.Cooldown < 0 || s.Cooldown > MaxCooldown {
			res.errorf(f, p+".cooldown", "cooldown is from 0 to %d seconds", MaxCooldown)
		}
		if s.Effect == "fun" && s.Cooldown < MinFunCooldown {
			res.errorf(f, p+".cooldown", "a fun spell waits at least %d seconds (it is chatter)", MinFunCooldown)
		}
		if s.Power < 0 || s.Power > MaxSpellPower {
			res.errorf(f, p+".power", "power is from 0 to %d", MaxSpellPower)
		}
		if (s.Effect == "bolt" || s.Effect == "mend" || s.Effect == "ward") && s.Power == 0 {
			res.errorf(f, p+".power", "a %s needs a power above 0", s.Effect)
		}
		for name, text := range map[string]string{"description": s.Description, "cast": s.Cast, "room": s.Room} {
			switch {
			case strings.TrimSpace(text) == "" && name != "room":
				res.errorf(f, p+"."+name, "a spell needs %s text", name)
			case utf8.RuneCountInString(text) > MaxSpellLine || strings.ContainsAny(text, "\n\r\x1b"):
				res.errorf(f, p+"."+name, "%s is one line of at most %d characters", name, MaxSpellLine)
			}
		}
		if s.Effect == "fun" && strings.TrimSpace(s.Room) == "" {
			res.errorf(f, p+".room", "a fun spell needs a room line: it is for others to see")
		}
	}
}
