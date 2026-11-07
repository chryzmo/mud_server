package mudbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

/*
The reconnect storm (plans/guest-sessions.md, capacity rule 5): many guests join
and play lightly, then a share of their connections is cut at once, and each bot
reconnects with a random delay and exponential backoff and resumes with its token
and frame count. RunStorm reports how long that took, how many came back with the
character kept or with a gap, and how many frames were replayed.

It also keeps counting through every reconnect exactly as a real client must:
every frame except hello, auth and error is counted, and after a resume the count
is reset to the reply's base. A second storm that reports gaps where the first
reported none means the counts drifted.
*/

// StormConfig describes one storm run. Names are Prefix plus five digits, so
// the prefix must leave room within the 16-character name limit.
type StormConfig struct {
	URL         string
	Count       int
	JoinWorkers int
	Prefix      string
	Interval    time.Duration // per-bot pause between light actions
	Before      time.Duration // play before the first storm
	Cycles      int           // storms to run; 0 just plays for Before (for a restart driven from outside)
	Between     time.Duration // play after each storm
	Fraction    float64       // share of bots cut in each storm, 0..1
	Jitter      time.Duration // reconnect delay is uniform in [0, Jitter), growing with each failed attempt
	GiveUp      time.Duration // a bot stops trying to come back after this long
	Timeout     time.Duration // per dial and handshake
	JoinTimeout time.Duration // whole admission phase
	AllowRemote bool
	Mix         string // what the bots do: "light" (SAY and a move, the default) or "verbs" (the full Phase 4 mix, see verbLines)
	Stay        bool   // with the verbs mix, walk to the east room once and stay there, so a crowd forms in one room
}

func (c StormConfig) validate() error {
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" || u.User != nil {
		return errors.New("URL must be ws:// or wss:// without embedded credentials")
	}
	if !c.AllowRemote && !isLoopback(u.Hostname()) {
		return errors.New("remote load target refused; explicit AllowRemote is required")
	}
	if c.Count < 1 || c.Count > 100000 || c.JoinWorkers < 1 || c.JoinWorkers > c.Count {
		return errors.New("count must be 1..100000 and join workers 1..count")
	}
	if !validPrefix.MatchString(c.Prefix) || len(c.Prefix)+5 > 16 {
		return errors.New("prefix must be alphanumeric/underscore and at most 11 characters, so names fit the 16-character limit")
	}
	if c.Mix != "" && c.Mix != "light" && c.Mix != "verbs" {
		return errors.New(`mix must be "light" or "verbs"`)
	}
	if c.Stay && c.Mix != "verbs" {
		return errors.New("stay needs the verbs mix")
	}
	if c.Interval < 250*time.Millisecond || c.Before < 0 || c.Between < 0 || c.Cycles < 0 || c.Cycles > 20 {
		return errors.New("interval must be >=250ms, durations non-negative, cycles 0..20")
	}
	if c.Cycles > 0 && (c.Fraction <= 0 || c.Fraction > 1) {
		return errors.New("fraction must be in (0,1] when cycles > 0")
	}
	if c.Jitter < 0 || c.GiveUp <= 0 || c.Timeout <= 0 || c.Timeout > time.Minute || c.JoinTimeout <= 0 {
		return errors.New("jitter must be non-negative; give-up, timeout (max 1m) and join timeout positive")
	}
	return nil
}

// StormCycle summarises the reconnects after one storm (or, for Unplanned, the
// connections that ended without the storm cutting them, such as a server restart).
type StormCycle struct {
	Dropped        int   `json:"dropped"`
	Resumed        int   `json:"resumed_character_kept"` // resumed with no gap
	ResumedWithGap int   `json:"resumed_with_gap"`
	Failed         int   `json:"failed"` // gave up, or the server refused the token
	Retries        int   `json:"retries"`
	RateLimited    int   `json:"rate_limited_replies"`
	ReplayedFrames int   `json:"replayed_frames"`
	P50MS          int64 `json:"p50_ms"` // from the cut to the resume reply, including the random delay
	P95MS          int64 `json:"p95_ms"`
	P99MS          int64 `json:"p99_ms"`
	MaxMS          int64 `json:"max_ms"`
	HandshakeP95MS int64 `json:"handshake_p95_ms"` // dial + hello + resume reply for the final, successful attempt
	HandshakeMaxMS int64 `json:"handshake_max_ms"`

	mu         sync.Mutex
	totals     []int64
	handshakes []int64
	pending    atomic.Int64
}

