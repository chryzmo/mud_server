package mudserver

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"mud_server/src/internal/muddata"
	"mud_server/src/internal/mudutils"
)

/*
Socials, WHO and IGNORE (plans/event-systems.md B1, B2, B4). A social is a canned verb a player can
do alone or aimed at someone in the room (WAVE, WAVE BOB); EMOTE <text> is the free form. They are
chatter, so they share SAY's rate limit, refuse muted players, and are hidden from anyone who has
ignored the sender. WHO gives counts, not a list of names. IGNORE hides a player's SAY, whispers,
emotes and global chat from you for the session; a GM cannot be ignored.
*/

const (
	emoteMax      = 100
	ignoreMax     = 20
	whoCacheFor   = 5 * time.Second
	ignoreNameMax = 24
)

// social is one canned verb: "you wave", "Ann waves", then a preposition before the target.
type social struct {
	you, third, prep, alone string
}

var socials = map[string]social{
	"WAVE":     {you: "wave", third: "waves", prep: "at"},
	"BOW":      {you: "bow", third: "bows", prep: "to"},
	"CHEER":    {you: "cheer", third: "cheers", prep: "for"},
	"DANCE":    {you: "dance", third: "dances", prep: "with"},
	"HUG":      {you: "hug", third: "hugs"},
	"SHRUG":    {you: "shrug", third: "shrugs", prep: "at"},
	"NOD":      {you: "nod", third: "nods", prep: "at"},
	"LAUGH":    {you: "laugh", third: "laughs", prep: "with"},
	"CLAP":     {you: "clap", third: "claps", prep: "for"},
	"SALUTE":   {you: "salute", third: "salutes"},
	"HIGHFIVE": {you: "high-five", third: "high-fives", alone: " the air"},
	"FACEPALM": {you: "facepalm", third: "facepalms", prep: "at"},
}

// SocialNames is the verbs in a stable order (the client lists them; tests use them).
func SocialNames() []string {
	names := make([]string, 0, len(socials))
	for n := range socials {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (mp *MudPlayer) ignores(other *MudPlayer) bool {
	return len(mp.ignored) > 0 && other != nil && mp.ignored[strings.ToLower(other.Username)]
}

func styled(name string) string { return mudutils.ColorString(name, mudutils.COLOR_BLUE) }

// tellRoom sends text to everyone in the actor's room except the actor and the excluded, and
// skips anyone who ignores the actor.
func tellRoom(actor *MudPlayer, text string, except ...*MudPlayer) {
	frame := encodeText(text)
outer:
	for _, p := range actor.Location.Players() {
		if p == actor || p.ignores(actor) {
			continue
		}
		for _, e := range except {
			if p == e {
				continue outer
			}
		}
		p.sendFrame(frame)
	}
}

// chatterAllowed is the checks every social and emote passes first: not muted, not too fast.
func (ms *MudServer) chatterAllowed(p *MudPlayer, now time.Time) bool {
	return !p.refuseIfMuted(now) && !p.quietRefused(now) && p.allowSay(now)
}

// pickInRoom finds a player in the actor's room by name or unique prefix (three letters or more).
func pickInRoom(actor *MudPlayer, query string) (*MudPlayer, string) {
	query = strings.ToLower(strings.TrimSpace(query))
	var prefix []*MudPlayer
	for _, p := range actor.Location.Players() {
		name := strings.ToLower(p.Username)
		if name == query {
			return p, ""
		}
		if len(query) >= 3 && strings.HasPrefix(name, query) {
			prefix = append(prefix, p)
		}
	}
	switch len(prefix) {
	case 1:
		return prefix[0], ""
	case 0:
		return nil, "You do not see '" + query + "' here."
	}
	return nil, "Which one do you mean: " + strings.Join(namesOf(prefix), ", ") + "?"
}

func namesOf(ps []*MudPlayer) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Username
	}
	sort.Strings(out)
	return out
}

