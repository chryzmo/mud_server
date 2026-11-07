package mudserver

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"mud_server/src/internal/worldfile"
)

func TestLevelsComeFromTheTable(t *testing.T) {
	p := worldfile.DefaultProgression()
	for _, tc := range []struct{ xp, level, next int }{
		{0, 1, 25}, {24, 1, 25}, {25, 2, 60}, {899, 9, 900}, {900, 10, 0}, {99999, 10, 0},
	} {
		l := levelFor(&p, tc.xp)
		if l != tc.level || nextLevelAt(&p, l) != tc.next {
			t.Errorf("xp %d: level %d next %d, want %d and %d", tc.xp, l, nextLevelAt(&p, l), tc.level, tc.next)
		}
	}
}

func decodeBoard(t *testing.T, b *board) boardJSON {
	t.Helper()
	var out boardJSON
	if err := json.Unmarshal(b.json, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBoardRanksEachTabAndDropsWhatIsNotPublic(t *testing.T) {
	p := worldfile.DefaultProgression()
	rows := []boardRow{
		{Name: "Zed", Explore: 100, Fight: 0, Color: "gold", Badge: "star"},
		{Name: "amy", Explore: 40, Fight: 60},
		{Name: "Bob", Explore: 100, Fight: 0, Color: "red; background:url(x)", Badge: "skull"}, // ties Zed; bad keys are dropped
		{Name: "Cy", Explore: 0, Fight: 300},
	}
	b := buildBoard(rows, &p, time.Unix(0, 0))
	got := decodeBoard(t, b)
	names := func(l []boardEntry) (out []string) {
		for _, e := range l {
			out = append(out, e.Name)
		}
		return
	}
	if fmt.Sprint(names(got.Overall)) != "[Cy amy Bob Zed]" { // 300, then amy, Bob and Zed tie on 100: by name, ignoring case
		t.Errorf("overall = %v", names(got.Overall))
	}
	if fmt.Sprint(names(got.Explorers)) != "[Bob Zed amy]" || fmt.Sprint(names(got.Fighters)) != "[Cy amy]" {
		t.Errorf("explorers %v fighters %v", names(got.Explorers), names(got.Fighters))
	}
	if got.Total != 4 || b.ranks["cy"] != 1 || b.ranks["zed"] != 4 {
		t.Errorf("total %d ranks %v", got.Total, b.ranks)
	}
	for _, e := range got.Overall {
		switch e.Name {
		case "Zed":
			if e.Color != "gold" || e.Badge != "star" || e.Level != 3 {
				t.Errorf("Zed = %+v", e)
			}
		case "Bob":
			if e.Color != "" || e.Badge != "" {
				t.Errorf("Bob kept bad keys: %+v", e)
			}
		}
	}
}

func TestBoardIsCappedAndEmptyListsAreArrays(t *testing.T) {
	p := worldfile.DefaultProgression()
	var rows []boardRow
	for i := 0; i < boardTop+30; i++ {
		rows = append(rows, boardRow{Name: fmt.Sprintf("p%03d", i), Explore: int64(1000 - i)})
	}
	got := decodeBoard(t, buildBoard(rows, &p, time.Now()))
	if len(got.Overall) != boardTop || got.Total != boardTop+30 || got.Overall[0].Name != "p000" {
		t.Errorf("%d entries, total %d", len(got.Overall), got.Total)
	}
	empty := string(buildBoard(nil, &p, time.Unix(0, 0)).json)
	if want := `{"updated":"1970-01-01T00:00:00Z","total":0,"overall":[],"explorers":[],"fighters":[]}`; empty != want {
		t.Errorf("empty board = %s", empty)
	}
}