func (c *StormCycle) record(f func(*StormCycle)) {
	c.mu.Lock()
	f(c)
	c.mu.Unlock()
}

func (c *StormCycle) finish() {
	c.mu.Lock()
	defer c.mu.Unlock()
	pct := func(values []int64, p int) int64 {
		if len(values) == 0 {
			return 0
		}
		sorted := append([]int64(nil), values...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		return sorted[min(len(sorted)-1, (len(sorted)*p+99)/100-1)]
	}
	c.P50MS, c.P95MS, c.P99MS = pct(c.totals, 50), pct(c.totals, 95), pct(c.totals, 99)
	c.MaxMS = pct(c.totals, 100)
	c.HandshakeP95MS, c.HandshakeMaxMS = pct(c.handshakes, 95), pct(c.handshakes, 100)
}

type StormReport struct {
	Requested       int           `json:"requested"`
	Joined          int           `json:"joined"`
	JoinFailures    int           `json:"join_failures"`
	ActiveAtEnd     int64         `json:"active_at_end"`
	ActionsSent     int64         `json:"actions_sent"`
	AdmissionTime   time.Duration `json:"admission_ns"`
	Cycles          []*StormCycle `json:"cycles"`
	Unplanned       *StormCycle   `json:"unplanned_reconnects"`
	UnplannedDrops  int           `json:"unplanned_drops"`
	JoinRateLimited int           `json:"join_rate_limited_replies"` // refused by the server's join limit while joining; the bots waited and retried
	FirstErrors     []string      `json:"first_errors,omitempty"`

	// What the bots received over the whole run, to compare against the server's own counters.
	FramesReceived int64 `json:"frames_received"`
	BytesReceived  int64 `json:"bytes_received"`
	Announces      int64 `json:"announce_frames"`
	EventsFrames   int64 `json:"events_frames"`
	TurnedBack     int64 `json:"moves_refused_room_full"`
	QuietLimited   int64 `json:"lines_refused_quiet_room"`

	// One extra guest asks for STATS once a second and times the reply: how long a command waits behind the crowd.
	ProbeSamples int   `json:"probe_samples"`
	ProbeP50MS   int64 `json:"probe_p50_ms"`
	ProbeP95MS   int64 `json:"probe_p95_ms"`
	ProbeP99MS   int64 `json:"probe_p99_ms"`
	ProbeMaxMS   int64 `json:"probe_max_ms"`
}

type stormBot struct {
	cfg    *StormConfig
	index  int
	name   string
	token  string
	report *stormRun

	mu         sync.Mutex
	conn       *websocket.Conn
	online     bool
	cutting    bool // the storm is cutting the current connection; its reader ending is expected
	cycle      *StormCycle
	cutAt      time.Time
	frames     uint64 // counted frames processed; handed from each reader to the next via readerDone
	wake       chan struct{}
	readerDone chan struct{} // closed when the current connection's reader has finished
}

type stormRun struct {
	cfg        StormConfig
	unplanned  *StormCycle
	active     atomic.Int64
	actions    atomic.Int64
	drops      atomic.Int64
	limited    atomic.Int64 // rate_limited replies seen while joining
	frames     atomic.Int64 // every frame the bots read
	bytes      atomic.Int64
	announces  atomic.Int64
	events     atomic.Int64
	turnedBack atomic.Int64 // moves refused because the room was at capacity
	quieted    atomic.Int64 // lines refused because the room was quiet
	errMu      sync.Mutex
	errs       []string
}

func (r *stormRun) fail(format string, args ...any) {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	if len(r.errs) < 10 {
		r.errs = append(r.errs, fmt.Sprintf(format, args...))
	}
}

// counted reports whether a frame of this type counts toward the resume count.
func counted(typ string) bool { return typ != "hello" && typ != "auth" && typ != "error" }

// RunStorm joins Count guests, plays, runs the configured storms and returns
// what happened. It returns an error if admission failed, or any bot gave up or
// was refused its token, or bots were still offline at the end.
func RunStorm(ctx context.Context, cfg StormConfig) (*StormReport, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	run := &stormRun{cfg: cfg, unplanned: &StormCycle{}}
	report := &StormReport{Requested: cfg.Count, Unplanned: run.unplanned}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()

	// Admission.
	bots := make([]*stormBot, cfg.Count)
	joinCtx, cancelJoin := context.WithTimeout(runCtx, cfg.JoinTimeout)
	started := time.Now()
	jobs := make(chan int)
	var workers sync.WaitGroup
	var joinMu sync.Mutex
	for w := 0; w < cfg.JoinWorkers; w++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				b, err := run.join(joinCtx, index)
				joinMu.Lock()
				if err != nil {
					report.JoinFailures++
					run.fail("join %d: %v", index, err)
				} else {
					bots[index] = b
					report.Joined++
				}
				joinMu.Unlock()
			}
		}()
	}
