package mudserver

import (
	"strings"
)

/*
USE <item> in a room where a rule takes that item. A puzzle that wants the item given, thrown, put somewhere
or used on a thing is written as a rule for the verb, the thing and the item (GIVE honeycomb TO Bubble). The
Use button in the pack sends only USE <item>, so when exactly one such rule applies here it runs as if the player
had typed the full command; when several do, the player is told what they could type instead.
*/

// useHere handles USE <item> for an item in the pack when rules in this room take it. It reports whether it
// handled the words, so the caller can fall back to the other uses. Loop only.
func (mp *MudPlayer) useHere(query string) bool {
	room := mp.Location
	if room == nil || len(room.Rules) == 0 {
		return false
	}
	hit, _ := mp.thing(query, true)
	if hit == nil || hit.key == "" {
		return false
	}

	type way struct{ verb, target string }
	var ways []way
	seen := map[way]bool{}
	for i := range room.Rules {
		rule := &room.Rules[i]
		t := rule.On
		if t == nil || t.Verb == "" || t.With != hit.key || t.Value != "" || !twoThingVerbs[strings.ToUpper(t.Verb)] {
			continue
		}
		w := way{strings.ToUpper(t.Verb), t.Target}
		if seen[w] {
			continue
		}
		rr := &ruleRun{p: mp, room: room}
		if !rr.conditionsHold(rule) {
			continue
		}
		seen[w] = true
		ways = append(ways, w)
	}

	switch len(ways) {
	case 0:
		return false
	case 1:
		return mp.act(ways[0].verb, ways[0].target, hit.key, "")
	}

	names := map[string]string{}
	for _, t := range mp.examineTargets(false) {
		if t.key != "" {
			names[t.key] = t.display
		}
	}
	prep := map[string]string{"GIVE": "TO", "THROW": "INTO", "PUT": "ON", "USE": "ON"}
	item := strings.ToUpper(hit.display)
	var options []string
	for _, w := range ways {
		target := names[w.target]
		if target == "" {
			target = w.target
		}
		options = append(options, w.verb+" "+item+" "+prep[w.verb]+" "+target)
	}
	mp.SendMessage("There is more than one thing to do with that here. Try: " + joinList(options, false) + ".")
	return true
}
