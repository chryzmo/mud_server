package worldfile

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

/*
XP and levels (plans/event-systems.md section 6). Content may carry one progression: block; any part
left out takes the default below, so a world with none still has levels. Level comes from total
XP (exploration plus combat) through Levels, which lists the XP at which each level starts.
*/

// Progression is the tuning of XP and levels.
type Progression struct {
	Levels   []int `yaml:"levels"` // total XP at which level 1, 2, 3... starts; the first is 0
	PerLevel struct {
		MaxHP   int `yaml:"max_hp"`
		Attack  int `yaml:"attack"`
		Defense int `yaml:"defense"`
	} `yaml:"per_level"` // what each level after the first adds
	XP struct {
		Room    int `yaml:"room"`    // the first time a room is entered (exploration)
		Feature int `yaml:"feature"` // the first time a feature is examined (exploration)
		Kill    int `yaml:"kill"`    // a monster killed (combat)
	} `yaml:"xp"`
}

// MaxLevels is the most levels a table may have; MaxXPAward the most any one award may be.
const (
	MaxLevels   = 50
	MaxXPAward  = 1000
	maxLevelGap = 1000000
)

// DefaultProgression is used for whatever content does not set.
func DefaultProgression() Progression {
	var p Progression
	p.Levels = []int{0, 25, 60, 110, 180, 270, 380, 520, 700, 900}
	p.PerLevel.MaxHP, p.PerLevel.Attack, p.PerLevel.Defense = 2, 1, 1
	p.XP.Room, p.XP.Feature, p.XP.Kill = 10, 5, 5
	return p
}

// UnmarshalYAML starts from the defaults, so a block that sets only some parts keeps the rest.
// A zero written in content is a real zero (no XP from that source).
func (p *Progression) UnmarshalYAML(n *yaml.Node) error {
	type plain Progression // no methods: avoids recursing into this one
	d := plain(DefaultProgression())
	if err := n.Decode(&d); err != nil {
		return err
	}
	*p = Progression(d)
	return nil
}

// ResolvedProgression is the content's progression, or the defaults when it has none.
func (w *World) ResolvedProgression() Progression {
	d := DefaultProgression()
	if w.Progression == nil {
		return d
	}
	return *w.Progression
}

func (w *World) validateProgression(res *Result) {
	p := w.Progression
	if p == nil {
		return
	}
	f := w.source["progression"]
	if len(p.Levels) > MaxLevels {
		res.errorf(f, "progression.levels", "at most %d levels", MaxLevels)
	}
	for i, v := range p.Levels {
		switch {
		case i == 0 && v != 0:
			res.errorf(f, "progression.levels[0]", "the first level must start at 0 XP")
		case i > 0 && v <= p.Levels[i-1]:
			res.errorf(f, fmt.Sprintf("progression.levels[%d]", i), "must be more than the level before (%d)", p.Levels[i-1])
		case v > maxLevelGap*10:
			res.errorf(f, fmt.Sprintf("progression.levels[%d]", i), "is absurdly large")
		}
	}
	for name, v := range map[string]int{
		"per_level.max_hp": p.PerLevel.MaxHP, "per_level.attack": p.PerLevel.Attack, "per_level.defense": p.PerLevel.Defense,
		"xp.room": p.XP.Room, "xp.feature": p.XP.Feature, "xp.kill": p.XP.Kill,
	} {
		if v < 0 || v > MaxXPAward {
			res.errorf(f, "progression."+name, "must be from 0 to %d", MaxXPAward)
		}
	}
}
