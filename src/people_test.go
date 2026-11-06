package main

import (
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	mudserver "mud_server/src/internal/mudserver"
)

// People (plans/interactive-world.md section 4): TALK and ASK.

const cafeWorld = `
start_room: cafe
rooms:
  cafe:
    name: Cafe
    description: A small cafe.
    exits: {N: back}
    features:
      counter: {names: [long counter, counter], examine: "A long wooden counter."}
    people:
      mira:
        names: [Mira the Barista, Mira, barista]
        examine: "Mira has flour on her nose."
        greeting: "\"Welcome in,\" says Mira."
        shrug: "Mira laughs. \"No idea!\""
        topics:
          coffee: {keywords: [coffee, brew, beans], say: "\"Dark, strong, and free.\""}
          map: {keywords: [map, directions], say: "\"The back room is north.\""}
      miro:
        names: [Miro the Baker, Miro, baker]
        examine: "Miro is covered in flour."
        greeting: "\"Bread!\" says Miro."
  back:
    name: Back Room
    description: Empty.
    exits: {S: cafe}
  lonely:
    name: Lonely Room
    description: Nobody here.
    exits: {}
`

func cafeServer(t *testing.T, world string, configure func(*mudserver.Configuration)) string {
	dir := contentDir(t, map[string]string{"w.yaml": world})
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		c.World.ContentDir = dir
		if configure != nil {
			configure(c)
		}
	})
	return wsURL
}

// cafeVisitor logs in a new password player and waits until they are in the cafe.
func cafeVisitor(t *testing.T, wsURL, name string) *websocket.Conn {
	t.Helper()
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")
	loginNew(t, conn, name)
	expect(t, conn, "Welcome back, "+name+"!")
	expect(t, conn, "You are in 'Cafe'")
	return conn
}

func TestLookNamesThePeopleAndEveryoneCanBeExamined(t *testing.T) {
	wsURL := cafeServer(t, cafeWorld, nil)
	conn := cafeVisitor(t, wsURL, "visitor")
	send(t, conn, "LOOK")
	out := expect(t, conn, "Also here: Mira the Barista, Miro the Baker. (TALK to them.)")
	if !strings.Contains(out, "You notice: long counter.") {
		t.Errorf("features are missing from LOOK: %q", out)
	}
	send(t, conn, "X mira")
	expect(t, conn, "Mira has flour on her nose.")
	send(t, conn, "X baker")
	expect(t, conn, "Miro is covered in flour.")
	send(t, conn, "X mir") // two people share the prefix
	expect(t, conn, "Which do you mean: Mira the Barista, Miro the Baker?")
}

func TestTalkGivesTheGreetingAndTheTopics(t *testing.T) {
	wsURL := cafeServer(t, cafeWorld, nil)
	conn := cafeVisitor(t, wsURL, "visitor")
	send(t, conn, "TALK barista")
	out := expect(t, conn, "Welcome in")
	if !strings.Contains(out, "about: coffee, map.") {
		t.Errorf("TALK should list the topics: %q", out)
	}
	send(t, conn, "TALK to the baker")
	out = expect(t, conn, "Bread!")
	if strings.Contains(out, "You could ask") {
		t.Errorf("a person with no topics should not list any: %q", out)
	}
	send(t, conn, "TALK") // two people: it asks whom
	expect(t, conn, "Talk to whom? Mira the Barista, Miro the Baker.")
	send(t, conn, "TALK mir")
	expect(t, conn, "Which do you mean: Mira the Barista, Miro the Baker?")
	send(t, conn, "TALK nobody")
	expect(t, conn, "There is no one called that here.")
}