// doSocial runs one social verb. Loop only.
func (ms *MudServer) doSocial(p *MudPlayer, verb, arg string, now time.Time) {
	s := socials[verb]
	if !ms.chatterAllowed(p, now) {
		return
	}
	prep := ""
	if s.prep != "" {
		prep = s.prep + " "
	}
	arg = strings.TrimSpace(arg)
	var target *MudPlayer
	if arg != "" {
		var problem string
		if target, problem = pickInRoom(p, arg); target == nil {
			p.SendMessage(problem)
			return
		}
		if target == p {
			target = nil // aimed at yourself is just doing it
		}
	}
	if target == nil {
		p.SendMessage(fmt.Sprintf("You %s%s.", s.you, s.alone))
		tellRoom(p, fmt.Sprintf("%s %s%s.", styled(p.Username), s.third, s.alone))
		return
	}
	p.SendMessage(fmt.Sprintf("You %s %s%s.", s.you, prep, target.Username))
	if !target.ignores(p) {
		target.SendMessage(fmt.Sprintf("%s %s %syou.", styled(p.Username), s.third, prep))
	}
	tellRoom(p, fmt.Sprintf("%s %s %s%s.", styled(p.Username), s.third, prep, target.Username), target)
}

// doEmote is EMOTE <text>: "* Ann dances wildly." Loop only.
func (ms *MudServer) doEmote(p *MudPlayer, text string, now time.Time) {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		p.SendMessage("Usage: EMOTE <what you do>  (like EMOTE juggles three lemons; WAVE, BOW and others are ready-made)")
		return
	}
	if utf8.RuneCountInString(text) > emoteMax {
		p.SendMessage(fmt.Sprintf("That is too long for an emote (at most %d characters).", emoteMax))
		return
	}
	if !ms.chatterAllowed(p, now) || ms.checkRepeat(p, text, now) {
		return
	}
	text = ms.names.mask(text)
	if p.lineSink != nil && p.Location != nil {
		p.lineSink(p.Location.Name, p.Username, "* "+text)
	}
	line := fmt.Sprintf("* %s %s", styled(p.Username), text)
	p.SendMessage(line)
	tellRoom(p, line)
}

// whoSnapshot is the counts WHO shows, rebuilt at most every few seconds so a crowd typing WHO costs one pass.
type whoSnapshot struct {
	at      time.Time
	total   int
	regions []string // "Atrium 12", in a stable order
}

func (ms *MudServer) who(now time.Time) *whoSnapshot {
	if ms.whoCache != nil && now.Sub(ms.whoCache.at) < whoCacheFor {
		return ms.whoCache
	}
	counts := map[string]int{}
	total := 0
	for _, p := range ms.players {
		if p.dormant {
			continue // dropped, still in the world for a grace period
		}
		total++
		region := elsewhere
		if p.Location != nil && ms.World.guide != nil {
			if r, ok := ms.World.guide.regionOf[p.Location.Key]; ok {
				region = r
			}
		}
		counts[region]++
	}
	names := make([]string, 0, len(counts))
	for r := range counts {
		names = append(names, r)
	}
	sort.Strings(names)
	snap := &whoSnapshot{at: now, total: total}
	for _, r := range names {
		snap.regions = append(snap.regions, fmt.Sprintf("%s %d", r, counts[r]))
	}
	ms.whoCache = snap
	return snap
}

func (ms *MudServer) whoText(p *MudPlayer, now time.Time) string {
	snap := ms.who(now)
	var b strings.Builder
	if snap.total == 1 {
		b.WriteString("You are the only one online.")
	} else {
		fmt.Fprintf(&b, "%d players are online.", snap.total)
	}
	if len(snap.regions) > 1 {
		b.WriteString("\nWhere: " + strings.Join(snap.regions, ", ") + ".")
	}
	var here []string
	if p.Location != nil {
		for _, o := range p.Location.Players() {
			if o != p {
				here = append(here, o.Username)
			}
		}
	}
	sort.Strings(here)
	switch {
	case len(here) == 0:
		b.WriteString("\nNo one else is here with you.")
	case len(here) > 20:
		b.WriteString("\nHere with you: " + strings.Join(here[:20], ", ") + fmt.Sprintf(" and %d more.", len(here)-20))
	default:
		b.WriteString("\nHere with you: " + strings.Join(here, ", ") + ".")
	}
	return b.String()
}

