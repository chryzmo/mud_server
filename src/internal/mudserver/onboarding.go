package mudserver

import (
	"strings"

	"mud_server/src/internal/worldfile"
	muddata "mud_server/src/internal/muddata"
)

/*
Guided first steps (plans/event-systems.md, C1). When the content carries an onboarding: block a
brand-new player is offered a short walkthrough on arrival; each step done (LOOK, a move, EXAMINE,
TALK, SAY) brings the nudge for the next one. It is driven only by the player's own flags, so it
survives a reconnect, sends nothing on a timer, and never gates anything: TOUR SKIP ends it at any
time and TOUR START takes it again. A world without the block has none of this.
*/

const (
	flagTourOffered = "tour-offered" // the welcome was shown
	flagTourDone    = "tour-done"    // finished or skipped
	flagTourDidPre  = "tour-did-"    // plus a step name
)

// tourOn reports whether this world has a walkthrough. Loop only.
func (mp *MudPlayer) tourOn() bool { return mp.world != nil && mp.world.onboarding != nil }

// tourActive is true while the walkthrough is offered and neither finished nor skipped.
func (mp *MudPlayer) tourActive() bool {
	return mp.tourOn() && mp.flag(flagTourOffered) && !mp.flag(flagTourDone)
}

// tourNext is the first step not done yet, or "" when all are.
func (mp *MudPlayer) tourNext() string {
	for _, s := range worldfile.OnboardingSteps {
		if !mp.flag(flagTourDidPre + s) {
			return s
		}
	}
	return ""
}

func (mp *MudPlayer) tourNudge(step string) {
	mp.SendMessage(mp.world.onboarding.Steps[step])
}

// tourStart shows the welcome and the first nudge. Loop only.
func (mp *MudPlayer) tourStart() {
	if !mp.tourOn() {
		return
	}
	mp.setFlag(flagTourOffered)
	mp.SendMessage(strings.TrimSpace(mp.world.onboarding.Welcome) + "\n(TOUR SKIP ends the walkthrough at any time.)")
	if next := mp.tourNext(); next != "" {
		mp.tourNudge(next)
	}
}

// tourFresh is true for a player who has never been anywhere: ask before their first room is
// counted as visited. Loop only.
func (mp *MudPlayer) tourFresh() bool {
	return mp.tourOn() && len(mp.st().visited) == 0 && !mp.flag(flagTourOffered)
}

// tourDid records that the player did a step. It speaks only when the step done was the one
// being nudged, so doing things out of order stays quiet. Loop only.
func (mp *MudPlayer) tourDid(step string) {
	if !mp.tourActive() || mp.flag(flagTourDidPre+step) {
		return
	}
	was := mp.tourNext()
	mp.setFlag(flagTourDidPre + step)
	next := mp.tourNext()
	switch {
	case next == "":
		mp.setFlag(flagTourDone)
		mp.SendMessage(strings.TrimSpace(mp.world.onboarding.Finish))
	case was == step:
		mp.tourNudge(next)
	}
}

func (mp *MudPlayer) tour(arguments string) {
	if !mp.tourOn() {
		mp.SendMessage("There is no walkthrough here.")
		return
	}
	switch strings.ToUpper(strings.TrimSpace(arguments)) {
	case "":
		switch {
		case mp.tourActive():
			mp.tourNudge(mp.tourNext())
			mp.SendMessage("(TOUR SKIP ends the walkthrough.)")
		case mp.flag(flagTourOffered):
			mp.SendMessage("You have finished the walkthrough. TOUR START takes it again.")
		default:
			mp.tourStart()
		}
	case "SKIP", "OFF", "STOP":
		if !mp.tourActive() {
			mp.SendMessage("No walkthrough is running. TOUR START begins it.")
			return
		}
		mp.setFlag(flagTourDone)
		mp.SendMessage("Walkthrough ended. TOUR START brings it back.")
	case "START", "ON", "RESTART":
		for _, s := range worldfile.OnboardingSteps {
			mp.clearFlag(flagTourDidPre + s)
		}
		mp.clearFlag(flagTourDone)
		mp.clearFlag(flagTourOffered)
		mp.tourStart()
	default:
		mp.SendMessage("Usage: TOUR, TOUR SKIP or TOUR START")
	}
}

func registerTour(cList map[string]*MudCommand) {
	cList["TOUR"] = &MudCommand{
		description: "The guided walkthrough for new players.  Usage: TOUR (the next step), TOUR SKIP, TOUR START",
		action: func(player *MudPlayer, arguments string) int {
			player.tour(arguments)
			return muddata.MUD_OK
		},
	}
}
