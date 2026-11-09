package mudserver

import (
	"regexp"
	"strings"

	"mud_server/src/internal/mudutils"
)

/*
People (plans/interactive-world.md section 4). A room's content may have friendly characters:
not monsters, they never fight or move, and every word they say is written by the content's
author. TALK <person> gives their greeting and the topics they will discuss; ASK <person>
ABOUT <word> gets the answer to a topic. People can be examined like scenery, and LOOK
names them. Topics match by keyword, whole or by a unique prefix of three or more letters.
*/

// Person is a friendly character in a room. The first name is what they are called.
type Person struct {
	Key      string   `json:"key"`
	Names    []string `json:"names"`
	Examine  string   `json:"examine"`
	Greeting string   `json:"greeting"`
	Shrug    string   `json:"shrug,omitempty"`
	Topics   []Topic  `json:"topics,omitempty"`
	Shop     []ShopItem `json:"shop,omitempty"` // what they sell (shop.go)
}

// Topic is something a person can be asked about; the first keyword is the one TALK lists.
type Topic struct {
	Key      string   `json:"key"`
	Keywords []string `json:"keywords"`
	Say      string   `json:"say"`
}

// peopleNames are the display names LOOK mentions.
func (r *Room) peopleNames() []string {
	var out []string
	for _, p := range r.People {
		if len(p.Names) > 0 {
			out = append(out, p.Names[0])
		}
	}
	return out
}

// findPerson picks the person a query means among the people in the room, with the same
// matching as EXAMINE. Loop only.
func (mp *MudPlayer) findPerson(query string) (*Person, []string) {
	if mp.Location == nil {
		return nil, nil
	}
	var targets []examineTarget
	for i := range mp.Location.People {
		p := &mp.Location.People[i]
		if len(p.Names) > 0 {
			targets = append(targets, examineTarget{display: p.Names[0], names: p.Names, text: p.Key})
		}
	}
	hit, several := pickTarget(targets, strings.ToUpper(query))
	if hit == nil {
		return nil, several
	}
	for i := range mp.Location.People {
		if mp.Location.People[i].Key == hit.text {
			return &mp.Location.People[i], nil
		}
	}
	return nil, nil
}

// topicList is the sentence TALK ends with.
// topicList is the topics a person can be asked about, each in the white code so the client can make it a
// button (it asks with the red-coloured name earlier in the same line).
func (p *Person) topicList() string {
	var words []string
	for _, t := range p.Topics {
		if len(t.Keywords) > 0 {
			words = append(words, mudutils.ColorString(t.Keywords[0], mudutils.COLOR_WHITE)) // the client makes each a button
		}
	}
	return strings.Join(words, ", ")
}

// personLinks joins names with commas, each in the bright magenta code, so the client makes each a button that
// sends TALK <name> (the same marking as the "Also here" line).
func personLinks(names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = mudutils.ColorString(n, mudutils.COLOR_BRIGHT_MAGENTA)
	}
	return strings.Join(out, ", ")
}

// talk answers TALK [person]. Loop only.
func (mp *MudPlayer) talk(arguments string) {
	query := strings.Join(strings.Fields(arguments), " ")
	query = strings.TrimPrefix(strings.TrimPrefix(strings.ToUpper(query), "TO "), "THE ")
	if mp.Location == nil || len(mp.Location.People) == 0 {
		mp.SendMessage("There is no one here to talk to.")
		return
	}
	if query == "" {
		if len(mp.Location.People) == 1 {
			query = mp.Location.People[0].Names[0]
		} else {
			mp.SendMessage("Talk to whom? " + personLinks(mp.Location.peopleNames()) + ".")
			return
		}
	}
	p, several := mp.findPerson(query)
	switch {
	case p != nil:
		msg := npcSpeech(strings.TrimSpace(p.Greeting))
		if list := p.topicList(); list != "" {
			msg += "\n(You could ask " + mudutils.ColorString(p.Names[0], mudutils.COLOR_RED) + " about: " + list + ".  ASK <name> ABOUT <topic>)"
		}
		mp.SendMessage(msg)
		mp.tourDid("talk")
	case len(several) > 0:
		mp.SendMessage("Which do you mean: " + personLinks(several) + "?")
	default:
		mp.SendMessage("There is no one called that here.")
	}
}

var askAbout = regexp.MustCompile(`(?i)\s+ABOUT\s+`)

// ask answers ASK <person> ABOUT <topic> (or ASK <person> <topic>). Loop only.
func (mp *MudPlayer) ask(arguments string) {
	args := strings.TrimSpace(arguments)
	if args == "" {
		mp.SendMessage("Ask whom about what? Usage: ASK <person> ABOUT <topic>")
		return
	}
	var who, topic string
	if loc := askAbout.FindStringIndex(args); loc != nil {
		who, topic = args[:loc[0]], args[loc[1]:]
	} else if f := strings.Fields(args); len(f) >= 2 {
		who, topic = f[0], strings.Join(f[1:], " ")
	} else {
		who = args
	}
	if mp.Location == nil || len(mp.Location.People) == 0 {
		mp.SendMessage("There is no one here to ask.")
		return
	}
	p, several := mp.findPerson(strings.TrimPrefix(strings.ToUpper(strings.Join(strings.Fields(who), " ")), "THE "))
	switch {
	case p == nil && len(several) > 0:
		mp.SendMessage("Which do you mean: " + strings.Join(several, ", ") + "?")
		return
	case p == nil:
		mp.SendMessage("There is no one called that here.")
		return
	case strings.TrimSpace(topic) == "":
		mp.SendMessage("Ask " + mudutils.ColorString(p.Names[0], mudutils.COLOR_RED) + " about what? " + p.topicList() + ".")
		return
	}
	query := strings.ToUpper(strings.Join(strings.Fields(topic), " "))
	var targets []examineTarget
	for _, t := range p.Topics {
		targets = append(targets, examineTarget{display: t.Keywords[0], names: t.Keywords, text: t.Say})
	}
	hit, many := pickTarget(targets, query)
	switch {
	case hit != nil:
		mp.SendMessage(npcSpeech(strings.TrimSpace(hit.text)))
	case len(many) > 0:
		mp.SendMessage("Which do you mean: " + strings.Join(many, ", ") + "?")
	case strings.TrimSpace(p.Shrug) != "":
		mp.SendMessage(npcSpeech(strings.TrimSpace(p.Shrug)))
	default:
		mp.SendMessage(p.Names[0] + " does not seem to know anything about that.")
	}
}

// speechQuote is a spoken passage: words between double quotes, on one line.
var speechQuote = regexp.MustCompile(`"[^"\n]*"`)

// npcSpeech colours the quoted words in a character's text (violet in the client) and leaves the narration
// around them alone.
func npcSpeech(text string) string {
	return speechQuote.ReplaceAllStringFunc(text, func(q string) string {
		return mudutils.ColorString(q, mudutils.COLOR_MAGENTA)
	})
}
