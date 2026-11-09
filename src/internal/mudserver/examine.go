package mudserver

import (
	"sort"
	"strconv"
	"strings"

	"mud_server/src/internal/mudutils"
)

/*
EXAMINE (plans/interactive-world.md). Players can look closely at what is around them: the
scenery a room's content describes (its features), the items lying in the room or in the
pack, the other players, and the monsters. Names match whole or by a unique prefix of three
or more letters (of the name or of any word in it), case-insensitively; when several things
fit, the player is told which. "MY <item>" looks only in the pack. LOOK AT <thing> is the
same command.
*/

// Feature is scenery: something a room has that can be looked at but not picked up. The first
// name is what it is called; the rest are aliases. Unlisted features are left out of LOOK's
// "You notice" line (for the player to find) but can still be examined.
type Feature struct {
	Key      string   `json:"key"`
	Names    []string `json:"names"`
	Examine  string   `json:"examine"`
	Unlisted bool     `json:"unlisted,omitempty"`
}

// listedFeatures are the display names LOOK mentions.
func (r *Room) listedFeatures() []string {
	var out []string
	for _, f := range r.Features {
		if !f.Unlisted && len(f.Names) > 0 {
			out = append(out, f.Names[0])
		}
	}
	return out
}

// examineTarget is one thing that could be meant: what to call it, the names that match it,
// and what to say about it.
type examineTarget struct {
	display string
	names   []string
	text    string
	key     string // the content key of a feature, person or item (rules act on keys); "" for others
	feature bool   // a room feature: examining one for the first time earns exploration XP
}

const minPrefix = 3

// matchScore is 2 for an exact name, 1 for a prefix of the name or of one of its words, else 0.
func matchScore(name, query string) int {
	name = strings.ToUpper(strings.TrimSpace(name))
	switch {
	case name == query:
		return 2
	case len(query) < minPrefix:
		return 0
	case strings.HasPrefix(name, query):
		return 1
	}
	for _, w := range strings.Fields(name) {
		if strings.HasPrefix(w, query) {
			return 1
		}
	}
	return 0
}

// pickTarget finds the one target a query means. Exact matches beat prefix matches. It
// returns the target, or the display names of several that fit, or neither.
func pickTarget(targets []examineTarget, query string) (hit *examineTarget, ambiguous []string) {
	best := 0
	scores := make([]int, len(targets))
	for i, t := range targets {
		for _, n := range t.names {
			if s := matchScore(n, query); s > scores[i] {
				scores[i] = s
			}
		}
		if scores[i] > best {
			best = scores[i]
		}
	}
	if best == 0 {
		return nil, nil
	}
	var found []int
	for i, s := range scores {
		if s == best {
			found = append(found, i)
		}
	}
	if len(found) == 1 {
		return &targets[found[0]], nil
	}
	seen := map[string]bool{}
	for _, i := range found {
		if d := targets[i].display; !seen[d] {
			seen[d] = true
			ambiguous = append(ambiguous, d)
		}
	}
	sort.Strings(ambiguous)
	return nil, ambiguous
}

// examineTargets lists what the player can look at. Loop only.
func (mp *MudPlayer) examineTargets(packOnly bool) []examineTarget {
	var out []examineTarget
	item := func(it *Item) examineTarget {
		text := mudutils.ColorString(it.Name, mudutils.COLOR_GREEN) + ": " + strings.TrimSpace(it.Description)
		if strings.TrimSpace(it.Description) == "" {
			text = mudutils.ColorString(it.Name, mudutils.COLOR_GREEN) + ": nothing special."
		}
		key := ""
		if it.ContentKey != nil {
			key = *it.ContentKey
		}
		return examineTarget{display: it.Name, names: []string{it.Name}, text: text, key: key}
	}
	if !packOnly && mp.Location != nil {
		room := mp.Location
		for _, f := range room.Features {
			if len(f.Names) > 0 {
				out = append(out, examineTarget{display: f.Names[0], names: f.Names, text: mp.fill(f.Examine), key: f.Key, feature: true})
			}
		}
		for _, pe := range room.People {
			if len(pe.Names) > 0 {
				out = append(out, examineTarget{display: pe.Names[0], names: pe.Names, text: mp.fill(pe.Examine), key: pe.Key})
			}
		}
		if room.Inventory != nil {
			for _, it := range room.Inventory.items {
				out = append(out, item(it))
			}
		}
		for name, p := range room.UserMap {
			who := name
			if p == mp {
				out = append(out, examineTarget{display: who, names: []string{who}, text: "That is you."})
				continue
			}
			out = append(out, examineTarget{display: who, names: []string{who}, text: playerDescription(who, p)})
		}
		for _, b := range room.BaddieMap {
			out = append(out, examineTarget{
				display: b.Name, names: []string{b.Name},
				text: mudutils.ColorString(b.Name, mudutils.COLOR_RED) + ", a " + strings.ToLower(b.Race) + ". It looks ready for a fight.",
			})
		}
	}
	if mp.Inventory != nil {
		for _, it := range mp.Inventory.items {
			out = append(out, item(it))
		}
	}
	return out
}

