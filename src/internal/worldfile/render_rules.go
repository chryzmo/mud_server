package worldfile

import (
	"fmt"
	"sort"
	"strings"
)

// Plain-language summaries of rules and gates for the review overview (-render-world), so a
// puzzle can be read and checked without reading YAML.

func describeTrigger(t *Trigger) string {
	switch {
	case t == nil:
		return "(only when another rule runs it)"
	case t.Say != "":
		return fmt.Sprintf("a player says %q", t.Say)
	case t.Enter:
		return "a player arrives"
	case t.Examine != "":
		return "a player examines " + t.Examine
	}
	s := strings.ToUpper(t.Verb) + " " + t.Target
	if t.With != "" {
		s += " with " + t.With
	}
	if t.Value != "" {
		s += " to " + t.Value
	}
	return s
}

func describeCondition(c Condition) string {
	switch name, _ := c.kind(); name {
	case "flag":
		return "flag " + c.Flag
	case "not_flag":
		return "not flag " + c.NotFlag
	case "world_flag":
		return "world flag " + c.WorldFlag
	case "has_item":
		return "has " + c.HasItem
	case "room_flag":
		return "room holds " + c.RoomFlag
	case "counter":
		return "counters " + describeIntMap(c.Counter, "=")
	case "counter_at_least":
		return "counters " + describeIntMap(c.CounterAtLeast, ">=")
	case "sequence":
		var parts []string
		for _, k := range sortedKeys(c.Sequence) {
			parts = append(parts, fmt.Sprintf("%s is [%s]", k, strings.Join(c.Sequence[k], ", ")))
		}
		return "sequence " + strings.Join(parts, " and ")
	case "sequence_longer_than":
		return "sequence " + describeIntMap(c.SequenceLongerThan, " longer than ")
	case "players_in_room_at_least":
		return fmt.Sprintf("at least %d players here", *c.PlayersInRoomAtLeast)
	}
	return "?"
}

func describeIntMap(m map[string]int, op string) string {
	var parts []string
	for _, k := range sortedKeys(m) {
		parts = append(parts, fmt.Sprintf("%s%s%d", k, op, m[k]))
	}
	return strings.Join(parts, ", ")
}

func describeEffect(e Effect) string {
	one := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	switch name, _ := e.kind(); name {
	case "say":
		return fmt.Sprintf("tells the player %q", one(*e.Say))
	case "room_say":
		return fmt.Sprintf("tells the room %q", one(*e.RoomSay))
	case "set_flag":
		return "sets flag " + *e.SetFlag
	case "clear_flag":
		return "clears flag " + *e.ClearFlag
	case "add":
		s := fmt.Sprintf("adds %d to %s", e.Add.By, e.Add.Counter)
		if len(e.Add.Wrap) == 2 {
			s += fmt.Sprintf(" (wraps %d to %d)", e.Add.Wrap[0], e.Add.Wrap[1])
		}
		return s
	case "set":
		return fmt.Sprintf("sets %s to %d", e.Set.Counter, e.Set.Value)
	case "append":
		return fmt.Sprintf("adds %q to sequence %s", e.Append.Value, e.Append.Sequence)
	case "reset":
		return "empties sequence " + e.Reset.Sequence
	case "give_item":
		return "gives " + *e.GiveItem
	case "take_item":
		return "takes " + *e.TakeItem
	case "open", "close":
		g := e.Open
		verb := "opens"
		if name == "close" {
			g, verb = e.Close, "closes"
		}
		s := verb + " gate " + g.Gate
		if g.Room != "" {
			s += " in " + g.Room
		}
		if g.For == "room" {
			s += " for everyone here"
		}
		return s
	case "hold":
		return fmt.Sprintf("holds %s for %d s", e.Hold.Key, e.Hold.Seconds)
	case "teleport":
		return "moves the player to " + *e.Teleport
	case "stop":
		return "stops"
	case "run":
		return "runs rule " + *e.Run
	}
	return "?"
}

// renderRules writes a room's gates and rules as bullet lists.
func renderRules(b *strings.Builder, w *World, key string) {
	r := w.Rooms[key]
	if len(r.Gates) > 0 {
		b.WriteString("**Gates:**\n\n")
		for _, gk := range sortedKeys(r.Gates) {
			g := r.Gates[gk]
			dest := "`" + g.Exit.To + "`"
			if d, ok := w.Rooms[g.Exit.To]; ok {
				dest = md(d.Name) + " (`" + g.Exit.To + "`)"
			}
			line := fmt.Sprintf("- `%s`: %s → %s, %s", gk, g.Exit.Dir, dest, g.Mode)
			if g.Mode == "locked" {
				line += fmt.Sprintf(" (%q)", strings.Join(strings.Fields(g.Message), " "))
			}
			if g.OpenWhen != "" {
				line += ", open for everyone while world flag `" + g.OpenWhen + "` is set"
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}
	if len(r.Rules) > 0 {
		b.WriteString("**Rules:**\n\n")
		for i, rule := range r.Rules {
			label := fmt.Sprintf("%d.", i+1)
			if rule.ID != "" {
				label += " `" + rule.ID + "`"
			}
			line := fmt.Sprintf("%s when %s", label, md(describeTrigger(rule.On)))
			var conds []string
			for _, c := range rule.If {
				conds = append(conds, describeCondition(c))
			}
			for _, c := range rule.Unless {
				conds = append(conds, "unless "+describeCondition(c))
			}
			if len(conds) > 0 {
				line += " and " + md(strings.Join(conds, " and "))
			}
			var effects []string
			for _, e := range rule.Do {
				effects = append(effects, describeEffect(e))
			}
			fmt.Fprintf(b, "%s: %s\n", line, md(strings.Join(effects, "; ")))
		}
		b.WriteString("\n")
	}
}

// gateEdges lists "from>to" gate links for the map, sorted.
func gateEdges(w *World) [][3]string {
	var out [][3]string
	for _, rk := range sortedKeys(w.Rooms) {
		for _, gk := range sortedKeys(w.Rooms[rk].Gates) {
			g := w.Rooms[rk].Gates[gk]
			if _, ok := w.Rooms[g.Exit.To]; ok {
				out = append(out, [3]string{rk, g.Exit.To, g.Exit.Dir})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0]+out[i][1] < out[j][0]+out[j][1] })
	return out
}
