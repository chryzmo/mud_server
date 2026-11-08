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

func TestDropsAreRenderedForReview(t *testing.T) {
	out := render(t, map[string]string{"w.yaml": roomWith + "items:\n  charm: {name: CHARM, description: A charm.}\nnpcs:\n  rat: {race: RAT, hp: 5, combat_tick_ms: 1000, behavior: orc, drops: [{item: charm, chance: 25}, {badge: flame, chance: 100}]}\n"})
	if !strings.Contains(out, "**Drops from RAT:** charm (25%), flame badge (100%)") {
		t.Errorf("overview is missing the drops\n%s", out)
	}
}

func TestShopsAreCheckedAndRendered(t *testing.T) {
	world := func(shop string) map[string]string {
		return map[string]string{"w.yaml": `start_room: hall
rooms:
  hall:
    name: Hall
    description: A hall.
    people:
      smith:
        names: [Smith]
        examine: A smith.
        greeting: Hello.
        shop: ` + shop + `
items:
  dagger: {name: DAGGER, description: A dagger., equipment: {slot: weapon, attack: 2}}
  token: {name: TOKEN, description: A token., bound: true}
`}
	}
	good := render(t, world("[{item: dagger, price: 7}]"))
	if !strings.Contains(good, "sells: dagger (7 gold)") {
		t.Errorf("overview lacks the wares\n%s", good)
	}
	for shop, want := range map[string]string{
		"[{item: nothing, price: 5}]":                          `"nothing" is not an item`,
		"[{item: token, price: 5}]":                            "puzzle token and cannot be sold",
		"[{item: dagger, price: 5}, {item: dagger, price: 6}]": "listed twice",
		"[{item: dagger, price: 0}]":                           "price must be from 1 to 10000",
		"[{item: dagger, price: 10001}]":                       "price must be from 1 to 10000",
	} {
		_, res, _ := ValidateDir(write(t, world(shop)))
		var all []string
		for _, p := range res.Errors {
			all = append(all, p.String())
		}
		if !strings.Contains(strings.Join(all, "\n"), want) {
			t.Errorf("shop %s: want %q, got %v", shop, want, all)
		}
	}
}

func TestConsumablesAreChecked(t *testing.T) {
	world := func(item string) map[string]string {
		return map[string]string{"w.yaml": roomWith + "items:\n  potion: " + item + "\n"}
	}
	if _, res, _ := ValidateDir(write(t, world("{name: POTION, description: Red., consume: {hp: 20, mp: 5}}"))); len(res.Errors) != 0 {
		t.Errorf("a good potion is an error: %v", res.Errors)
	}
	for item, want := range map[string]string{
		"{name: POTION, description: Red., consume: {}}":                                            "at least one above zero",
		"{name: POTION, description: Red., consume: {hp: -1}}":                                      "from 0 to 1000",
		"{name: POTION, description: Red., consume: {hp: 5000}}":                                    "from 0 to 1000",
		"{name: POTION, description: Red., consume: {hp: 5}, equipment: {slot: weapon, attack: 1}}": "worn or used up, not both",
		"{name: POTION, description: Red., bound: true, consume: {hp: 5}}":                          "puzzle token cannot be used up",
	} {
		_, res, _ := ValidateDir(write(t, world(item)))
		var all []string
		for _, p := range res.Errors {
			all = append(all, p.String())
		}
		if !strings.Contains(strings.Join(all, "\n"), want) {
			t.Errorf("item %s: want %q, got %v", item, want, all)
		}
	}
	out := render(t, world("{name: POTION, description: Red., consume: {hp: 20}}"))
	if !strings.Contains(out, "(used up)") || !strings.Contains(out, "restores 20 hp") {
		t.Errorf("the overview lacks the potion\n%s", out)
	}
}
