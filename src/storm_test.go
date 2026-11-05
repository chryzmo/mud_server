package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	mudbot "mud_server/src/internal/mudbot"
	mudserver "mud_server/src/internal/mudserver"
)

func stormConfig(wsURL string, count int) mudbot.StormConfig {
	return mudbot.StormConfig{
		URL: wsURL, Count: count, JoinWorkers: 4, Prefix: "storm", Interval: 250 * time.Millisecond,
		Before: time.Second, Cycles: 2, Between: time.Second, Fraction: 0.5, Jitter: 500 * time.Millisecond,
		GiveUp: 20 * time.Second, Timeout: 5 * time.Second, JoinTimeout: 60 * time.Second,
	}
}

func lotsOfTalk(c *mudserver.Configuration) {
	c.Connection.SayPerSecond, c.Connection.SayBurst = 1000, 1000
	c.Connection.CommandsPerSecond, c.Connection.CommandBurst = 1000, 1000
}

// A storm inside the grace period: everyone cut comes back with the character
// kept, in both storms, which also proves the frame counts stay consistent
// through repeated reconnects (a drift would show up as gaps in storm two).
func TestReconnectStormWithinGraceKeepsEveryCharacter(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		lotsOfTalk(c)
		c.Connection.ResumeGraceMS = 20000
	})
	report, err := mudbot.RunStorm(context.Background(), stormConfig(wsURL, 40))
	t.Logf("report: joined=%d cycles=%d unplanned=%d active=%d", report.Joined, len(report.Cycles), report.UnplannedDrops, report.ActiveAtEnd)
	if err != nil {
		t.Fatalf("storm: %v (first errors: %v)", err, report.FirstErrors)
	}
	if report.Joined != 40 || report.ActiveAtEnd != 40 || report.UnplannedDrops != 0 {
		t.Errorf("joined %d, active at end %d, unplanned drops %d; want 40, 40, 0", report.Joined, report.ActiveAtEnd, report.UnplannedDrops)
	}
	replayed := 0
	for i, c := range report.Cycles {
		t.Logf("storm %d: %s", i+1, asJSON(c))
		if c.Dropped < 10 || c.Resumed != c.Dropped || c.ResumedWithGap != 0 || c.Failed != 0 {
			t.Errorf("storm %d: dropped %d, resumed with the character kept %d, with a gap %d, failed %d; want everyone back, no gaps",
				i+1, c.Dropped, c.Resumed, c.ResumedWithGap, c.Failed)
		}
		replayed += c.ReplayedFrames
	}
	if replayed == 0 {
		t.Error("no frames were replayed, though half the room kept talking while the other half was away")
	}
}

// A storm longer than the grace period: characters were removed, so each resume
// is an ordinary arrival, reported as a gap, and nobody is lost.
func TestReconnectStormPastGraceResumesWithAGap(t *testing.T) {
	wsURL, _ := startTestServerWithConfig(t, func(c *mudserver.Configuration) {
		lotsOfTalk(c)
		c.Connection.ResumeGraceMS = 150
	})
	cfg := stormConfig(wsURL, 30)
	cfg.Cycles, cfg.Fraction, cfg.Jitter, cfg.Between = 1, 1, 1500*time.Millisecond, 500*time.Millisecond
	report, err := mudbot.RunStorm(context.Background(), cfg)
	if err != nil {
		t.Fatalf("storm: %v (%v)", err, report.FirstErrors)
	}
	c := report.Cycles[0]
	t.Logf("storm: %s", asJSON(c))
	if c.Dropped != 30 || c.Failed != 0 || c.Resumed+c.ResumedWithGap != 30 {
		t.Errorf("dropped %d, back %d, failed %d; want all 30 back", c.Dropped, c.Resumed+c.ResumedWithGap, c.Failed)
	}
	if c.ResumedWithGap < 15 {
		t.Errorf("only %d of 30 resumed with a gap after a 1.5 s jitter against a 150 ms grace period", c.ResumedWithGap)
	}
}