feed:
	for index := 0; index < cfg.Count; index++ {
		select {
		case jobs <- index:
		case <-joinCtx.Done():
			joinMu.Lock()
			report.JoinFailures += cfg.Count - index
			joinMu.Unlock()
			break feed
		}
	}
	close(jobs)
	workers.Wait()
	cancelJoin()
	report.AdmissionTime = time.Since(started)
	report.JoinRateLimited = int(run.limited.Load())

	// Play.
	var wg sync.WaitGroup
	var probeLatencies []int64
	if cfg.Mix == "verbs" {
		wg.Add(1)
		go func() { defer wg.Done(); probeLatencies = run.probe(runCtx) }()
	}
	for _, b := range bots {
		if b == nil {
			continue
		}
		wg.Add(2)
		go func(b *stormBot) { defer wg.Done(); b.actions(runCtx) }(b)
		go func(b *stormBot) { defer wg.Done(); b.lifecycle(runCtx) }(b)
	}
	sleep := func(d time.Duration) bool {
		select {
		case <-time.After(d):
			return true
		case <-runCtx.Done():
			return false
		}
	}
	alive := sleep(cfg.Before)
	for i := 0; i < cfg.Cycles && alive; i++ {
		cycle := &StormCycle{}
		report.Cycles = append(report.Cycles, cycle)
		for _, b := range bots {
			if b != nil && inStorm(b.index, cfg.Fraction) {
				if b.cut(cycle) {
					cycle.Dropped++
					cycle.pending.Add(1)
				}
			}
		}
		// Wait for everyone cut to come back (or give up) before the next storm.
		for cycle.pending.Load() > 0 && alive {
			alive = sleep(50 * time.Millisecond)
		}
		cycle.finish()
		alive = alive && sleep(cfg.Between)
	}
	if cfg.Cycles == 0 {
		alive = alive && sleep(cfg.Between)
	}

	report.ActiveAtEnd = run.active.Load()
	report.ActionsSent = run.actions.Load()
	report.FramesReceived, report.BytesReceived = run.frames.Load(), run.bytes.Load()
	report.Announces, report.EventsFrames = run.announces.Load(), run.events.Load()
	report.TurnedBack, report.QuietLimited = run.turnedBack.Load(), run.quieted.Load()
	stop()
	for _, b := range bots {
		if b != nil {
			b.close()
		}
	}
	wg.Wait()
	report.ProbeSamples = len(probeLatencies)
	if len(probeLatencies) > 0 {
		sort.Slice(probeLatencies, func(i, j int) bool { return probeLatencies[i] < probeLatencies[j] })
		at := func(p int) int64 { return probeLatencies[min(len(probeLatencies)-1, (len(probeLatencies)*p+99)/100-1)] }
		report.ProbeP50MS, report.ProbeP95MS, report.ProbeP99MS, report.ProbeMaxMS = at(50), at(95), at(99), at(100)
	}
	run.unplanned.Dropped = int(run.drops.Load())
	run.unplanned.finish()
	report.UnplannedDrops = int(run.drops.Load())
	report.FirstErrors = run.errs

	if ctx.Err() != nil {
		return report, ctx.Err()
	}
	failed := run.unplanned.Failed
	for _, c := range report.Cycles {
		failed += c.Failed
	}
	if report.JoinFailures > 0 || failed > 0 || report.ActiveAtEnd != int64(report.Joined) {
		return report, errors.New("storm not survived: inspect join failures, failed reconnects and the active count at the end")
	}
	return report, nil
}

