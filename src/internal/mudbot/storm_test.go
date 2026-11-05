package mudbot

import (
	"strings"
	"testing"
	"time"
)

func goodStormConfig() StormConfig {
	return StormConfig{
		URL: "ws://127.0.0.1:8080/ws", Count: 10, JoinWorkers: 2, Prefix: "stormbot", Interval: time.Second,
		Before: time.Second, Cycles: 1, Between: time.Second, Fraction: 0.5, Jitter: time.Second,
		GiveUp: time.Minute, Timeout: 5 * time.Second, JoinTimeout: time.Minute,
	}
}

func TestStormConfigValidation(t *testing.T) {
	if err := goodStormConfig().validate(); err != nil {
		t.Fatalf("good config refused: %v", err)
	}
	for name, mutate := range map[string]func(*StormConfig){
		"remote host":           func(c *StormConfig) { c.URL = "ws://example.com/ws" },
		"not a websocket":       func(c *StormConfig) { c.URL = "http://127.0.0.1/ws" },
		"credentials in URL":    func(c *StormConfig) { c.URL = "ws://user:pw@127.0.0.1/ws" },
		"no bots":               func(c *StormConfig) { c.Count = 0 },
		"too many workers":      func(c *StormConfig) { c.JoinWorkers = 11 },
		"prefix too long":       func(c *StormConfig) { c.Prefix = "twelvecharsx" }, // 12 + 5 digits > 16
		"prefix has a dash":     func(c *StormConfig) { c.Prefix = "bad-prefix" },
		"interval too small":    func(c *StormConfig) { c.Interval = 100 * time.Millisecond },
		"too many cycles":       func(c *StormConfig) { c.Cycles = 21 },
		"fraction zero":         func(c *StormConfig) { c.Fraction = 0 },
		"fraction above one":    func(c *StormConfig) { c.Fraction = 1.5 },
		"negative jitter":       func(c *StormConfig) { c.Jitter = -time.Second },
		"no give-up":            func(c *StormConfig) { c.GiveUp = 0 },
		"timeout over a minute": func(c *StormConfig) { c.Timeout = 2 * time.Minute },
	} {
		c := goodStormConfig()
		mutate(&c)
		if err := c.validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	remote := goodStormConfig()
	remote.URL, remote.AllowRemote = "wss://example.com/ws", true
	if err := remote.validate(); err != nil {
		t.Errorf("an explicit remote opt-in was refused: %v", err)
	}
	restart := goodStormConfig()
	restart.Cycles, restart.Fraction = 0, 0 // a restart driven from outside needs no storms
	if err := restart.validate(); err != nil {
		t.Errorf("zero cycles refused: %v", err)
	}
}

func TestStormNamesFitTheGuestNameLimit(t *testing.T) {
	c := goodStormConfig()
	c.Prefix = strings.Repeat("a", 11)
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	if name := Name(c.Prefix, 99999); len(name) != 16 {
		t.Errorf("longest name %q is %d characters, want exactly the 16-character limit", name, len(name))
	}
}

func TestInStormPicksARepeatableSpreadShare(t *testing.T) {
	for _, fraction := range []float64{0.1, 0.25, 0.5, 1} {
		picked := 0
		const bots = 5000
		for i := 0; i < bots; i++ {
			if inStorm(i, fraction) != inStorm(i, fraction) {
				t.Fatal("selection must be repeatable")
			}
			if inStorm(i, fraction) {
				picked++
			}
		}
		if got := float64(picked) / bots; got < fraction-0.02 || got > fraction+0.02 {
			t.Errorf("fraction %.2f picked %.3f of %d bots", fraction, got, bots)
		}
	}
	if inStorm(7, 0) {
		t.Error("fraction 0 picked a bot")
	}
}

func TestStormCycleFinishComputesPercentiles(t *testing.T) {
	var c StormCycle
	for i := int64(1); i <= 100; i++ {
		c.totals = append(c.totals, i)
		c.handshakes = append(c.handshakes, i*2)
	}
	c.finish()
	if c.P50MS != 50 || c.P95MS != 95 || c.P99MS != 99 || c.MaxMS != 100 || c.HandshakeP95MS != 190 || c.HandshakeMaxMS != 200 {
		t.Errorf("percentiles = %d/%d/%d max %d; handshake %d max %d", c.P50MS, c.P95MS, c.P99MS, c.MaxMS, c.HandshakeP95MS, c.HandshakeMaxMS)
	}
	var empty StormCycle
	empty.finish()
	if empty.P95MS != 0 || empty.MaxMS != 0 {
		t.Error("an empty cycle must report zeros, not panic")
	}
}

func TestCountedFramesAreEverythingButHelloAuthAndError(t *testing.T) {
	for typ, want := range map[string]bool{
		"hello": false, "auth": false, "error": false,
		"text": true, "say": true, "arrive": true, "leave": true, "whisper": true, "stats": true, "room": true, "inventory": true,
	} {
		if counted(typ) != want {
			t.Errorf("counted(%q) = %v, want %v", typ, counted(typ), want)
		}
	}
}

func TestParseFrameReadsResumeReplies(t *testing.T) {
	f := parseFrame([]byte(`{"t":"auth","ok":true,"resumed":true,"gap":true,"replayed":3,"base":42,"token":"tok","recovery":"R"}`))
	if !f.OK || !f.Resumed || !f.Gap || f.Replayed != 3 || f.Base != 42 || f.Token != "tok" || f.Recovery != "R" {
		t.Errorf("parsed = %+v", f)
	}
	if g := parseFrame([]byte(`{"t":"error","code":"rate_limited","retry_ms":1500}`)); g.RetryMS != 1500 || g.Code != "rate_limited" {
		t.Errorf("parsed = %+v", g)
	}
}