// listenAt starts handler on addr and returns a stop function that closes the
// listener and every connection, like a process exiting.
func listenAt(t *testing.T, addr string, handler http.Handler) (string, func()) {
	t.Helper()
	var ln net.Listener
	var err error
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if ln, err = net.Listen("tcp", addr); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("listen %s: %v", addr, err)
		}
	}
	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)
	return ln.Addr().String(), func() { srv.Close() }
}

// The real storm: the server process goes away and a new one starts on the same
// address and data. Every guest's connection drops unplanned, redials with
// backoff while the server is down, and resumes into the new process as an
// ordinary arrival with a gap.
func TestServerRestartStorm(t *testing.T) {
	url := testDatabaseURL(t)
	resetDatabase(t, url)
	configure := func(c *mudserver.Configuration) { lotsOfTalk(c); c.Connection.ResumeGraceMS = 20000 }

	handlerFor := func(si *mudserver.MudServer) http.Handler {
		mux := http.NewServeMux()
		mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) { handleConnections(si, w, r) })
		return mux
	}
	build := func() *mudserver.MudServer {
		c := mudserver.Configuration{}
		c.Database.DatabaseURL = url
		c.Game.RoomTickDurationInMS = 250
		c.Game.PlayerStartHP, c.Game.PlayerStartMaxHP = 20, 20
		c.Game.PlayerStartMP, c.Game.PlayerStartMaxMP = 10, 10
		c.Game.PlayerStartAttack, c.Game.PlayerStartDefense = 5, 10
		configure(&c)
		si, err := mudserver.NewMudServer(c)
		if err != nil {
			t.Fatal(err)
		}
		return si
	}

	serverA := build()
	addr, stopA := listenAt(t, "127.0.0.1:0", handlerFor(serverA))
	wsURL := "ws://" + addr + "/ws"

	cfg := stormConfig(wsURL, 25)
	cfg.Cycles, cfg.Before, cfg.Between = 0, 500*time.Millisecond, 8*time.Second
	cfg.Jitter = 400 * time.Millisecond

	type result struct {
		report *mudbot.StormReport
		err    error
	}
	done := make(chan result, 1)
	go func() {
		r, err := mudbot.RunStorm(context.Background(), cfg)
		done <- result{r, err}
	}()

	time.Sleep(2 * time.Second) // everyone has joined and played for a bit
	stopA()
	serverA.Shutdown(context.Background())
	time.Sleep(600 * time.Millisecond) // the server is down; bots are redialling
	serverB := build()
	_, stopB := listenAt(t, addr, handlerFor(serverB))
	t.Cleanup(func() { stopB(); serverB.Shutdown(context.Background()) })

	var res result
	select {
	case res = <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("the storm run did not finish")
	}
	u := res.report.Unplanned
	t.Logf("restart storm: %s", asJSON(u))
	if res.err != nil {
		t.Fatalf("storm: %v (%v)", res.err, res.report.FirstErrors)
	}
	if res.report.UnplannedDrops != 25 || u.ResumedWithGap != 25 || u.Resumed != 0 || u.Failed != 0 {
		t.Errorf("unplanned drops %d, resumed with a gap %d, with the character kept %d, failed %d; want 25, 25, 0, 0 (a new process remembers nobody)",
			res.report.UnplannedDrops, u.ResumedWithGap, u.Resumed, u.Failed)
	}
	if res.report.ActiveAtEnd != 25 {
		t.Errorf("active at end %d, want 25", res.report.ActiveAtEnd)
	}
	if u.Retries == 0 {
		t.Error("no retries, so the bots never saw the server down")
	}
	// All 25 characters are back in the new process.
	if n := queryString(t, "SELECT count(*)::text FROM player WHERE guest"); n != "25" {
		t.Errorf("guest rows = %s, want 25 and no duplicates", n)
	}
}

func asJSON(v any) string {
	out, _ := json.Marshal(v)
	return string(out)
}
