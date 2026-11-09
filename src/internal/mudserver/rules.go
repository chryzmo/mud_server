package mudserver

import (
	"regexp"
	"sort"
	"strings"
	"time"

	muddata "mud_server/src/internal/muddata"
	"mud_server/src/internal/mudutils"
	"mud_server/src/internal/worldfile"
)

/*
Rules, gates and puzzle verbs (plans/interactive-world.md sections 1, 3 and 5). A room's content
has rules: "when this happens, if these things are true, do these things". The player's verbs
(TURN, PULL, USE, GIVE, ...) resolve what they name and look for the first rule in the room that
matches; SAY and arriving can trigger rules too. Effects change the player's own state (flags,
counters, sequences, opened gates, items) or the room (a held plate). Everything runs on the
game loop, one command at a time, with a fixed set of conditions and effects; the content
validator has already proved every reference.
*/

// gateLink is a gate as the running room sees it: an exit that exists only for players it was opened for.
type gateLink struct {
	Key     string
	Dir     string
	ToKey   string
	To      *Room
	Mode    string // "hidden" or "locked"
	Message string
	OpenWhen string // a world flag that opens it for everyone while set
}

// id is how a player's state names this gate: its room's key and its own.
func (g *gateLink) id(r *Room) string { return r.Key + "/" + g.Key }

// usable reports whether the gate is open for this player: they opened it, or the world flag it waits for is set.
func (mp *MudPlayer) gateUsable(g *gateLink, r *Room) bool {
	return mp.gateOpen(g.id(r)) || (g.OpenWhen != "" && mp.world.worldFlags[g.OpenWhen])
}

// gateFor is the gate in direction dir, if the room has one.
func (r *Room) gateFor(dir string) *gateLink {
	for _, g := range r.Gates {
		if g.Dir == dir {
			return g
		}
	}
	return nil
}

// retRoomLocked is MoveRoom's answer when a sealed gate refused the player (the message was sent).
const retRoomLocked = 1001

// exitFor resolves a direction for this player: an ordinary exit, or a gate they have opened. For a
// sealed gate it says why not and returns (nil, true).
func (mp *MudPlayer) exitFor(dir string) (room *Room, refused bool) {
	r := mp.Location
	if next := r.ExitMap[dir]; next != nil {
		return next, false
	}
	g := r.gateFor(dir)
	if g == nil || g.To == nil {
		return nil, false
	}
	if mp.gateUsable(g, r) {
		return g.To, false
	}
	if g.Mode == "locked" {
		mp.SendMessage(g.Message)
		return nil, true
	}
	return nil, false // a hidden gate is not there
}

// usableExits are the directions this player can leave by, sorted: ordinary exits and gates opened for them.
func (mp *MudPlayer) usableExits() []string {
	r := mp.Location
	var out []string
	for dir := range r.ExitMap {
		out = append(out, dir)
	}
	for _, g := range r.Gates {
		if g.To != nil && mp.gateUsable(g, r) {
			if _, plain := r.ExitMap[g.Dir]; !plain {
				out = append(out, g.Dir)
			}
		}
	}
	sort.Strings(out)
	return out
}