// inStorm picks which bots a storm cuts: a spread-out, repeatable share.
func inStorm(index int, fraction float64) bool {
	return float64((index*7919)%1000) < fraction*1000
}

func (r *stormRun) dial(ctx context.Context) (*websocket.Conn, time.Time, error) {
	dialCtx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{subprotocol}
	begun := time.Now()
	conn, _, err := dialer.DialContext(dialCtx, r.cfg.URL, nil)
	if err != nil {
		return nil, begun, err
	}
	conn.SetReadLimit(4 << 20)
	conn.SetReadDeadline(time.Now().Add(r.cfg.Timeout))
	conn.SetWriteDeadline(time.Now().Add(r.cfg.Timeout))
	return conn, begun, nil
}

// join dials, joins by name, and counts the frames that follow admission.
func (r *stormRun) join(ctx context.Context, index int) (*stormBot, error) {
	conn, _, err := r.dial(ctx)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			conn.Close()
		}
	}()
	b := &stormBot{cfg: &r.cfg, index: index, name: Name(r.cfg.Prefix, index), report: r, wake: make(chan struct{}, 1)}
	joinFrame := frame("join", map[string]string{"name": b.name})
	if err := conn.WriteMessage(websocket.TextMessage, joinFrame); err != nil {
		return nil, err
	}
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		f := parseFrame(raw)
		switch {
		case f.T == "error" && f.Code == "rate_limited":
			// The server's join limit: wait as told, within the admission budget, and ask again.
			r.limited.Add(1)
			if err := waitToRetry(ctx, f.RetryMS); err != nil {
				return nil, err
			}
			if err := conn.WriteMessage(websocket.TextMessage, joinFrame); err != nil {
				return nil, err
			}
		case f.T == "auth" && f.OK:
			b.token = f.Token
			if b.token == "" {
				return nil, errors.New("join succeeded without a token")
			}
			conn.SetReadDeadline(time.Time{})
			b.attach(conn)
			ok = true
			return b, nil
		case f.T == "auth":
			return nil, fmt.Errorf("join refused: %s", f.Code)
		case f.T == "hello":
		default:
			return nil, errors.New("unexpected frame before join reply: " + f.T)
		}
	}
}

// attach makes conn the bot's connection and starts reading it.
func (b *stormBot) attach(conn *websocket.Conn) {
	b.mu.Lock()
	b.conn = conn
	b.online = true
	b.cutting = false
	done := make(chan struct{})
	b.readerDone = done
	b.mu.Unlock()
	b.report.active.Add(1)
	go b.read(conn, done)
}

// read counts frames until the connection ends. If the storm did not cut it, that
// is an unplanned drop and the bot reconnects like any client would.
func (b *stormBot) read(conn *websocket.Conn, done chan struct{}) {
	defer close(done)
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		parsed := parseFrame(raw)
		typ := parsed.T
		if typ == "text" {
			switch {
			case strings.Contains(parsed.Text, "too crowded to go that way"):
				b.report.turnedBack.Add(1)
			case strings.Contains(parsed.Text, "too crowded to talk over"):
				b.report.quieted.Add(1)
			}
		}
		b.report.frames.Add(1)
		b.report.bytes.Add(int64(len(raw)))
		switch typ {
		case "announce":
			b.report.announces.Add(1)
		case "events":
			b.report.events.Add(1)
		}
		if counted(typ) {
			b.frames++ // one reader at a time owns the count; attach/resume hand it over
		}
	}
	b.mu.Lock()
	b.online = false
	wasCut := b.cutting
	still := b.conn == conn
	b.mu.Unlock()
	b.report.active.Add(-1)
	if still && !wasCut {
		b.report.drops.Add(1)
		b.wakeLifecycle()
	}
}

func (b *stormBot) wakeLifecycle() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// cut abruptly ends the bot's connection, as a lost signal would, and records
// the storm cycle that its reconnect belongs to. False if it was already offline.
func (b *stormBot) cut(cycle *StormCycle) bool {
	b.mu.Lock()
	if !b.online || b.conn == nil || b.cycle != nil {
		b.mu.Unlock()
		return false
	}
	b.cutting = true
	b.cycle = cycle
	b.cutAt = time.Now()
	conn := b.conn
	b.mu.Unlock()
	conn.UnderlyingConn().Close()
	b.wakeLifecycle()
	return true
}

