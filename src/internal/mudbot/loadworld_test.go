package mudbot

import (
	"strings"
	"testing"
	"time"

	"mud_server/src/internal/worldfile"
)

// The load world must stay valid as the content format grows: a run that cannot start is worse than none.
func TestTheLoadWorldIsValidAndShapedForTheRun(t *testing.T) {
	dir := t.TempDir()
	if err := WriteLoadWorld(dir, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	w, res, err := worldfile.ValidateDir(dir)
	if err != nil || len(res.Errors) != 0 || len(res.Warnings) != 0 {
		t.Fatalf("err %v, errors %v, warnings %v", err, res.Errors, res.Warnings)
	}
	if hall := w.Rooms["hall"]; hall.Capacity != 200 || hall.ResolvedCrowd().QuietAt != 30 {
		t.Errorf("the feast hall should hold 200 and go quiet at 30, got %+v", hall.ResolvedCrowd())
	}
	if !strings.Contains(LoadWorldYAML(time.Date(2027, 1, 7, 17, 0, 0, 0, time.UTC)), `"2027-01-07 17:00 UTC"`) {
		t.Error("the feast time should be written to the minute in UTC")
	}
}

func TestVerbsMixRotatesAndStaysWhenAsked(t *testing.T) {
	first, cycle := verbLines(3, true)
	if first != "MOVE E" {
		t.Errorf("staying bots walk east first, got %q", first)
	}
	for _, line := range cycle {
		if strings.HasPrefix(line, "MOVE") {
			t.Errorf("a staying bot should not keep walking, cycle has %q", line)
		}
	}
	first, cycle = verbLines(0, false)
	if first != "" || cycle[len(cycle)-1] != "MOVE W" {
		t.Errorf("a walking bot ends its turn back where it began: first %q, cycle %v", first, cycle)
	}
	a, b := cycle[0], func() string { _, c := verbLines(1, false); return c[0] }()
	if a == b {
		t.Errorf("neighbouring bots should start at different verbs, both started with %q", a)
	}
	c := goodStormConfig()
	c.Mix, c.Stay = "verbs", true
	if err := c.validate(); err != nil {
		t.Errorf("verbs with stay refused: %v", err)
	}
	c.Mix = "light"
	if err := c.validate(); err == nil {
		t.Error("stay without the verbs mix should be refused")
	}
	c.Mix, c.Stay = "nonsense", false
	if err := c.validate(); err == nil {
		t.Error("an unknown mix should be refused")
	}
}
