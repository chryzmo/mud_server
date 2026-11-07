package mudserver

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"mud_server/src/internal/muddata"
	"mud_server/src/internal/worldfile"
)

/*
Orientation (plans/interactive-world.md section 8): JOURNAL says where the player is in each
puzzle they have started, HINT gives the next hint for one on request, and MAP lists the rooms
they have discovered. All of it is built from the player's own state and the content's
declarations (puzzles, region): the engine knows nothing about any particular puzzle.
*/

// guide is the declared orientation data, built once from the content. It never changes.
type guide struct {
	puzzles     []guidePuzzle
	byFlag      map[string][]string // a step or solved flag -> the keys of the puzzles it belongs to
	regionOf    map[string]string // room key -> region (rooms without one are not listed)
	regionTotal map[string]int
	totalRooms  int
}

type guidePuzzle struct {
	key string
	worldfile.Puzzle
}

const (
	elsewhere    = "Elsewhere"
	hintCooldown = 2 * time.Second // a double tap shows the same hint again instead of burning the next
)

func newGuide(puzzles map[string]worldfile.Puzzle, rooms map[string]worldfile.Room) *guide {
	g := &guide{byFlag: map[string][]string{}, regionOf: map[string]string{}, regionTotal: map[string]int{}}
	for _, key := range sortedKeys(puzzles) {
		p := puzzles[key]
		g.puzzles = append(g.puzzles, guidePuzzle{key: key, Puzzle: p})
		for _, s := range p.Steps {
			g.byFlag[s.Flag] = append(g.byFlag[s.Flag], key)
		}
		g.byFlag[p.Solved.Flag] = append(g.byFlag[p.Solved.Flag], key)
	}
	for rk, r := range rooms {
		region := r.Region
		if region == "" {
			region = elsewhere
		}
		g.regionOf[rk] = region
		g.regionTotal[region]++
		g.totalRooms++
	}
	return g
}

func (g *guide) puzzle(key string) *guidePuzzle {
	for i := range g.puzzles {
		if g.puzzles[i].key == key {
			return &g.puzzles[i]
		}
	}
	return nil
}

// started: any step or the solved flag is set.
func (mp *MudPlayer) puzzleStarted(p *guidePuzzle) bool {
	if mp.flag(p.Solved.Flag) {
		return true
	}
	for _, s := range p.Steps {
		if mp.flag(s.Flag) {
			return true
		}
	}
	return false
}

func (mp *MudPlayer) visit(roomKey string) {
	if roomKey != "" && !mp.st().visited[roomKey] {
		mp.st().visited[roomKey] = true
		mp.persistState("visited", roomKey, "1", false)
		mp.discoverRoom()
	}
}

func (mp *MudPlayer) journal() {
	g := mp.world.guide
	var b strings.Builder
	b.WriteString("Your journal\n")
	any := false
	for i := range g.puzzles {
		p := &g.puzzles[i]
		if !mp.puzzleStarted(p) {
			continue
		}
		any = true
		b.WriteString("\n" + p.Title + "\n")
		for _, s := range p.Steps {
			if mp.flag(s.Flag) {
				b.WriteString("  - " + mp.fill(s.Line) + "\n")
			}
		}
		if mp.flag(p.Solved.Flag) {
			b.WriteString("  * " + mp.fill(p.Solved.Line) + " (solved)\n")
		}
	}
	if !any {
		b.WriteString("\nNothing yet. Look around, examine things, talk to people; what you learn is written here.\n")
	}
	b.WriteString("\nRooms discovered: " + strconv.Itoa(len(mp.st().visited)) + " of " + strconv.Itoa(g.totalRooms) + ".")
	mp.SendMessage(b.String())
}

