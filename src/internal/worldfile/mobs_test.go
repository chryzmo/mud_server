package worldfile

import (
	"strings"
	"testing"
)

func TestMonsterTraitsAreCheckedAndRendered(t *testing.T) {
	npc := func(extra string) map[string]string {
		return map[string]string{"w.yaml": roomWith + "npcs:\n  rat: {race: RAT, hp: 5, combat_tick_ms: 1000, behavior: orc" + extra + "}\n"}
	}
	w, res, _ := ValidateDir(write(t, npc(", aggressive: true, xp: 8")))
	if len(res.Errors) != 0 || !w.NPCs["rat"].Aggressive || w.NPCs["rat"].XP != 8 {
		t.Fatalf("errors %v, npc %+v", res.Errors, w.NPCs["rat"])
	}
	if out := render(t, npc(", aggressive: true, xp: 8")); !strings.Contains(out, "| on sight | 8 |") {
		t.Errorf("overview is missing the traits\n%s", out)
	}
	if out := render(t, npc("")); !strings.Contains(out, "| when attacked | default |") {
		t.Errorf("a plain monster should read as fighting when attacked\n%s", out)
	}
	_, res, _ = ValidateDir(write(t, npc(", xp: 5000")))
	var all []string
	for _, p := range res.Errors {
		all = append(all, p.String())
	}
	if !strings.Contains(strings.Join(all, "\n"), "npcs.rat.xp") {
		t.Errorf("an xp of 5000 should be refused: %v", all)
	}
}

func TestUnlockEffectIsCheckedAndRendered(t *testing.T) {
	world := func(unlock string) map[string]string {
		return map[string]string{"w.yaml": `
start_room: a
rooms:
  a:
    name: A
    description: d
    features:
      plaque: {names: [plaque], examine: A plaque.}
    rules:
      - on: {verb: READ, target: plaque}
        do:
          - unlock: ` + unlock + `
          - say: Done.
`}
	}
	_, res, _ := ValidateDir(write(t, world("{badge: crown}")))
	if len(res.Errors) != 0 || len(res.Warnings) != 0 {
		t.Fatalf("a valid unlock gave errors %v warnings %v", res.Errors, res.Warnings)
	}
	if out := render(t, world("{color: gold}")); !strings.Contains(out, "## Cosmetic rewards") || !strings.Contains(out, "`a`: READ plaque grants the colour `gold`") {
		t.Errorf("overview is missing the reward\n%s", out)
	}
	if out := render(t, world("{badge: crown}")); !strings.Contains(out, "grants the badge `crown`") {
		t.Errorf("overview is missing the badge\n%s", out)
	}
	for name, unlock := range map[string]string{
		"both set":       "{color: gold, badge: crown}",
		"neither set":    "{}",
		"unknown colour": "{color: puce}",
		"unknown badge":  "{badge: sombrero}",
	} {
		_, res, _ := ValidateDir(write(t, world(unlock)))
		var all []string
		for _, p := range res.Errors {
			all = append(all, p.String())
		}
		if !strings.Contains(strings.Join(all, "\n"), "unlock") {
			t.Errorf("%s: no error about unlock in %v", name, all)
		}
	}
}
