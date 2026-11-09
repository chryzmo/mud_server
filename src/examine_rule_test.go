package main

import (
	"strings"
	"testing"
)

// A rule that listens for examining a thing runs after the thing's own text, so a clue can be written in the
// journal when the player finds it (and starts the puzzle).

const clueWorld = `
start_room: hall
rooms:
  hall:
    name: Hall
    description: A hall.
    features:
      chart: {names: [chart], examine: "The chart says red is at the fourth mark."}
      sign: {names: [sign], examine: "A plain sign."}
    rules:
      - on: {examine: chart}
        do: [{set_flag: clue-red}]
      - on: {say: finished}
        do: [{set_flag: marks-done}]
puzzles:
  marks:
    title: The Marks
    steps:
      - {flag: clue-red, line: "The chart gave red at the fourth mark."}
    solved: {flag: marks-done, line: "Solved."}
    hints: ["Look at the chart."]
`

func TestExaminingAClueWritesItInTheJournal(t *testing.T) {
	t.Parallel()
	wsURL := cafeServer(t, clueWorld, nil)
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")
	loginNew(t, conn, "reader")
	expect(t, conn, "You are in 'Hall'")

	send(t, conn, "JOURNAL")
	if out := expect(t, conn, "Your journal"); strings.Contains(out, "The Marks") {
		t.Errorf("the puzzle began before any clue was found: %q", out)
	}

	send(t, conn, "EXAMINE sign") // a thing with no examine rule changes nothing
	expect(t, conn, "A plain sign.")
	send(t, conn, "JOURNAL")
	if out := expect(t, conn, "Your journal"); strings.Contains(out, "The Marks") {
		t.Errorf("examining another thing began the puzzle: %q", out)
	}

	send(t, conn, "EXAMINE chart")
	expect(t, conn, "The chart says red is at the fourth mark.")
	send(t, conn, "JOURNAL")
	out := expect(t, conn, "Your journal")
	if !strings.Contains(out, "The Marks") || !strings.Contains(out, "The chart gave red at the fourth mark.") {
		t.Errorf("the clue is not in the journal: %q", out)
	}
}