// hint answers HINT [puzzle]: the puzzle named, else the one touched last, else the first one begun.
func (mp *MudPlayer) hint(arguments string) {
	g := mp.world.guide
	var p *guidePuzzle
	if q := strings.ToLower(strings.TrimSpace(arguments)); q != "" {
		var found []*guidePuzzle
		for i := range g.puzzles {
			c := &g.puzzles[i]
			if mp.puzzleStarted(c) && (c.key == q || strings.HasPrefix(strings.ToLower(c.Title), q)) {
				found = append(found, c)
			}
		}
		if len(found) != 1 {
			mp.SendMessage("Which puzzle? HINT alone picks the one you worked on last. See JOURNAL for the ones you have begun.")
			return
		}
		p = found[0]
	} else {
		p = g.puzzle(mp.lastPuzzle)
		if p == nil || mp.flag(p.Solved.Flag) {
			p = nil
			for i := range g.puzzles {
				if c := &g.puzzles[i]; mp.puzzleStarted(c) && !mp.flag(c.Solved.Flag) {
					p = c
					break
				}
			}
		}
		if p == nil {
			mp.SendMessage("You have no puzzle underway. Explore first; when something has you stumped, ask again.")
			return
		}
	}
	if mp.flag(p.Solved.Flag) {
		mp.SendMessage("You solved " + p.Title + " already.")
		return
	}
	if len(p.Hints) == 0 {
		mp.SendMessage("No hint for " + p.Title + ": you are on your own with this one.")
		return
	}
	tier := mp.st().hints[p.key]
	now := time.Now()
	if tier == 0 || now.Sub(mp.lastHint) >= hintCooldown {
		if tier < len(p.Hints) {
			tier++
		}
		mp.st().hints[p.key] = tier
		mp.persistState("hint", p.key, strconv.Itoa(tier), false)
	}
	mp.lastHint = now
	text := "Hint " + strconv.Itoa(tier) + " of " + strconv.Itoa(len(p.Hints)) + " for " + p.Title + ": " + mp.fill(p.Hints[tier-1])
	if tier < len(p.Hints) {
		text += "\n(HINT again for a bigger one.)"
	}
	mp.SendMessage(text)
}

// mapView lists the rooms the player has discovered by region, never naming one they have not.
func (mp *MudPlayer) mapView() {
	g := mp.world.guide
	room := mp.Location
	var b strings.Builder
	b.WriteString("You are in '" + room.Name + "'")
	if region, ok := g.regionOf[room.Key]; ok && region != elsewhere {
		b.WriteString(" (" + region + ")")
	}
	b.WriteString(".\n")
	if exits := mp.usableExits(); len(exits) > 0 {
		sort.Strings(exits)
		b.WriteString("Exits: [" + strings.Join(exits, "] [") + "]\n")
	}
	names := map[string][]string{}
	seen := 0
	for key := range mp.st().visited {
		region, ok := g.regionOf[key]
		if !ok {
			continue // a room the content no longer has
		}
		seen++
		names[region] = append(names[region], mp.world.roomsByKey[key].Name)
	}
	regions := make([]string, 0, len(names))
	for region := range names {
		regions = append(regions, region)
	}
	sort.Strings(regions)
	for _, region := range regions {
		sort.Strings(names[region])
		b.WriteString("\n" + region + " (" + strconv.Itoa(len(names[region])) + " of " + strconv.Itoa(g.regionTotal[region]) + "): " + strings.Join(names[region], ", ") + "\n")
	}
	b.WriteString("\nDiscovered " + strconv.Itoa(seen) + " of " + strconv.Itoa(g.totalRooms) + " rooms.")
	mp.SendMessage(b.String())
}

func registerGuide(cList map[string]*MudCommand) {
	add := func(name, description string, run func(*MudPlayer, string)) {
		cList[name] = &MudCommand{description: description, action: func(player *MudPlayer, arguments string) int {
			run(player, arguments)
			return muddata.MUD_OK
		}}
	}
	add("JOURNAL", "What you have learned about the puzzles you have begun.  Usage: JOURNAL", func(p *MudPlayer, _ string) { p.journal() })
	add("HINT", "A hint for a puzzle you have begun; ask again for a bigger one.  Usage: HINT [puzzle]", func(p *MudPlayer, a string) { p.hint(a) })
	add("MAP", "The rooms you have discovered.  Usage: MAP", func(p *MudPlayer, _ string) { p.mapView() })
}
