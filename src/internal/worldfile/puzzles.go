package worldfile

import (
	"fmt"
	"unicode/utf8"
)

// Puzzle limits: what JOURNAL and HINT show must stay short enough to read on a phone.
const (
	MaxPuzzleSteps = 12
	MaxHints       = 5
	maxTitle       = 60
	maxJournalLine = 200
	maxHint        = 300
	maxRegion      = 40
)

// Puzzle is declared so the journal and hints can follow a player through it
// (plans/interactive-world.md section 8). It names flags the rules set; it adds no behavior.
type Puzzle struct {
	Title string `yaml:"title"`
	// Steps are in order; the journal shows the line of every step whose flag is set. A puzzle
	// counts as started when any step or the solved flag is set.
	Steps  []PuzzleStep `yaml:"steps"`
	Solved PuzzleStep   `yaml:"solved"`
	// Hints go from a nudge to nearly the answer; each HINT asks for the next.
	Hints []string `yaml:"hints"`
}

// PuzzleStep is a flag and the journal line that reaching it earns.
type PuzzleStep struct {
	Flag string `yaml:"flag"`
	Line string `yaml:"line"`
}

func (w *World) validatePuzzles(res *Result, flagsSet map[string]bool) {
	for _, pk := range sortedKeys(w.Puzzles) {
		p := w.Puzzles[pk]
		f, path := w.source["puzzle:"+pk], "puzzles."+pk
		if !keyPattern.MatchString(pk) {
			res.errorf(f, path, "puzzle key %q must be lower case words joined by dashes", pk)
		}
		if p.Title == "" || utf8.RuneCountInString(p.Title) > maxTitle {
			res.errorf(f, path+".title", "a title of 1 to %d characters is required", maxTitle)
		}
		if len(p.Steps) == 0 || len(p.Steps) > MaxPuzzleSteps {
			res.errorf(f, path+".steps", "%d steps; between 1 and %d are required", len(p.Steps), MaxPuzzleSteps)
		}
		check := func(s PuzzleStep, where string) {
			if s.Flag == "" || s.Line == "" {
				res.errorf(f, where, "a flag and a line are both required")
				return
			}
			if !flagsSet[s.Flag] {
				res.errorf(f, where+".flag", "flag %q is never set by any rule, so this step could never be reached", s.Flag)
			}
			if utf8.RuneCountInString(s.Line) > maxJournalLine {
				res.errorf(f, where+".line", "a journal line is at most %d characters", maxJournalLine)
			}
		}
		for i, s := range p.Steps {
			check(s, fmt.Sprintf("%s.steps[%d]", path, i))
		}
		check(p.Solved, path+".solved")
		if len(p.Hints) == 0 {
			res.warnf(f, path+".hints", "no hints: HINT will have nothing to say for this puzzle")
		}
		if len(p.Hints) > MaxHints {
			res.errorf(f, path+".hints", "%d hints; at most %d", len(p.Hints), MaxHints)
		}
		for i, h := range p.Hints {
			if h == "" || utf8.RuneCountInString(h) > maxHint {
				res.errorf(f, fmt.Sprintf("%s.hints[%d]", path, i), "a hint of 1 to %d characters is required", maxHint)
			}
		}
	}
	for _, rk := range sortedKeys(w.Rooms) {
		if r := w.Rooms[rk].Region; utf8.RuneCountInString(r) > maxRegion {
			res.errorf(w.source["room:"+rk], "rooms."+rk+".region", "a region name is at most %d characters", maxRegion)
		}
	}
}