// doIgnore is IGNORE [name]; doUnignore is UNIGNORE <name|ALL>. Loop only.
func (ms *MudServer) doIgnore(p *MudPlayer, arg string) {
	name := cleanName(arg)
	if name == "" {
		if len(p.ignored) == 0 {
			p.SendMessage("You are not ignoring anyone. IGNORE <name> hides what they say to you until you leave; UNIGNORE <name> undoes it.")
			return
		}
		list := make([]string, 0, len(p.ignored))
		for n := range p.ignored {
			list = append(list, n)
		}
		sort.Strings(list)
		p.SendMessage("You are ignoring: " + strings.Join(list, ", ") + ". UNIGNORE <name> undoes it.")
		return
	}
	if strings.EqualFold(name, p.Username) {
		p.SendMessage("You cannot ignore yourself.")
		return
	}
	if o := ms.findPlayerFold(name); o != nil {
		if o.gm {
			p.SendMessage("You cannot ignore a GM. REPORT someone if they are a problem.")
			return
		}
		name = o.Username
	}
	if p.ignored == nil {
		p.ignored = map[string]bool{}
	}
	key := strings.ToLower(name)
	if !p.ignored[key] && len(p.ignored) >= ignoreMax {
		p.SendMessage(fmt.Sprintf("You can ignore at most %d players. UNIGNORE someone first.", ignoreMax))
		return
	}
	p.ignored[key] = true
	p.SendMessage(name + " is ignored: you will not see what they say, whisper, emote or send to global chat until you leave. UNIGNORE " + name + " undoes it.")
}

func (ms *MudServer) doUnignore(p *MudPlayer, arg string) {
	arg = strings.TrimSpace(arg)
	switch {
	case arg == "":
		p.SendMessage("Usage: UNIGNORE <name>  (or UNIGNORE ALL)")
	case strings.EqualFold(arg, "ALL"):
		p.ignored = nil
		p.SendMessage("You are not ignoring anyone now.")
	case p.ignored[strings.ToLower(arg)]:
		delete(p.ignored, strings.ToLower(arg))
		p.SendMessage(arg + " is no longer ignored.")
	default:
		p.SendMessage("You are not ignoring '" + arg + "'.")
	}
}

func registerSocial(ms *MudServer, cList map[string]*MudCommand) {
	for verb := range socials {
		verb := verb
		cList[verb] = &MudCommand{description: strings.ToUpper(verb[:1]) + strings.ToLower(verb[1:]) + ", alone or at someone here.  Usage: " + verb + " [player]", action: func(p *MudPlayer, arg string) int {
			ms.doSocial(p, verb, arg, time.Now())
			return muddata.MUD_OK
		}}
	}
	cList["EMOTE"] = &MudCommand{description: "Do something in front of the room.  Usage: EMOTE <what you do>  (/me works too)", action: func(p *MudPlayer, arg string) int {
		ms.doEmote(p, arg, time.Now())
		return muddata.MUD_OK
	}}
	cList["WHO"] = &MudCommand{description: "How many players are online, and who is here with you.", action: func(p *MudPlayer, arg string) int {
		p.SendMessage(ms.whoText(p, time.Now()))
		return muddata.MUD_OK
	}}
	cList["IGNORE"] = &MudCommand{description: "Hide a player's talk from you until you leave.  Usage: IGNORE [name]  (alone lists who)", action: func(p *MudPlayer, arg string) int {
		ms.doIgnore(p, arg)
		return muddata.MUD_OK
	}}
	cList["UNIGNORE"] = &MudCommand{description: "Stop ignoring a player.  Usage: UNIGNORE <name|ALL>", action: func(p *MudPlayer, arg string) int {
		ms.doUnignore(p, arg)
		return muddata.MUD_OK
	}}
}
