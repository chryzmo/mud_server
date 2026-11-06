package worldfile

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func render(t *testing.T, files map[string]string) string {
	t.Helper()
	w, res, err := ValidateDir(write(t, files))
	if err != nil {
		t.Fatal(err)
	}
	return Render(w, res)
}

func TestRenderTheSampleWorld(t *testing.T) {
	w, res, err := ValidateDir(filepath.Join("..", "..", "..", "world-sample"))
	if err != nil {
		t.Fatal(err)
	}
	out := Render(w, res)
	for _, want := range []string{
		"# World overview",
		"3 rooms, 10 items, 1 npcs. New players start in **Start Room** (`start-room`).",
		"```mermaid\nflowchart TD",
		`r0["Start Room (start)"]`,
		`subgraph a_start["area start"]`,
		"r0 <-->|N/S| r1",
		"r1 <-->|E/W| r2",
		"### Start Room",
		"`start-room` · start room · area start · 0 steps from the start",
		"> You find yourself in a generic looking room.  There is a door to the North.",
		"**Exits:** N → Balcony (`balcony`)",
		"**To examine:**\n\n- *wooden sign / sign / notice*: A hand-painted sign nailed beside the door",
		"- *arena sand / sand (not listed)*: The sand is raked into a pattern",
		"**On the floor:** KEY ×1",
		"**Spawns:** ORC, up to 1, one every 5 s",
		"| IRON SWORD | `iron-sword` | weapon | attack +5 |",
		"| ORC | `orc` | 5 | 0 | 0 | 0 | 5 s | 0-5 |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("overview is missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "## Problems") {
		t.Errorf("a clean world has no problems section")
	}
	// A two-way exit is drawn once, not twice.
	if n := strings.Count(out, "<-->"); n != 2 {
		t.Errorf("two-way edges = %d, want 2", n)
	}
}

func TestRenderListsRoomsByDistanceAndFlagsTheUnreachable(t *testing.T) {
	out := render(t, map[string]string{"w.yaml": `
start_room: hall
rooms:
  attic: {name: Attic, description: Nobody comes here.}
  yard:
    name: Yard
    description: A yard.
    exits: {S: hall, E: shed}
  shed:
    name: Shed
    description: A shed.
    exits: {W: yard}
  hall:
    name: Hall
    description: A hall.
    exits: {N: yard}
`})
	pos := func(s string) int { return strings.Index(out, s) }
	if !(pos("### Hall") < pos("### Yard") && pos("### Yard") < pos("### Shed") && pos("### Shed") < pos("### Attic")) {
		t.Errorf("rooms are not in reading order:\n%s", out)
	}
	if !strings.Contains(out, "`yard` · 1 step from the start") || !strings.Contains(out, "`shed` · 2 steps from the start") {
		t.Errorf("distances are not worded right:\n%s", out)
	}
	if !strings.Contains(out, "`attic` · **not reachable from the start**") {
		t.Errorf("the unreachable room is not flagged:\n%s", out)
	}
	if !strings.Contains(out, "## Problems") || !strings.Contains(out, "cannot be reached from the start room") {
		t.Errorf("the validator's warning is not listed:\n%s", out)
	}
}

func TestRenderKeepsAOneWayExitAsAnArrow(t *testing.T) {
	out := render(t, map[string]string{"w.yaml": `
start_room: top
rooms:
  top: {name: Top, description: A ledge., exits: {D: pit}}
  pit: {name: Pit, description: A pit.}
`})
	if !strings.Contains(out, "r0 -->|D| r1") {
		t.Errorf("a one-way exit should be an arrow:\n%s", out)
	}
}

func TestRenderEscapesWhatWouldBreakMarkdownAndMermaid(t *testing.T) {
	out := render(t, map[string]string{"w.yaml": `
start_room: odd
rooms:
  odd:
    name: 'The "Odd" <Room> | a_b'
    description: |
      Line one has *stars* and a pipe | here.
      Line two.
items:
  thing:
    name: THING
    description: 'A | pipe
      and a second line.'
`})
	for _, want := range []string{
		`r0["The #quot;Odd#quot; #lt;Room#gt; | a_b (start)"]`, // inside quotes a pipe is safe; quotes and angle brackets are entities
		`### The "Odd" &lt;Room&gt; \| a\_b`,
		"> Line one has \\*stars\\* and a pipe \\| here.\n> Line two.",
		"| THING | `thing` |  |  | A \\| pipe and a second line. |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestRunRenderCommand(t *testing.T) {
	good := write(t, map[string]string{"w.yaml": twoRooms})
	var out, errOut bytes.Buffer
	if code := RunRender([]string{good}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "# World overview") {
		t.Errorf("render: %d %q %q", code, out.String(), errOut.String())
	}
	// Content errors do not stop the overview; they are listed in it.
	broken := write(t, map[string]string{"w.yaml": strings.Replace(twoRooms, "{N: yard}", "{N: pit}", 1)})
	out.Reset()
	if code := RunRender([]string{broken}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "**error**") {
		t.Errorf("render of broken content: %d %q", code, out.String())
	}
	for _, args := range [][]string{nil, {"a", "b"}, {"-x"}, {filepath.Join(good, "missing")}} {
		if code := RunRender(args, &out, &errOut); code != 2 {
			t.Errorf("RunRender(%v) = %d, want 2", args, code)
		}
	}
}
