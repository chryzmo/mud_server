package worldfile

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Render writes a readable overview of the world as Markdown: a map (a Mermaid
// flowchart, which GitHub draws), every room with its description, exits, floor items
// and spawners, then the items and NPCs, so a reviewer can walk the world without
// reading YAML. Problems the validator found are listed first.
func Render(w *World, res *Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# World overview\n\n%d rooms, %d items, %d npcs", len(w.Rooms), len(w.Items), len(w.NPCs))
	if start, ok := w.Rooms[w.StartRoom]; ok {
		fmt.Fprintf(&b, ". New players start in **%s** (`%s`).", md(start.Name), w.StartRoom)
	}
	b.WriteString("\n\n")

	if res != nil && (len(res.Errors) > 0 || len(res.Warnings) > 0) {
		b.WriteString("## Problems\n\n")
		for _, p := range res.Errors {
			fmt.Fprintf(&b, "- **error** %s\n", md(p.String()))
		}
		for _, p := range res.Warnings {
			fmt.Fprintf(&b, "- warning %s\n", md(p.String()))
		}
		b.WriteString("\n")
	}

	order, dist := readingOrder(w)
	areaOf := map[string]string{}
	for _, name := range sortedKeys(w.Areas) {
		for _, rk := range w.Areas[name].Rooms {
			areaOf[rk] = name
		}
	}

	b.WriteString("## Map\n\n")
	b.WriteString(mermaid(w, order, areaOf))
	b.WriteString("\n")

	b.WriteString("## Rooms\n\n")
	for _, key := range order {
		r := w.Rooms[key]
		fmt.Fprintf(&b, "### %s\n\n", md(r.Name))
		meta := []string{"`" + key + "`"}
		if key == w.StartRoom {
			meta = append(meta, "start room")
		}
		if a, ok := areaOf[key]; ok {
			meta = append(meta, "area "+a)
		}
		if d, ok := dist[key]; ok {
			meta = append(meta, stepsText(d))
		} else {
			meta = append(meta, "**not reachable from the start**")
		}
		b.WriteString(strings.Join(meta, " · ") + "\n\n")
		for _, line := range strings.Split(strings.TrimSpace(r.Description), "\n") {
			fmt.Fprintf(&b, "> %s\n", md(strings.TrimRight(line, " ")))
		}
		b.WriteString("\n")
		if len(r.Exits) > 0 {
			var parts []string
			for _, dir := range Directions {
				if to, ok := r.Exits[dir]; ok {
					name := to
					if dest, ok := w.Rooms[to]; ok {
						name = dest.Name
					}
					parts = append(parts, fmt.Sprintf("%s → %s (`%s`)", dir, md(name), to))
				}
			}
			fmt.Fprintf(&b, "**Exits:** %s\n\n", strings.Join(parts, "; "))
		} else {
			b.WriteString("**Exits:** none\n\n")
		}
		if len(r.Features) > 0 {
			b.WriteString("**To examine:**\n\n")
			for _, fk := range sortedKeys(r.Features) {
				ft := r.Features[fk]
				label := strings.Join(ft.Names, " / ")
				if ft.Unlisted {
					label += " (not listed)"
				}
				fmt.Fprintf(&b, "- *%s*: %s\n", md(label), md(strings.Join(strings.Fields(ft.Examine), " ")))
			}
			b.WriteString("\n")
		}
		if len(r.People) > 0 {
			b.WriteString("**People:**\n\n")
			for _, pk := range sortedKeys(r.People) {
				pe := r.People[pk]
				fmt.Fprintf(&b, "- **%s** (`%s`): %s\n", md(strings.Join(pe.Names, " / ")), pk, md(strings.Join(strings.Fields(pe.Examine), " ")))
				fmt.Fprintf(&b, "  - greets: %s\n", md(strings.Join(strings.Fields(pe.Greeting), " ")))
				for _, tk := range sortedKeys(pe.Topics) {
					t := pe.Topics[tk]
					fmt.Fprintf(&b, "  - ask about *%s*: %s\n", md(strings.Join(t.Keywords, " / ")), md(strings.Join(strings.Fields(t.Say), " ")))
				}
				if strings.TrimSpace(pe.Shrug) != "" {
					fmt.Fprintf(&b, "  - otherwise: %s\n", md(strings.Join(strings.Fields(pe.Shrug), " ")))
				}
			}
			b.WriteString("\n")
		}
		if len(r.Items) > 0 {
			var parts []string
			for _, ik := range sortedKeys(r.Items) {
				name := ik
				if it, ok := w.Items[ik]; ok {
					name = it.Name
				}
				parts = append(parts, fmt.Sprintf("%s ×%d", md(name), r.Items[ik]))
			}
			fmt.Fprintf(&b, "**On the floor:** %s\n\n", strings.Join(parts, ", "))
		}
		for _, s := range r.Spawners {
			name := s.NPC
			if n, ok := w.NPCs[s.NPC]; ok {
				name = n.Race
			}
			fmt.Fprintf(&b, "**Spawns:** %s, up to %d, one every %s\n\n", md(name), s.Count, seconds(s.TickMS))
		}
	}

	if len(w.Items) > 0 {
		b.WriteString("## Items\n\n| Item | Key | Worn on | Bonuses | Description |\n|---|---|---|---|---|\n")
		for _, key := range sortedKeys(w.Items) {
			it := w.Items[key]
			slot, bonus := "", ""
			if e := it.Equipment; e != nil {
				slot = e.Slot
				var parts []string
				for _, p := range []struct {
					name string
					v    int
				}{{"attack", e.Attack}, {"defense", e.Defense}, {"max hp", e.MaxHP}, {"hp", e.HP}, {"max mp", e.MaxMP}, {"mp", e.MP}} {
					if p.v != 0 {
						parts = append(parts, fmt.Sprintf("%s %+d", p.name, p.v))
					}
				}
				bonus = strings.Join(parts, ", ")
			}
			fmt.Fprintf(&b, "| %s | `%s` | %s | %s | %s |\n", cell(it.Name), key, slot, bonus, cell(it.Description))
		}
		b.WriteString("\n")
	}
	if len(w.NPCs) > 0 {
		b.WriteString("## NPCs\n\n| Race | Key | HP | Attack | Defense | Accuracy | Acts every | Gold |\n|---|---|---|---|---|---|---|---|\n")
		for _, key := range sortedKeys(w.NPCs) {
			n := w.NPCs[key]
			fmt.Fprintf(&b, "| %s | `%s` | %d | %d | %d | %d | %s | %d-%d |\n", cell(n.Race), key, n.HP, n.Attack, n.Defense, n.Accuracy, seconds(n.CombatTickMS), n.LootGoldMin, n.LootGoldMax)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// readingOrder lists room keys by distance from the start (breadth first, keys sorted
// within a distance), with the rooms that cannot be reached last.
func readingOrder(w *World) ([]string, map[string]int) {
	dist := map[string]int{}
	var order []string
	if _, ok := w.Rooms[w.StartRoom]; ok {
		dist[w.StartRoom] = 0
		level := []string{w.StartRoom}
		for len(level) > 0 {
			sort.Strings(level)
			order = append(order, level...)
			var next []string
			for _, cur := range level {
				for _, to := range w.Rooms[cur].Exits {
					if _, ok := w.Rooms[to]; !ok {
						continue
					}
					if _, seen := dist[to]; !seen {
						dist[to] = dist[cur] + 1
						next = append(next, to)
					}
				}
			}
			level = next
		}
	}
	for _, key := range sortedKeys(w.Rooms) {
		if _, ok := dist[key]; !ok {
			order = append(order, key)
		}
	}
	return order, dist
}

// mermaid draws the rooms and exits. A pair of exits that lead back to each other is
// one line labelled with both directions; areas are boxes around their rooms.
func mermaid(w *World, order []string, areaOf map[string]string) string {
	id := map[string]string{}
	for i, key := range order {
		id[key] = fmt.Sprintf("r%d", i)
	}
	var b strings.Builder
	b.WriteString("```mermaid\nflowchart TD\n")
	node := func(key string) string {
		label := w.Rooms[key].Name
		if key == w.StartRoom {
			label += " (start)"
		}
		return fmt.Sprintf("%s[\"%s\"]", id[key], mermaidText(label))
	}
	inArea := map[string][]string{}
	for _, key := range order {
		if a, ok := areaOf[key]; ok {
			inArea[a] = append(inArea[a], key)
		}
	}
	for _, key := range order {
		if _, ok := areaOf[key]; !ok {
			fmt.Fprintf(&b, "    %s\n", node(key))
		}
	}
	for _, name := range sortedKeys(inArea) {
		fmt.Fprintf(&b, "    subgraph a_%s[\"area %s\"]\n", strings.ReplaceAll(name, "-", "_"), mermaidText(name))
		for _, key := range inArea[name] {
			fmt.Fprintf(&b, "        %s\n", node(key))
		}
		b.WriteString("    end\n")
	}
	drawn := map[string]bool{}
	for _, key := range order {
		for _, dir := range Directions {
			to, ok := w.Rooms[key].Exits[dir]
			if !ok {
				continue
			}
			if _, exists := w.Rooms[to]; !exists {
				continue // the validator reports it; there is nothing to draw
			}
			if drawn[key+">"+to+dir] {
				continue
			}
			if back, twoWay := w.Rooms[to].Exits[opposite[dir]]; twoWay && back == key && to != key {
				drawn[to+">"+key+opposite[dir]] = true
				fmt.Fprintf(&b, "    %s <-->|%s/%s| %s\n", id[key], dir, opposite[dir], id[to])
			} else {
				fmt.Fprintf(&b, "    %s -->|%s| %s\n", id[key], dir, id[to])
			}
		}
	}
	b.WriteString("```\n")
	return b.String()
}

func mermaidText(s string) string {
	s = strings.ReplaceAll(s, "&", "#amp;")
	s = strings.ReplaceAll(s, "\"", "#quot;")
	s = strings.ReplaceAll(s, "<", "#lt;")
	s = strings.ReplaceAll(s, ">", "#gt;")
	return s
}

// md escapes what would turn plain text into Markdown structure.
func md(s string) string {
	r := strings.NewReplacer("\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_", "<", "&lt;", ">", "&gt;", "|", "\\|")
	return r.Replace(s)
}

// cell makes text safe inside a table cell.
func cell(s string) string {
	return md(strings.Join(strings.Fields(s), " "))
}

func stepsText(d int) string {
	if d == 1 {
		return "1 step from the start"
	}
	return fmt.Sprintf("%d steps from the start", d)
}

func seconds(ms int) string {
	if ms%1000 == 0 {
		return fmt.Sprintf("%d s", ms/1000)
	}
	return fmt.Sprintf("%.2f s", float64(ms)/1000)
}

// RunRender is the -render-world command: it prints the Markdown overview of a
// content directory (problems included) and returns the process exit code (0, or 2
// for usage or an unreadable directory). It does not fail on content errors; use
// -validate-world for that.
func RunRender(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(stderr, "usage: mud_server -render-world DIR")
		return 2
	}
	w, res, err := ValidateDir(args[0])
	if err != nil {
		fmt.Fprintln(stderr, "render-world:", err)
		return 2
	}
	io.WriteString(stdout, Render(w, res))
	return 0
}
