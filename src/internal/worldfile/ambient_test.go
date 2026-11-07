package worldfile

import (
	"strings"
	"testing"
)

const roomWith = `
start_room: hall
rooms:
  hall:
    name: Hall
    description: A hall.
`

func TestGoodAmbientIsCleanAndRendered(t *testing.T) {
	files := map[string]string{"w.yaml": roomWith + `    ambient:
      - {every: [90, 240], lines: ["A gust ruffles the banners.", "A bell rings once."]}
      - {every: [120, 300], when: {time_of_day: night, players_at_least: 2}, lines: ["The Hall hums."]}
    day_notes: {night: "The lamps are dimmed.", dawn: "Grey light."}
    people:
      bell:
        names: [Bellweather]
        examine: A keeper.
        greeting: Hello.
        idle: ["Bellweather polishes a key."]
        idle_every: [60, 90]
`}
	_, res, err := ValidateDir(write(t, files))
	if err != nil || len(res.Errors) != 0 {
		t.Fatalf("err %v errors %v", err, res.Errors)
	}
	out := render(t, files)
	for _, want := range []string{"**Ambient:**", "every 90-240 s:", "A bell rings once.", "only at night", "LOOK at night adds: The lamps are dimmed.", "LOOK at dawn adds: Grey light.", "idle: Bellweather polishes a key."} {
		if !strings.Contains(out, want) {
			t.Errorf("overview is missing %q\n%s", want, out)
		}
	}
}

func TestAmbientProblems(t *testing.T) {
	long := strings.Repeat("x", MaxAmbientLine+1)
	_, res, _ := ValidateDir(write(t, map[string]string{"w.yaml": roomWith + `    ambient:
      - {every: [5, 10], lines: [fine]}
      - {every: [100, 50], lines: [fine]}
      - {every: [10], lines: [fine]}
      - {every: [60, 90], lines: []}
      - {every: [60, 90], lines: ["", "` + long + `"]}
      - {every: [60, 90], when: {time_of_day: teatime, players_at_least: -1}, lines: [fine]}
      - {every: [60, 4000], lines: [fine]}
    day_notes: {midnight: "x"}
    people:
      p:
        names: [P]
        examine: e
        greeting: g
        idle: [fine]
        idle_every: [1, 2]
      q:
        names: [Q]
        examine: e
        greeting: g
        idle_every: [60, 90]
`}))
	var all string
	for _, p := range res.Errors {
		all += p.String() + "\n"
	}
	for _, p := range res.Warnings {
		all += "warning " + p.String() + "\n"
	}
	for _, want := range []string{
		"at most 6 per room", "ambient[0].every", "ambient[1].every", "ambient[2].every: must be [least, most]", "ambient[3].lines: needs at least one line",
		"ambient[4].lines[0]: is empty", "ambient[4].lines[1]", "time_of_day: \"teatime\"", "players_at_least", "ambient[6].every", "day_notes.midnight",
		"people.p.idle_every", "rooms.hall.people.q.idle_every: has no idle lines",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
}
