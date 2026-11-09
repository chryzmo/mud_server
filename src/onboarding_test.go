package main

import "testing"

// The guided first steps (plans/event-systems.md, C1) through a real server.

const tourWorld = `
start_room: hall
onboarding:
  welcome: "Gatewarden: Welcome, traveller."
  steps:
    look: "Gatewarden: Try LOOK."
    move: "Gatewarden: Try going east."
    examine: "Gatewarden: Try EXAMINE LAMP."
    talk: "Gatewarden: Try TALK."
    say: "Gatewarden: Try SAY."
  finish: "Gatewarden: That is all. HELP, WHO, REPORT."
rooms:
  hall:
    name: Hall
    description: A hall.
    exits: {E: yard}
    features:
      lamp: {names: [lamp], examine: A brass lamp.}
    people:
      gatewarden: {names: [Gatewarden], examine: A keeper., greeting: "Well met."}
  yard:
    name: Yard
    description: A yard.
    exits: {W: hall}
`

func tourServer(t *testing.T) string {
	t.Helper()
	wsURL, _ := startTestServerWithConfig(t, withContent(contentDir(t, map[string]string{"world.yaml": tourWorld})))
	return wsURL
}

func TestANewPlayerIsOfferedTheWalkthroughAndEachStepBringsTheNext(t *testing.T) {
	t.Parallel()
	wsURL := tourServer(t)
	conn := eventPlayer(t, wsURL, "newcomer")
	expect(t, conn, "Gatewarden: Welcome, traveller.")
	expect(t, conn, "Gatewarden: Try LOOK.")

	send(t, conn, "LOOK")
	expect(t, conn, "Gatewarden: Try going east.")
	send(t, conn, "MOVE E")
	expect(t, conn, "Gatewarden: Try EXAMINE LAMP.")
	send(t, conn, "MOVE W")
	send(t, conn, "EXAMINE lamp")
	expect(t, conn, "Gatewarden: Try TALK.")
	send(t, conn, "TALK")
	expect(t, conn, "Gatewarden: Try SAY.")
	send(t, conn, "SAY hello")
	expect(t, conn, "Gatewarden: That is all.")

	send(t, conn, "TOUR")
	expect(t, conn, "You have finished the walkthrough.")
}

func TestStepsDoneOutOfOrderStayQuietAndAreSkippedLater(t *testing.T) {
	t.Parallel()
	wsURL := tourServer(t)
	conn := eventPlayer(t, wsURL, "eager")
	expect(t, conn, "Gatewarden: Try LOOK.")

	send(t, conn, "SAY first") // not the step being nudged
	send(t, conn, "FROBNICATE")
	neverSaw(t, readUntil(t, conn, "Unknown command."), "Gatewarden:")
	send(t, conn, "LOOK")
	expect(t, conn, "Gatewarden: Try going east.")
	send(t, conn, "EXAMINE lamp")
	send(t, conn, "TALK")
	send(t, conn, "MOVE E")
	expect(t, conn, "Gatewarden: That is all.") // say, examine and talk were done already
}

func TestAFailedMoveOrExamineDoesNotCountAsAStep(t *testing.T) {
	t.Parallel()
	wsURL := tourServer(t)
	conn := eventPlayer(t, wsURL, "clumsy")
	expect(t, conn, "Gatewarden: Try LOOK.")
	send(t, conn, "LOOK")
	expect(t, conn, "Gatewarden: Try going east.")
	send(t, conn, "MOVE N")
	send(t, conn, "EXAMINE dragon")
	send(t, conn, "TALK nobody")
	send(t, conn, "FROBNICATE")
	neverSaw(t, readUntil(t, conn, "Unknown command."), "Gatewarden:")
}

func TestSkipEndsItAndStartTakesItAgain(t *testing.T) {
	t.Parallel()
	wsURL := tourServer(t)
	conn := eventPlayer(t, wsURL, "busy")
	expect(t, conn, "Gatewarden: Try LOOK.")
	send(t, conn, "TOUR SKIP")
	expect(t, conn, "Walkthrough ended.")
	send(t, conn, "LOOK")
	send(t, conn, "FROBNICATE")
	neverSaw(t, readUntil(t, conn, "Unknown command."), "Gatewarden:")
	send(t, conn, "TOUR SKIP")
	expect(t, conn, "No walkthrough is running.")
	send(t, conn, "TOUR START")
	expect(t, conn, "Gatewarden: Welcome, traveller.")
	expect(t, conn, "Gatewarden: Try LOOK.")
	send(t, conn, "TOUR")
	expect(t, conn, "Gatewarden: Try LOOK.")
	send(t, conn, "TOUR NONSENSE")
	expect(t, conn, "Usage: TOUR")
}

func TestTheWalkthroughIsOfferedOnceAndResumesAfterReconnecting(t *testing.T) {
	t.Parallel()
	wsURL := tourServer(t)
	conn := eventPlayer(t, wsURL, "returner")
	expect(t, conn, "Gatewarden: Try LOOK.")
	send(t, conn, "LOOK")
	expect(t, conn, "Gatewarden: Try going east.")
	waitForStateRows(t, "returner", 3) // offered, did-look, and the visited room
	conn.Close()

	again, _ := loginWhenFree(t, wsURL, "returner", "secret")
	send(t, again, "FROBNICATE")
	neverSaw(t, readUntil(t, again, "Unknown command."), "Welcome, traveller.")
	send(t, again, "TOUR")
	expect(t, again, "Gatewarden: Try going east.") // where they left off
}

func TestWithoutAnOnboardingBlockThereIsNoWalkthrough(t *testing.T) {
	t.Parallel()
	wsURL, _ := startTestServerWithConfig(t, withContent(contentDir(t, map[string]string{"world.yaml": "start_room: hall\nrooms:\n  hall:\n    name: Hall\n    description: A hall.\n"})))
	conn := eventPlayer(t, wsURL, "plain")
	send(t, conn, "TOUR")
	neverSaw(t, readUntil(t, conn, "There is no walkthrough here."), "TOUR SKIP")
}
