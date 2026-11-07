package worldfile

import (
	"fmt"
	"strings"
)

/*
Ambient life and time of day (plans/interactive-world.md section 9). A room may list ambient entries:
lines the server sends to the room, at a random gap within a range of seconds, but only while players
are there (nothing runs in an empty room). A person may list idle lines, which work the same way. The
real clock in the con's zone gives the time of day: dawn, day, dusk or night, and a room's day_notes
add a line to LOOK for the current one.
*/

// TimesOfDay are the phases of the day, by the real clock in America/New_York: dawn 06-08, day 08-18, dusk 18-20, night 20-06.
var TimesOfDay = []string{"dawn", "day", "dusk", "night"}

const (
	MinAmbientEvery   = 15   // seconds: the least gap between two lines of one entry
	MaxAmbientEvery   = 3600 // seconds
	MaxAmbientEntries = 6    // per room, not counting people
	MaxAmbientLines   = 12   // per entry
	MaxAmbientLine    = 200  // characters
	DefaultIdleMin    = 120  // seconds, for a person's idle lines
	DefaultIdleMax    = 300
)

// Ambient is one entry of a room's ambient lines. Every is [least, most] seconds between lines.
type Ambient struct {
	Every []int        `yaml:"every"`
	When  *AmbientWhen `yaml:"when"`
	Lines []string     `yaml:"lines"`
}

// AmbientWhen limits when an entry may fire; both parts are optional.
type AmbientWhen struct {
	TimeOfDay      string `yaml:"time_of_day"`
	PlayersAtLeast int    `yaml:"players_at_least"`
}

func (w *World) validateAmbient(res *Result) {
	for _, key := range sortedKeys(w.Rooms) {
		r := w.Rooms[key]
		f, p := w.source["room:"+key], "rooms."+key
		if len(r.Ambient) > MaxAmbientEntries {
			res.errorf(f, p+".ambient", "%d entries; at most %d per room", len(r.Ambient), MaxAmbientEntries)
		}
		for i, a := range r.Ambient {
			ap := fmt.Sprintf("%s.ambient[%d]", p, i)
			validateEvery(res, f, ap+".every", a.Every, false)
			validateLines(res, f, ap+".lines", a.Lines)
			if a.When != nil {
				if a.When.TimeOfDay != "" && !contains(TimesOfDay, a.When.TimeOfDay) {
					res.errorf(f, ap+".when.time_of_day", "%q is not a time of day (%s)", a.When.TimeOfDay, strings.Join(TimesOfDay, ", "))
				}
				if a.When.PlayersAtLeast < 0 || a.When.PlayersAtLeast > 1000 {
					res.errorf(f, ap+".when.players_at_least", "must be from 0 to 1000")
				}
			}
		}
		for _, phase := range sortedKeys(r.DayNotes) {
			note := r.DayNotes[phase]
			if !contains(TimesOfDay, phase) {
				res.errorf(f, p+".day_notes."+phase, "not a time of day (%s)", strings.Join(TimesOfDay, ", "))
			}
			if len([]rune(note)) > MaxAmbientLine {
				res.warnf(f, p+".day_notes."+phase, "is %d characters; keep it under %d", len([]rune(note)), MaxAmbientLine)
			}
		}
		for _, pk := range sortedKeys(r.People) {
			pe := r.People[pk]
			pp := p + ".people." + pk
			if len(pe.Idle) == 0 && len(pe.IdleEvery) > 0 {
				res.warnf(f, pp+".idle_every", "has no idle lines to space out")
			}
			if len(pe.Idle) > 0 {
				validateLines(res, f, pp+".idle", pe.Idle)
				validateEvery(res, f, pp+".idle_every", pe.IdleEvery, true)
			}
		}
	}
}

func validateEvery(res *Result, f, path string, every []int, optional bool) {
	if len(every) == 0 && optional {
		return
	}
	if len(every) != 2 {
		res.errorf(f, path, "must be [least, most] seconds, like [90, 240]")
		return
	}
	lo, hi := every[0], every[1]
	if lo < MinAmbientEvery || hi > MaxAmbientEvery || lo > hi {
		res.errorf(f, path, "[%d, %d] must satisfy %d <= least <= most <= %d seconds", lo, hi, MinAmbientEvery, MaxAmbientEvery)
	}
}

func validateLines(res *Result, f, path string, lines []string) {
	if len(lines) == 0 {
		res.errorf(f, path, "needs at least one line")
	}
	if len(lines) > MaxAmbientLines {
		res.errorf(f, path, "%d lines; at most %d", len(lines), MaxAmbientLines)
	}
	for i, l := range lines {
		lp := fmt.Sprintf("%s[%d]", path, i)
		switch n := len([]rune(strings.TrimSpace(l))); {
		case n == 0:
			res.errorf(f, lp, "is empty")
		case n > MaxAmbientLine:
			res.errorf(f, lp, "is %d characters; at most %d", n, MaxAmbientLine)
		}
	}
}
