package worldfile

import "strings"

/*
Guided first steps (plans/event-systems.md section 4, C1). A content onboarding: block turns the
walkthrough on for new players; a world without one has none. Every line is in the world's voice
and any line left out takes the plain default, so the block can be as short as "onboarding: {}".
The steps are taught in the order of OnboardingSteps; doing one early just ticks it off.
*/

// OnboardingSteps are the things the walkthrough teaches, in order.
var OnboardingSteps = []string{"look", "move", "examine", "talk", "say"}

// MaxOnboardingLine is the longest line the walkthrough may show.
const MaxOnboardingLine = 400

// Onboarding is the walkthrough's script.
type Onboarding struct {
	Welcome string            `yaml:"welcome"` // offered to a new player on arrival
	Steps   map[string]string `yaml:"steps"`   // what to try next, by step in OnboardingSteps
	Finish  string            `yaml:"finish"`  // shown when the last step is done: where HELP, WHO and REPORT are
}

// ResolvedOnboarding is the script with defaults filled in, or nil when the content has none.
func (w *World) ResolvedOnboarding() *Onboarding {
	if w.Onboarding == nil {
		return nil
	}
	out := &Onboarding{Welcome: w.Onboarding.Welcome, Finish: w.Onboarding.Finish, Steps: map[string]string{}}
	if out.Welcome == "" {
		out.Welcome = "Welcome. A short walkthrough can show you around."
	}
	if out.Finish == "" {
		out.Finish = "That is the way of it. HELP lists every command, WHO shows who is nearby, and REPORT tells the staff about a problem."
	}
	defaults := map[string]string{
		"look":    "Try LOOK to see where you are.",
		"move":    "Pick an exit from the list and go that way, for example MOVE N.",
		"examine": "Try EXAMINE with the name of something you can see.",
		"talk":    "Try TALK to speak to someone who is here.",
		"say":     "Try SAY followed by a greeting to speak to the room.",
	}
	for _, s := range OnboardingSteps {
		out.Steps[s] = defaults[s]
		if v := w.Onboarding.Steps[s]; v != "" {
			out.Steps[s] = v
		}
	}
	return out
}

func (w *World) validateOnboarding(res *Result) {
	o := w.Onboarding
	if o == nil {
		return
	}
	f := w.source["onboarding"]
	check := func(path, s string) {
		if len(s) > MaxOnboardingLine {
			res.warnf(f, path, "is %d characters; keep it under %d", len(s), MaxOnboardingLine)
		}
	}
	check("onboarding.welcome", o.Welcome)
	check("onboarding.finish", o.Finish)
	for _, k := range sortedKeys(o.Steps) {
		if !contains(OnboardingSteps, k) {
			res.errorf(f, "onboarding.steps."+k, "%q is not a step (%s)", k, strings.Join(OnboardingSteps, ", "))
			continue
		}
		check("onboarding.steps."+k, o.Steps[k])
	}
}
