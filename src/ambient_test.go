package main

import "testing"

// Ambient life through a real server: a room's day note shows in LOOK at any hour (the unit tests in
// internal/mudserver cover the clock, the schedule and the budget with a fixed clock).

func TestLookShowsTheDayNoteForTheCurrentTimeOfDay(t *testing.T) {
	t.Parallel()
	notes := `{dawn: "The Hall wakes.", day: "The Hall wakes.", dusk: "The Hall wakes.", night: "The Hall wakes."}`
	dir := contentDir(t, map[string]string{"world.yaml": `
start_room: hall
rooms:
  hall:
    name: Hall
    description: A hall.
    day_notes: ` + notes + `
    ambient:
      - {every: [3000, 3600], lines: ["Dust drifts."]}
`})
	wsURL, _ := startTestServerWithConfig(t, withContent(dir))
	conn := eventPlayer(t, wsURL, "looker")
	send(t, conn, "LOOK")
	expect(t, conn, "The Hall wakes.")
}
