package worldfile

import (
	"strings"
	"testing"
)

func TestOnboardingDefaultsFillWhatContentLeavesOut(t *testing.T) {
	w, res, err := ValidateDir(write(t, map[string]string{"w.yaml": roomWith + "onboarding:\n  welcome: Hi.\n  steps: {look: Look now.}\n"}))
	if err != nil || len(res.Errors) != 0 {
		t.Fatalf("err %v errors %v", err, res.Errors)
	}
	o := w.ResolvedOnboarding()
	if o == nil || o.Welcome != "Hi." || o.Steps["look"] != "Look now." {
		t.Fatalf("resolved = %+v", o)
	}
	for _, s := range OnboardingSteps {
		if o.Steps[s] == "" {
			t.Errorf("step %q has no line", s)
		}
	}
	if o.Finish == "" {
		t.Error("finish has no default")
	}
}

func TestNoOnboardingBlockMeansNoWalkthrough(t *testing.T) {
	w, _, _ := ValidateDir(write(t, map[string]string{"w.yaml": roomWith}))
	if w.ResolvedOnboarding() != nil {
		t.Error("a world without the block should have no walkthrough")
	}
	w, res, _ := ValidateDir(write(t, map[string]string{"w.yaml": roomWith + "onboarding: {}\n"}))
	if len(res.Errors) != 0 || w.ResolvedOnboarding() == nil {
		t.Errorf("an empty block should turn the defaults on: %v", res.Errors)
	}
}

func TestOnboardingProblems(t *testing.T) {
	long := strings.Repeat("x", MaxOnboardingLine+1)
	_, res, _ := ValidateDir(write(t, map[string]string{
		"a.yaml": roomWith + "onboarding:\n  welcome: \"" + long + "\"\n  steps: {fly: Up you go.}\n",
		"b.yaml": "onboarding: {}\n",
	}))
	var all []string
	for _, p := range append(res.Errors, res.Warnings...) {
		all = append(all, p.String())
	}
	text := strings.Join(all, "\n")
	for _, want := range []string{"is not a step", "onboarding is already set", "keep it under"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}

func TestOnboardingIsRenderedForReview(t *testing.T) {
	out := render(t, map[string]string{"w.yaml": roomWith + "onboarding:\n  welcome: Hello there.\n  steps: {talk: Say hi.}\n"})
	for _, want := range []string{"## Guided first steps", "- welcome: Hello there.", "- talk: Say hi.", "- look: Try LOOK", "- finish: "} {
		if !strings.Contains(out, want) {
			t.Errorf("overview is missing %q\n%s", want, out)
		}
	}
}
