package worldfile

import (
	"strings"
	"testing"
)

const goodSpell = `spells:
  bolt: {name: Firebolt, effect: bolt, cost: 3, cooldown: 5, power: 4, description: Fire., cast: You hurl fire., room: "{player} hurls fire."}
`

func spellErrors(t *testing.T, yaml string) string {
	t.Helper()
	_, res, _ := ValidateDir(write(t, map[string]string{"w.yaml": roomWith + yaml}))
	var all []string
	for _, p := range res.Errors {
		all = append(all, p.String())
	}
	return strings.Join(all, "\n")
}

func TestSpellsAreChecked(t *testing.T) {
	if errs := spellErrors(t, goodSpell); errs != "" {
		t.Fatalf("a good spell is an error: %s", errs)
	}
	for yaml, want := range map[string]string{
		"spells:\n  bolt: {name: Firebolt, effect: zap, cost: 3, power: 4, description: D., cast: C.}\n":                                                                          `effect "zap" is not one of`,
		"spells:\n  bolt: {name: Firebolt, effect: bolt, cost: 99, power: 4, description: D., cast: C.}\n":                                                                        "cost is from 0 to 50",
		"spells:\n  bolt: {name: Firebolt, effect: bolt, cost: 3, power: 0, description: D., cast: C.}\n":                                                                         "needs a power above 0",
		"spells:\n  glow: {name: Glow, effect: fun, cost: 1, cooldown: 1, description: D., cast: C., room: R.}\n":                                                                 "waits at least 3 seconds",
		"spells:\n  glow: {name: Glow, effect: fun, cost: 1, cooldown: 5, description: D., cast: C.}\n":                                                                           "needs a room line",
		"spells:\n  bolt: {name: '', effect: bolt, cost: 3, power: 4, description: D., cast: C.}\n":                                                                               "name of 1 to 30 characters",
		"spells:\n  a: {name: Same, effect: bolt, cost: 3, power: 4, description: D., cast: C.}\n  b: {name: SAME, effect: bolt, cost: 3, power: 4, description: D., cast: C.}\n": "already used by spell",
		"spells:\n  bolt: {name: Firebolt, effect: bolt, cost: 3, power: 4, description: D.}\n":                                                                                   "needs cast text",
	} {
		if errs := spellErrors(t, yaml); !strings.Contains(errs, want) {
			t.Errorf("%q: want %q, got %q", yaml, want, errs)
		}
	}
}

func TestScrollsTeachEffectsAndConditionsPointAtRealSpells(t *testing.T) {
	scroll := func(extra string) string {
		return goodSpell + "items:\n  s: {name: SCROLL, description: A scroll., " + extra + "}\n"
	}
	if errs := spellErrors(t, scroll("teaches: bolt, bound: true")); strings.Contains(errs, "teaches") {
		t.Errorf("a good scroll is refused: %s", errs)
	}
	for extra, want := range map[string]string{
		"teaches: nothing, bound: true": `"nothing" is not a spell`,
		"teaches: bolt":                 "must be bound",
	} {
		if errs := spellErrors(t, scroll(extra)); !strings.Contains(errs, want) {
			t.Errorf("scroll %s: want %q, got %q", extra, want, errs)
		}
	}
	rule := "rooms:\n  hall:\n    name: Hall\n    description: A hall.\n    features:\n      desk: {names: [desk], examine: A desk.}\n    rules:\n      - on: {verb: PULL, target: desk}\n        if: [{knows_spell: ghost}]\n        do: [{teach: phantom}]\n"
	_, res, _ := ValidateDir(write(t, map[string]string{"w.yaml": "start_room: hall\n" + rule + goodSpell}))
	var all []string
	for _, p := range res.Errors {
		all = append(all, p.String())
	}
	errs := strings.Join(all, "\n")
	for _, want := range []string{`"ghost" is not a spell`, `"phantom" is not a spell`} {
		if !strings.Contains(errs, want) {
			t.Errorf("want %q in %s", want, errs)
		}
	}
}

func TestSpellsAreRenderedWithHowTheyAreLearned(t *testing.T) {
	out := render(t, map[string]string{"w.yaml": roomWith + goodSpell + "items:\n  s: {name: SCROLL, description: A scroll., teaches: bolt, bound: true}\nnpcs:\n  rat: {race: RAT, hp: 5, combat_tick_ms: 1000, behavior: orc, drops: [{item: s, chance: 10}]}\n"})
	if !strings.Contains(out, "## Spells") || !strings.Contains(out, "scroll \\`s\\`") || !strings.Contains(out, "RAT drops it (10%)") {
		t.Errorf("overview lacks the spell table\n%s", out)
	}
}