// examine answers EXAMINE <thing>. Loop only.
func (mp *MudPlayer) examine(arguments string) {
	query := strings.ToUpper(strings.Join(strings.Fields(arguments), " "))
	for _, lead := range []string{"AT ", "THE "} {
		query = strings.TrimPrefix(query, lead)
	}
	packOnly := false
	if strings.HasPrefix(query, "MY ") {
		packOnly, query = true, strings.TrimPrefix(query, "MY ")
	}
	if query == "" {
		mp.SendMessage("Examine what? Try EXAMINE <thing>.")
		return
	}
	hit, several := pickTarget(mp.examineTargets(packOnly), query)
	switch {
	case hit != nil:
		if hit.feature && mp.Location != nil {
			mp.discoverFeature(mp.Location.Key, hit.key, hit.display) // the XP line first, then what they came to read
		}
		mp.SendMessage(hit.text)
		mp.examined(hit.key) // rules for examining this thing, such as a clue noted in the journal
		mp.tourDid("examine")
	case len(several) > 0:
		mp.SendMessage("Which do you mean: " + strings.Join(several, ", ") + "?")
	case packOnly:
		mp.SendMessage("You are not carrying that.")
	default:
		mp.SendMessage("You do not see that here.")
	}
}

// playerDescription is what EXAMINE <player> says about another player: their level, what they wield, wear and
// carry, and how hurt they look. Exact health stays hidden; only a look is given.
func playerDescription(who string, p *MudPlayer) string {
	level, _, _ := p.xpFields()
	return mudutils.ColorString(who, mudutils.COLOR_BLUE) + " is here with you, level " + strconv.Itoa(level) + ". " + gearSentence(p) + " " + healthSentence(p)
}

// gearSentence says what a player wields (weapon), wears (armor, boots, cosmetic) and carries (shield).
func gearSentence(p *MudPlayer) string {
	e := p.Equipment
	if e == nil {
		return "They have no gear to speak of."
	}
	the := func(it *EquipmentItem) string { return "the " + strings.ToLower(it.Name) }
	var clauses []string
	if e.Weapon != nil {
		clauses = append(clauses, "wield "+the(e.Weapon))
	}
	var wear []string
	for _, it := range []*EquipmentItem{e.Armor, e.Boots, e.Cosmetic} {
		if it != nil {
			wear = append(wear, the(it))
		}
	}
	if len(wear) > 0 {
		clauses = append(clauses, "wear "+joinList(wear, false))
	}
	if e.Shield != nil {
		clauses = append(clauses, "carry "+the(e.Shield))
	}
	if len(clauses) == 0 {
		return "They have no gear to speak of."
	}
	return "They " + joinList(clauses, true) + "."
}

// healthSentence is how hurt a player looks, from their health as a share of their maximum.
func healthSentence(p *MudPlayer) string {
	max := p.EffectiveMaxHP()
	if max <= 0 {
		return "They look hale and healthy."
	}
	switch pct := p.HP * 100 / max; {
	case pct >= 90:
		return "They look hale and healthy."
	case pct >= 65:
		return "They have a few scratches."
	case pct >= 40:
		return "They are wounded."
	case pct >= 15:
		return "They are badly hurt."
	}
	return "They are barely standing."
}

// joinList joins items with commas and "and" before the last; oxford puts a comma before that "and" when there
// are three or more (for clauses that contain their own lists).
func joinList(items []string, oxford bool) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	}
	sep := " and "
	if oxford {
		sep = ", and "
	}
	return strings.Join(items[:len(items)-1], ", ") + sep + items[len(items)-1]
}