func (b *stormBot) close() {
	b.mu.Lock()
	b.cutting = true // our own close is not a drop
	conn := b.conn
	b.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

// lifecycle reconnects whenever the connection ends.
func (b *stormBot) lifecycle(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.wake:
		}
		b.mu.Lock()
		cycle, cutAt, readerDone := b.cycle, b.cutAt, b.readerDone
		b.mu.Unlock()
		select { // the old reader must finish before its frame count is read
		case <-readerDone:
		case <-ctx.Done():
			return
		}
		unplanned := cycle == nil
		if unplanned {
			cycle, cutAt = b.report.unplanned, time.Now()
		}
		b.reconnect(ctx, cycle, cutAt, unplanned)
		b.mu.Lock()
		b.cycle = nil
		b.mu.Unlock()
		if !unplanned {
			cycle.pending.Add(-1)
		}
	}
}

// reconnect resumes with a random delay and exponential backoff until it works,
// the server refuses the token, or the bot gives up.
func (b *stormBot) reconnect(ctx context.Context, cycle *StormCycle, cutAt time.Time, unplanned bool) {
	jitter := func(window time.Duration) time.Duration {
		if window <= 0 {
			return 0
		}
		return time.Duration(rand.Int64N(int64(window)))
	}
	wait := jitter(b.cfg.Jitter)
	backoff := time.Second
	deadline := cutAt.Add(b.cfg.GiveUp)
	for attempt := 0; ; attempt++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if time.Now().After(deadline) {
			cycle.record(func(c *StormCycle) { c.Failed++ })
			b.report.fail("bot %d gave up after %v", b.index, b.cfg.GiveUp)
			return
		}
		retryAfter, done := b.resumeOnce(ctx, cycle, cutAt)
		if done {
			return
		}
		cycle.record(func(c *StormCycle) { c.Retries++ })
		if retryAfter > 0 {
			// The server said when to come back: do that, with a little jitter. Its hint
			// already spreads the crowd out, so this is not a failure to back off from.
			wait = retryAfter + jitter(min(b.cfg.Jitter, time.Second))
		} else {
			wait = backoff + jitter(b.cfg.Jitter)
			backoff = min(backoff*2, 10*time.Second)
		}
	}
}

// resumeOnce makes one attempt. done means the bot is back or has been refused for
// good; otherwise retryAfter is the server's hint (zero if none).
func (b *stormBot) resumeOnce(ctx context.Context, cycle *StormCycle, cutAt time.Time) (retryAfter time.Duration, done bool) {
	conn, begun, err := b.report.dial(ctx)
	if err != nil {
		return 0, false
	}
	keep := false
	defer func() {
		if !keep {
			conn.Close()
		}
	}()

	received := b.frames // the previous reader has finished, so this is stable
	send := frameAny("resume", map[string]any{"token": b.token, "received": received})
	if err := conn.WriteMessage(websocket.TextMessage, send); err != nil {
		return 0, false
	}
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return 0, false
		}
		f := parseFrame(raw)
		switch {
		case f.T == "hello":
		case f.T == "auth" && f.OK:
			b.frames = f.Base // the count before any replayed frames
			conn.SetReadDeadline(time.Time{})
			now := time.Now()
			cycle.record(func(c *StormCycle) {
				if f.Gap {
					c.ResumedWithGap++
				} else {
					c.Resumed++
				}
				c.ReplayedFrames += f.Replayed
				c.totals = append(c.totals, now.Sub(cutAt).Milliseconds())
				c.handshakes = append(c.handshakes, now.Sub(begun).Milliseconds())
			})
			keep = true
			b.attach(conn)
			return 0, true
		case f.T == "auth":
			cycle.record(func(c *StormCycle) { c.Failed++ })
			b.report.fail("bot %d refused: %s", b.index, f.Code)
			return 0, true
		case f.T == "error" && f.Code == "rate_limited":
			cycle.record(func(c *StormCycle) { c.RateLimited++ })
			return time.Duration(f.RetryMS) * time.Millisecond, false
		case f.T == "error":
			return 0, false
		}
	}
}

