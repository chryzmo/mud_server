package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	mudbot "mud_server/src/internal/mudbot"
	mudserver "mud_server/src/internal/mudserver"
)

func joinLimit(perSecond, burst int) func(*mudserver.Configuration) {
	return func(c *mudserver.Configuration) {
		c.Connection.JoinPerSecond, c.Connection.JoinBurst = perSecond, burst
	}
}

// tryJoin sends a join on a fresh connection and returns whatever answers.
func tryJoin(t *testing.T, wsURL, name string) map[string]any {
	t.Helper()
	conn := dial(t, wsURL)
	expect(t, conn, "Welcome to the MUD!")
	writeRaw(t, conn, fmt.Sprintf(`{"t":"join","name":%q}`, name))
	for {
		typ, m := readMessage(t, conn)
		if typ == "auth" || typ == "error" {
			return m
		}
	}
}

func TestJoinLimitRefusesPastTheBurstWithoutCreatingAnything(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, joinLimit(2, 3))
	var refused []string
	ok := 0
	for i := 0; i < 8; i++ {
		name := fmt.Sprintf("Crowd%d", i)
		got := tryJoin(t, wsURL, name)
		switch {
		case got["ok"] == true:
			ok++
		case got["code"] == "rate_limited":
			if retry, _ := got["retry_ms"].(float64); retry <= 0 {
				t.Errorf("%s: refusal without a retry hint: %v", name, got)
			}
			refused = append(refused, name)
		default:
			t.Fatalf("%s: unexpected answer %v", name, got)
		}
	}
	if ok < 3 || ok > 4 || len(refused) < 4 {
		t.Fatalf("admitted %d, refused %d of 8 with a burst of 3 at 2/s", ok, len(refused))
	}
	// A refused name was never touched: it is free, and joins once the bucket refills.
	if n := queryString(t, fmt.Sprintf("SELECT count(*)::text FROM player WHERE username='%s'", refused[0])); n != "0" {
		t.Errorf("a refused join created %s rows", n)
	}
	time.Sleep(1200 * time.Millisecond)
	if got := tryJoin(t, wsURL, refused[0]); got["ok"] != true {
		t.Errorf("after waiting, %s could not join: %v", refused[0], got)
	}
}

// The limit protects every way in, before any database work: a resume with a bad
// token gets rate_limited, not bad_token, once the bucket is spent.
func TestJoinLimitCoversResumeRecoverLoginAndRegister(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, joinLimit(1, 2))
	tryJoin(t, wsURL, "Spender1")
	tryJoin(t, wsURL, "Spender2") // the bucket is now empty

	for name, frame := range map[string]string{
		"resume":   `{"t":"resume","token":"` + fmt.Sprintf("%043d", 7) + `","received":0}`,
		"recover":  `{"t":"recover","name":"Spender1","code":"AAAA-BBBB-CCCC"}`,
		"login":    `{"t":"login","username":"c","password":"x"}`,
		"register": `{"t":"register","username":"Newcomer","password":"secret"}`,
	} {
		conn := dial(t, wsURL)
		expect(t, conn, "Welcome to the MUD!")
		writeRaw(t, conn, frame)
		typ, m := readMessage(t, conn)
		if typ != "error" || m["code"] != "rate_limited" {
			t.Errorf("%s with an empty bucket = %s %v, want a rate_limited error", name, typ, m)
		}
	}
}

// Messages that never reach the database do not spend the shared bucket: someone
// already in the game, or sending a bad request, cannot starve everyone else.
func TestJoinLimitIsNotSpentByRequestsThatDoNoWork(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		joinLimit(1, 2)(c)
		c.Connection.CommandsPerSecond, c.Connection.CommandBurst = 1000, 1000
	})
	g := joinGuest(t, wsURL, "Insider") // one token spent, one left
	for i := 0; i < 30; i++ {
		writeRaw(t, g.conn, `{"t":"join","name":"Another"}`) // already in the game
	}
	writeRaw(t, g.conn, `{"t":"cmd","line":"LOOK"}`)
	g.until(func(typ string, m map[string]any) bool { return typ == "room" })

	other := dial(t, wsURL)
	expect(t, other, "Welcome to the MUD!")
	for i := 0; i < 30; i++ {
		writeRaw(t, other, `{"t":"join"}`) // no name: a bad request
	}
	if got := tryJoin(t, wsURL, "StillAllowed"); got["ok"] != true {
		t.Errorf("a real join was refused after others only sent requests that do no work: %v", got)
	}
}

func TestJoinLimitCanBeDisabled(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, joinLimit(-1, 1))
	for i := 0; i < 30; i++ {
		if got := tryJoin(t, wsURL, fmt.Sprintf("Free%02d", i)); got["ok"] != true {
			t.Fatalf("join %d with no limit: %v", i, got)
		}
	}
}

// Bots obey the retry hint while joining and while resuming: with a tight limit
// every guest still gets in, every cut guest still comes back, and the report
// says how often the server said wait.
func TestStormBotsWaitOutTheJoinLimit(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		lotsOfTalk(c)
		joinLimit(80, 10)(c)
		c.Connection.ResumeGraceMS = 20000
	})
	cfg := stormConfig(wsURL, 50)
	cfg.Cycles, cfg.Fraction, cfg.Jitter = 1, 1, 0 // everyone cut at once, nobody waits before redialling
	cfg.GiveUp, cfg.Between = 30*time.Second, 500*time.Millisecond
	report, err := mudbot.RunStorm(context.Background(), cfg)
	if err != nil {
		t.Fatalf("storm: %v (%v)", err, report.FirstErrors)
	}
	c := report.Cycles[0]
	t.Logf("join refusals: %d; storm: %s", report.JoinRateLimited, asJSON(c))
	if report.Joined != 50 || report.JoinFailures != 0 {
		t.Errorf("joined %d, failures %d; want all 50 in despite the limit", report.Joined, report.JoinFailures)
	}
	if report.JoinRateLimited == 0 {
		t.Error("the join limit never said wait, so this test proved nothing")
	}
	// While the cut bots waited, the ones already back kept chatting, which can overflow a
	// dormant guest's replay ring: a gap is fine, a lost character is not.
	if c.Dropped != 50 || c.Failed != 0 || c.Resumed+c.ResumedWithGap != 50 || c.RateLimited == 0 {
		t.Errorf("storm: dropped %d, back %d, failed %d, told to wait %d times; want all 50 back after waiting",
			c.Dropped, c.Resumed+c.ResumedWithGap, c.Failed, c.RateLimited)
	}
	if c.MaxMS > 8000 {
		t.Errorf("the slowest guest took %d ms to come back; waiting out a limit of 80/s for 50 guests should take about a second", c.MaxMS)
	}
}