// sealedExits are the locked gates this player has not opened, shown in LOOK so they know to try.
func (mp *MudPlayer) sealedExits() []string {
	r := mp.Location
	var out []string
	for _, g := range r.Gates {
		if g.Mode == "locked" && g.To != nil && !mp.gateUsable(g, r) {
			out = append(out, g.Dir)
		}
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------------------------
// Rules

// ruleRun is one execution of a rule's effects.
type ruleRun struct {
	p       *MudPlayer
	room    *Room
	stopped bool
	moved   bool // a teleport ended the rule's business in this room
	exits   bool // a gate changed for someone in this room: send fresh exits
}

// conditionsHold: every If holds and no Unless does.
func (rr *ruleRun) conditionsHold(rule *worldfile.Rule) bool {
	for _, c := range rule.If {
		if !rr.holdsOne(c) {
			return false
		}
	}
	for _, c := range rule.Unless {
		if rr.holdsOne(c) {
			return false
		}
	}
	return true
}

func (rr *ruleRun) holdsOne(c worldfile.Condition) bool {
	p := rr.p
	switch {
	case c.Flag != "":
		return p.flag(c.Flag)
	case c.NotFlag != "":
		return !p.flag(c.NotFlag)
	case c.HasItem != "":
		return p.hasItemKey(c.HasItem)
	case c.KnowsSpell != "":
		return p.knowsSpell(c.KnowsSpell)
	case c.WorldFlag != "":
		return p.world.worldFlags[c.WorldFlag]
	case c.RoomFlag != "":
		return rr.room.holds[c.RoomFlag].After(time.Now())
	case len(c.Counter) > 0:
		for k, v := range c.Counter {
			if p.counter(k) != v {
				return false
			}
		}
		return true
	case len(c.CounterAtLeast) > 0:
		for k, v := range c.CounterAtLeast {
			if p.counter(k) < v {
				return false
			}
		}
		return true
	case len(c.Sequence) > 0:
		for k, want := range c.Sequence {
			got := p.sequence(k)
			if len(got) != len(want) {
				return false
			}
			for i := range want {
				if !strings.EqualFold(got[i], want[i]) {
					return false
				}
			}
		}
		return true
	case len(c.SequenceLongerThan) > 0:
		for k, n := range c.SequenceLongerThan {
			if len(p.sequence(k)) <= n {
				return false
			}
		}
		return true
	case c.PlayersInRoomAtLeast != nil:
		return len(rr.room.UserMap) >= *c.PlayersInRoomAtLeast
	}
	return false
}

// hasItemKey reports whether the player carries the item with this content key.
func (mp *MudPlayer) hasItemKey(key string) bool {
	if mp.world == nil {
		return false
	}
	it := mp.world.itemsByKey[key]
	return it != nil && mp.Inventory.GetItem(it.Name) != nil
}

// runEffects does a rule's effects in order, stopping at `stop`.
func (rr *ruleRun) runEffects(rule *worldfile.Rule, depth int) {
	for _, e := range rule.Do {
		if rr.stopped || rr.moved {
			return
		}
		rr.effect(e, depth)
	}
}

func (rr *ruleRun) effect(e worldfile.Effect, depth int) {
	p, room := rr.p, rr.room
	switch {
	case e.Say != nil:
		p.SendMessage(p.fill(*e.Say))
	case e.RoomSay != nil:
		for _, other := range room.Players() {
			other.SendMessage(strings.ReplaceAll(other.fillNamed(*e.RoomSay, p), "{player}", p.Username))
		}
	case e.SetFlag != nil:
		p.setFlag(*e.SetFlag)
	case e.ClearFlag != nil:
		p.clearFlag(*e.ClearFlag)
	case e.Add != nil:
		v := p.counter(e.Add.Counter) + e.Add.By
		if w := e.Add.Wrap; len(w) == 2 {
			span := w[1] - w[0] + 1
			v = w[0] + ((v-w[0])%span+span)%span
		}
		p.setCounter(e.Add.Counter, v)
	case e.Set != nil:
		p.setCounter(e.Set.Counter, e.Set.Value)
	case e.Append != nil:
		p.appendSequence(e.Append.Sequence, e.Append.Value)
	case e.Reset != nil:
		p.resetSequence(e.Reset.Sequence)
	case e.Unlock != nil:
		if e.Unlock.Color != "" {
			p.grantCosmetic("color", e.Unlock.Color)
		} else {
			p.grantCosmetic("badge", e.Unlock.Badge)
		}
	case e.Teach != nil:
		p.learnSpell(*e.Teach)
	case e.TeachRoom != nil:
		for _, other := range room.Players() {
			other.learnSpell(*e.TeachRoom)
		}
	case e.GiveItem != nil:
		p.giveItemKey(*e.GiveItem)
	case e.TakeItem != nil:
		p.takeItemKey(*e.TakeItem)
	case e.Open != nil:
		rr.gate(e.Open, true)
	case e.Close != nil:
		rr.gate(e.Close, false)
	case e.Hold != nil:
		if room.holds == nil {
			room.holds = map[string]time.Time{}
		}
		room.holds[e.Hold.Key] = time.Now().Add(time.Duration(e.Hold.Seconds) * time.Second)
	case e.Teleport != nil:
		if dest := p.world.roomsByKey[*e.Teleport]; dest != nil {
			if placed, ok := p.world.placeNear(dest.ID, -1); ok {
				relocatePlayer(p, placed)
				rr.moved = true
			}
		}
	case e.Stop != nil:
		rr.stopped = true
	case e.Run != nil:
		if depth >= worldfile.MaxRunDepth {
			return
		}
		for i := range room.Rules {
			if room.Rules[i].ID == *e.Run && room.Rules[i].On == nil {
				if rr.conditionsHold(&room.Rules[i]) {
					inner := *rr
					inner.stopped = false
					inner.runEffects(&room.Rules[i], depth+1)
					rr.moved, rr.exits = inner.moved, rr.exits || inner.exits
				}
				return
			}
		}
	}
}

// gate opens or closes a gate for the player, or for everyone in the room now.
func (rr *ruleRun) gate(g *worldfile.GateEffect, open bool) {
	roomKey := g.Room
	if roomKey == "" {
		roomKey = rr.room.Key
	}
	id := roomKey + "/" + g.Gate
	targets := []*MudPlayer{rr.p}
	if g.For == "room" {
		targets = rr.room.Players()
	}
	for _, t := range targets {
		t.setGate(id, open)
	}
	rr.exits = rr.exits || roomKey == rr.room.Key
}

// fillNamed is fill with another player's counters left out: room text is only about the actor.
func (mp *MudPlayer) fillNamed(text string, actor *MudPlayer) string { return actor.fill(text) }

func (mp *MudPlayer) giveItemKey(key string) {
	tpl := mp.world.itemsByKey[key]
	if tpl == nil {
		return
	}
	if tpl.Bound && mp.Inventory.GetItem(tpl.Name) != nil {
		return // a puzzle token is never stacked: one is all anyone needs
	}
	c := tpl.createCopy()
	c.Quantity = 1
	mp.Inventory.AddItem(c)
	mp.dirty = true
	mp.SendMessage("You receive " + mudutils.ColorString(tpl.Name, mudutils.COLOR_GREEN) + ".")
}

func (mp *MudPlayer) takeItemKey(key string) {
	tpl := mp.world.itemsByKey[key]
	if tpl == nil {
		return
	}
	if it := mp.Inventory.GetItem(tpl.Name); it != nil {
		mp.Inventory.RemoveItem(it)
		mp.dirty = true
	}
}

// finishRules sends fresh exits to the players whose gates just changed.
func (rr *ruleRun) finish() {
	if rr.exits && !rr.moved {
		for _, t := range rr.room.Players() {
			t.sendExits()
		}
	}
}

// sendExits sends the room frame again, with this player's exits.
func (mp *MudPlayer) sendExits() {
	mp.sendRoomFrame()
}

// act runs the first rule in the player's room that matches a verb on a target, with an
// optional second thing or value, and whose conditions hold. It reports whether one ran.
func (mp *MudPlayer) act(verb, target, with, value string) bool {
	room := mp.Location
	for i := range room.Rules {
		rule := &room.Rules[i]
		t := rule.On
		if t == nil || t.Verb == "" || !strings.EqualFold(t.Verb, verb) || t.Target != target || t.With != with || !strings.EqualFold(t.Value, value) {
			continue
		}
		rr := &ruleRun{p: mp, room: room}
		if !rr.conditionsHold(rule) {
			continue
		}
		rr.runEffects(rule, 0)
		rr.finish()
		return true
	}
	return false
}

var sayTrim = regexp.MustCompile(`[\s.!?,;:"']+`)

func normalizeWords(s string) string {
	return strings.ToLower(strings.TrimSpace(sayTrim.ReplaceAllString(" "+s+" ", " ")))
}

// said runs the first rule listening for these words in the player's room. Loop only.
func (mp *MudPlayer) said(words string) {
	room := mp.Location
	if room == nil || len(room.Rules) == 0 {
		return
	}
	heard := normalizeWords(words)
	for i := range room.Rules {
		rule := &room.Rules[i]
		if rule.On == nil || rule.On.Say == "" || normalizeWords(rule.On.Say) != heard {
			continue
		}
		rr := &ruleRun{p: mp, room: room}
		if !rr.conditionsHold(rule) {
			continue
		}
		rr.runEffects(rule, 0)
		rr.finish()
		return
	}
}

// entered runs every rule for arriving in the room whose conditions hold (they set flags so they run once).
func (mp *MudPlayer) entered() {
	room := mp.Location
	if room == nil {
		return
	}
	mp.visit(room.Key)
	if len(room.Rules) == 0 {
		return
	}
	for i := range room.Rules {
		rule := &room.Rules[i]
		if rule.On == nil || !rule.On.Enter {
			continue
		}
		rr := &ruleRun{p: mp, room: room}
		if !rr.conditionsHold(rule) {
			continue
		}
		rr.runEffects(rule, 0)
		rr.finish()
		if rr.moved {
			return
		}
	}
}

// ---------------------------------------------------------------------------------------------
// The verbs

var (
	twoThingVerbs = map[string]bool{"USE": true, "GIVE": true, "PUT": true, "THROW": true}
	prepRE        = regexp.MustCompile(`(?i)\s+(?:onto|into|with|on|in|to|at)\s+`)
	toValueRE     = regexp.MustCompile(`(?i)\s+to\s+`)
)

var verbHelp = map[string]string{
	"TURN": "Turn something.  Usage: TURN <thing>", "PULL": "Pull something.  Usage: PULL <thing>",
	"PUSH": "Push something.  Usage: PUSH <thing>", "PRESS": "Press something.  Usage: PRESS <thing>",
	"RING": "Ring something.  Usage: RING <thing>", "LIGHT": "Light something.  Usage: LIGHT <thing>",
	"OPEN": "Open something.  Usage: OPEN <thing>", "SEARCH": "Search something carefully.  Usage: SEARCH <thing>",
	"READ": "Read something.  Usage: READ <thing>", "USE": "Use a thing, or one thing on another.  Usage: USE <thing> [ON <thing>]",
	"GIVE": "Give an item to someone.  Usage: GIVE <item> TO <person>", "PUT": "Put an item somewhere.  Usage: PUT <item> IN <thing>",
	"THROW": "Throw an item at or into something.  Usage: THROW <item> INTO <thing>", "SET": "Set something to a value.  Usage: SET <thing> TO <value>",
}

// registerRuleVerbs adds the puzzle verbs to the command table.
func registerRuleVerbs(cList map[string]*MudCommand) {
	for _, verb := range worldfile.RuleVerbs {
		v := verb
		cList[v] = &MudCommand{
			description: verbHelp[v],
			action: func(player *MudPlayer, arguments string) int {
				player.doVerb(v, arguments)
				return muddata.MUD_OK
			},
		}
	}
}

// thing resolves what a player typed to one examine target (features, people, items here or carried).
func (mp *MudPlayer) thing(query string, packOnly bool) (hit *examineTarget, message string) {
	query = strings.ToUpper(strings.Join(strings.Fields(query), " "))
	for _, lead := range []string{"THE "} {
		query = strings.TrimPrefix(query, lead)
	}
	if strings.HasPrefix(query, "MY ") {
		packOnly, query = true, strings.TrimPrefix(query, "MY ")
	}
	if query == "" {
		return nil, "What do you mean?"
	}
	hit, several := pickTarget(mp.examineTargets(packOnly), query)
	switch {
	case hit != nil:
		return hit, ""
	case len(several) > 0:
		return nil, "Which do you mean: " + strings.Join(several, ", ") + "?"
	case packOnly:
		return nil, "You are not carrying that."
	}
	return nil, "You do not see that here."
}

// doVerb is a puzzle verb. Loop only.
func (mp *MudPlayer) doVerb(verb, arguments string) {
	args := strings.TrimSpace(arguments)
	if args == "" {
		mp.SendMessage(verbHelp[verb])
		return
	}
	var targetText, withText, value string
	switch {
	case verb == "SET":
		if loc := toValueRE.FindStringIndex(args); loc != nil {
			targetText, value = args[:loc[0]], strings.TrimSpace(args[loc[1]:])
		}
		if targetText == "" || value == "" {
			mp.SendMessage(verbHelp[verb])
			return
		}
	case twoThingVerbs[verb]:
		if loc := prepRE.FindStringIndex(args); loc != nil {
			withText, targetText = args[:loc[0]], args[loc[1]:] // USE key ON door: the item first, then what it is for
		} else if verb == "GIVE" || verb == "PUT" || verb == "THROW" {
			mp.SendMessage(verbHelp[verb])
			return
		} else {
			targetText = args // USE lens
		}
	default:
		targetText = args
	}

	if verb == "READ" && mp.useScroll(targetText) {
		return
	}
	var withKey string
	if verb == "USE" && withText == "" {
		if mp.useConsumable(targetText) {
			return
		}
	}
	if withText != "" {
		with, msg := mp.thing(withText, true)
		if with == nil {
			mp.SendMessage(msg)
			return
		}
		withKey = with.key
	}
	target, msg := mp.thing(targetText, false)
	if target == nil {
		mp.SendMessage(msg)
		return
	}
	if target.key != "" && mp.act(verb, target.key, withKey, value) {
		return
	}
	if verb == "GIVE" {
		mp.SendMessage(target.display + " does not want that.")
		return
	}
	mp.SendMessage("Nothing happens.")
}

// relocatePlayer moves a player to a room with the usual notices and shows them the room.
func relocatePlayer(p *MudPlayer, dest *Room) {
	if p.Location == dest {
		return
	}
	if p.Location != nil {
		p.Location.RemovePlayer(p.Username)
		p.Location.BroadcastFrame(encodeLeave(p, ""), p.Username)
	}
	p.NPCTarget = nil
	p.Location = dest
	p.rememberCopy()
	dest.BroadcastFrame(encodeArrive(p, ""), p.Username)
	dest.AddPlayer(p)
	p.dirty = true
	p.visit(dest.Key) // the exploration XP line goes before the description
	p.SendLook()
	p.entered()
}