// actions plays lightly so there is traffic to replay: a little talk and walking
// between the start room and the balcony.
func (b *stormBot) actions(ctx context.Context) {
	lines := []string{"SAY storm", "MOVE N", "SAY storm", "MOVE S"}
	first := ""
	if b.cfg.Mix == "verbs" {
		first, lines = verbLines(b.index, b.cfg.Stay)
	}
	timer := time.NewTimer(time.Duration(b.index%1000) * b.cfg.Interval / 1000)
	defer timer.Stop()
	for sequence := 0; ; sequence++ {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		b.mu.Lock()
		conn, online := b.conn, b.online
		b.mu.Unlock()
		if online && conn != nil {
			line := stormLine(lines[sequence%len(lines)], sequence)
			if sequence == 0 && first != "" {
				line = first
			}
			conn.SetWriteDeadline(time.Now().Add(b.cfg.Timeout))
			if conn.WriteMessage(websocket.TextMessage, frame("cmd", map[string]string{"line": line})) == nil {
				b.report.actions.Add(1)
			}
		}
		timer.Reset(b.cfg.Interval)
	}
}

// frameAny is frame for payloads with numbers.
func frameAny(t string, fields map[string]any) []byte {
	m := map[string]any{"t": t}
	for k, v := range fields {
		m[k] = v
	}
	out, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return out
}

// verbLines is one bot's turn through the verbs a Phase 4 guest uses: look around, examine, talk to the
// person, emote, a ready-made social, WHO, say something, and (unless staying) a walk to the next room and
// back. The order is rotated by bot so a room does not all do the same thing at once. With stay the bot
// first walks east, once, and then only talks and looks, so a crowd forms in that room.
func verbLines(index int, stay bool) (first string, cycle []string) {
	cycle = []string{"LOOK", "EXAMINE LAMP", "TALK", "EMOTE waves", "WAVE", "WHO", "SAY hello", "SAY well met"}
	if stay {
		first = "MOVE E"
	} else {
		cycle = append(cycle, "MOVE E", "LOOK", "MOVE W")
	}
	by := index % len(cycle)
	return first, append(append([]string{}, cycle[by:]...), cycle[:by]...)
}

// stormLine makes a SAY unique by its sequence number: the server mutes a message repeated four times a minute.
func stormLine(line string, sequence int) string {
	if strings.HasPrefix(line, "SAY ") || strings.HasPrefix(line, "EMOTE ") {
		return fmt.Sprintf("%s %d", line, sequence)
	}
	return line
}

// probe is one more guest that joins after the crowd and, once a second, sends STATS and times the stats
// frame that comes back, until ctx ends. It returns the latencies in milliseconds. A probe that cannot
// join returns nothing, which shows in the report as no samples.
func (r *stormRun) probe(ctx context.Context) []int64 {
	conn, _, err := r.dial(ctx)
	if err != nil {
		r.fail("probe dial: %v", err)
		return nil
	}
	defer conn.Close()
	name := Name(r.cfg.Prefix, r.cfg.Count)
	if err := conn.WriteMessage(websocket.TextMessage, frame("join", map[string]string{"name": name})); err != nil {
		return nil
	}
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return nil
		}
		if f := parseFrame(raw); f.T == "auth" {
			if !f.OK {
				r.fail("probe refused: %s", f.Code)
				return nil
			}
			break
		}
	}
	conn.SetReadDeadline(time.Time{})
	replies := make(chan struct{}, 1)
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if parseFrame(raw).T == "stats" {
				select {
				case replies <- struct{}{}:
				default:
				}
			}
		}
	}()
	var out []int64
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return out
		case <-tick.C:
		}
		for len(replies) > 0 {
			<-replies
		}
		sent := time.Now()
		conn.SetWriteDeadline(time.Now().Add(r.cfg.Timeout))
		if conn.WriteMessage(websocket.TextMessage, frame("cmd", map[string]string{"line": "STATS"})) != nil {
			return out
		}
		select {
		case <-replies:
			out = append(out, time.Since(sent).Milliseconds())
		case <-time.After(r.cfg.Timeout):
			out = append(out, r.cfg.Timeout.Milliseconds())
		case <-ctx.Done():
			return out
		}
	}
}
