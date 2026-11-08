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
