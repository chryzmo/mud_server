package main

import (
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// REPORT and the report queue (plans/event-systems.md track E1) through a real server.

func reportServer(t *testing.T) string {
	t.Helper()
	wsURL, _ := startTestServerWithConfig(t, withContent(progressWorld(t)))
	return wsURL
}

func TestAReportReachesTheGMWithTheTalkAroundIt(t *testing.T) {
	t.Parallel()
	wsURL := reportServer(t)
	gm := eventGM(t, wsURL, "boss")
	alice := eventPlayer(t, wsURL, "alice")
	bob := eventPlayer(t, wsURL, "bob")
	send(t, bob, "GLOBAL you are all terrible")
	expect(t, alice, "you are all terrible")
	send(t, bob, "SAY psst, alice")
	expect(t, alice, "psst, alice")

	send(t, alice, "REPORT bob rude in chat")
	expect(t, alice, "A GM will look at your report.")
	expect(t, gm, "[Report] alice reported bob: rude in chat")

	list := reportList(t, gm)
	if want := "alice reported bob: rude in chat"; !strings.Contains(list, want) {
		t.Fatalf("list = %q", list)
	}
	id := firstReportNumber(t, list)
	send(t, gm, "GM REPORTS "+id)
	shown := expect(t, gm, "Recent talk:")
	for _, want := range []string{"Reason: rude in chat", "[global] bob: you are all terrible", "bob: psst, alice"} {
		if !strings.Contains(shown, want) {
			t.Errorf("report is missing %q:\n%s", want, shown)
		}
	}
	send(t, gm, "GM REPORTS CLOSE "+id)
	expect(t, gm, "Closed report #"+id+".")
	send(t, gm, "GM REPORTS")
	expect(t, gm, "No open reports.")
	send(t, gm, "GM REPORTS CLOSE "+id)
	expect(t, gm, "No open report #"+id+".")
}

func TestReportsAreLimitedAndRefuseNonsense(t *testing.T) {
	t.Parallel()
	wsURL := reportServer(t)
	alice := eventPlayer(t, wsURL, "alice")
	send(t, alice, "REPORT")
	expect(t, alice, "Usage: REPORT <player>")
	send(t, alice, "REPORT alice")
	expect(t, alice, "You cannot report yourself.")
	for _, who := range []string{"one", "two", "three"} {
		send(t, alice, "REPORT "+who+" spam")
		expect(t, alice, "A GM will look at your report.")
	}
	send(t, alice, "REPORT four spam")
	expect(t, alice, "You have sent 3 reports in the last hour.")
}

func TestSeveralDifferentReportersMuteThePlayerForAWhile(t *testing.T) {
	t.Parallel()
	wsURL := reportServer(t)
	gm := eventGM(t, wsURL, "boss")
	bob := eventPlayer(t, wsURL, "bob")
	var reporters []string
	for _, n := range []string{"ann", "cy", "dee"} {
		reporters = append(reporters, n)
	}
	for i, n := range reporters {
		c := eventPlayer(t, wsURL, n)
		send(t, c, "REPORT bob harassing me")
		expect(t, c, "A GM will look at your report.")
		if i < 2 {
			send(t, bob, "SAY still free to talk")
			expect(t, bob, "You say:")
		}
	}
	expect(t, bob, "You have been muted for 10 minutes (3 players reported them).")
	expect(t, gm, "[Auto] bob muted for 10 minutes")
	send(t, bob, "SAY hello?")
	expect(t, bob, "You are muted for another")

	send(t, gm, "GM UNMUTE bob")
	expect(t, gm, "bob can speak again.")
	send(t, bob, "SAY thanks")
	expect(t, bob, "You say:")
}

func TestOneReporterRepeatingThemselvesDoesNotMuteAndAGMIsNeverAutoMuted(t *testing.T) {
	t.Parallel()
	wsURL := reportServer(t)
	gm := eventGM(t, wsURL, "boss")
	bob := eventPlayer(t, wsURL, "bob")
	ann := eventPlayer(t, wsURL, "ann")
	for i := 0; i < 3; i++ {
		send(t, ann, "REPORT bob again")
		expect(t, ann, "A GM will look at your report.")
	}
	send(t, bob, "SAY fine")
	expect(t, bob, "You say:")
	for _, n := range []string{"cy", "dee", "eve"} {
		c := eventPlayer(t, wsURL, n)
		send(t, c, "REPORT boss bossy")
		expect(t, c, "A GM will look at your report.")
	}
	send(t, gm, "SAY still here")
	expect(t, gm, "You say:")
}

// firstReportNumber pulls the "#12" out of a report list.
func firstReportNumber(t *testing.T, list string) string {
	t.Helper()
	i := strings.Index(list, "\n#")
	if i < 0 {
		t.Fatalf("no report number in %q", list)
	}
	j := i + 2
	for j < len(list) && list[j] >= '0' && list[j] <= '9' {
		j++
	}
	return list[i+2 : j]
}

// reportList asks for the open reports until one shows: a report is written a moment after REPORT answers.
func reportList(t *testing.T, gm *websocket.Conn) string {
	t.Helper()
	for i := 0; i < 20; i++ {
		send(t, gm, "GM REPORTS")
		out := expect(t, gm, "report") // "Open reports ..." or "No open reports."
		if strings.Contains(out, "Open reports") {
			return out
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the report never appeared")
	return ""
}