func TestAskFindsATopicByKeywordAliasOrPrefix(t *testing.T) {
	wsURL := cafeServer(t, cafeWorld, nil)
	conn := cafeVisitor(t, wsURL, "visitor")
	for _, tc := range []struct{ line, want string }{
		{"ASK mira ABOUT coffee", "Dark, strong, and free."},
		{"ask Mira about BEANS", "Dark, strong, and free."},
		{"ASK barista about directions", "The back room is north."},
		{"ASK mira ABOUT dir", "The back room is north."}, // a unique prefix
		{"ASK mira coffee", "Dark, strong, and free."},    // ABOUT is optional
		{"ASK the barista ABOUT the map", "No idea!"},     // "the map" is not a keyword: the shrug
		{"ASK mira ABOUT weather", "No idea!"},
		{"ASK miro ABOUT weather", "Miro the Baker does not seem to know anything about that."}, // no shrug of their own
	} {
		send(t, conn, tc.line)
		expect(t, conn, tc.want)
	}
	send(t, conn, "ASK mira")
	expect(t, conn, "Ask Mira the Barista about what? coffee, map.")
	send(t, conn, "ASK")
	expect(t, conn, "Ask whom about what?")
	send(t, conn, "ASK mir ABOUT coffee")
	expect(t, conn, "Which do you mean: Mira the Barista, Miro the Baker?")
	send(t, conn, "ASK nobody ABOUT coffee")
	expect(t, conn, "There is no one called that here.")
}

func TestPeopleBelongToTheirRoom(t *testing.T) {
	wsURL := cafeServer(t, cafeWorld, nil)
	conn := cafeVisitor(t, wsURL, "visitor")
	send(t, conn, "MOVE N")
	expect(t, conn, "You are in 'Back Room'")
	send(t, conn, "TALK mira")
	expect(t, conn, "There is no one here to talk to.")
	send(t, conn, "ASK mira ABOUT coffee")
	expect(t, conn, "There is no one here to ask.")
	send(t, conn, "X mira")
	expect(t, conn, "You do not see that here.")
	send(t, conn, "LOOK")
	if out := expect(t, conn, "You are in 'Back Room'"); strings.Contains(out, "Also here") {
		t.Errorf("an empty room names people: %q", out)
	}
}

func TestPeopleAppearInHelp(t *testing.T) {
	wsURL := cafeServer(t, cafeWorld, nil)
	conn := cafeVisitor(t, wsURL, "visitor")
	send(t, conn, "HELP")
	out := expect(t, conn, "Available commands")
	for _, want := range []string{"TALK", "ASK"} {
		if !strings.Contains(out, want) {
			t.Errorf("HELP is missing %s", want)
		}
	}
}

func TestACopyOfARoomKeepsItsPeople(t *testing.T) {
	wsURL := cafeServer(t, cafeWorld+"areas:\n  main: {rooms: [cafe, back]}\n", func(c *mudserver.Configuration) {
		c.Crowd = mudserver.CrowdConfig{SoftCap: 1, MaxCopies: 5}
	})
	cafeVisitor(t, wsURL, "first")
	second := cafeVisitor(t, wsURL, "second") // the second copy
	send(t, second, "ASK mira ABOUT coffee")
	expect(t, second, "Dark, strong, and free.")
}

func TestApplyingContentAgainChangesWhatPeopleSay(t *testing.T) {
	url := testDatabaseURL(t)
	resetDatabase(t, url)
	dir := contentDir(t, map[string]string{"w.yaml": cafeWorld})
	_, first := serveTestServer(t, url, withContent(dir))
	if got := queryString(t, "SELECT jsonb_array_length(peoplejson::jsonb)::text FROM room WHERE contentkey='cafe'"); got != "2" {
		t.Errorf("people stored = %s, want 2", got)
	}
	first.Shutdown(t.Context())
	edited := contentDir(t, map[string]string{"w.yaml": strings.Replace(cafeWorld, "Dark, strong, and free.", "Strong, and still free.", 1)})
	wsURL, _ := serveTestServer(t, url, withContent(edited))
	conn := cafeVisitor(t, wsURL, "visitor")
	send(t, conn, "ASK mira ABOUT coffee")
	expect(t, conn, "Strong, and still free.")
}

func TestTalkInAWorldWithNoPeople(t *testing.T) {
	wsURL := startTestServer(t) // the SQL seed
	conn := passwordPlayer(t, wsURL, "legacy")
	send(t, conn, "TALK")
	expect(t, conn, "There is no one here to talk to.")
}
