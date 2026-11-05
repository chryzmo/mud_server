// Package mudbot drives the public websocket protocol without accessing game state.
package mudbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// subprotocol is the websocket subprotocol that carries the protocol version.
const subprotocol = "mud.v1"

// frame builds a flat client message: {"t":t, ...fields}.
func frame(t string, fields map[string]string) []byte {
	m := map[string]any{"t": t}
	for k, v := range fields {
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return b
}

// inFrame is the part of a server message the bot looks at. Text is the game
// text it matches against: set for text messages, and for the sender's own
// whisper echo, rebuilt in the wording the fence matches.
type inFrame struct {
	T    string
	OK   bool
	Code string
	Text string
}

func parseFrame(message []byte) inFrame {
	var msg struct {
		T    string `json:"t"`
		OK   bool   `json:"ok"`
		Code string `json:"code"`
		Text string `json:"text"`
		Who  string `json:"who"`
		Out  bool   `json:"out"`
	}
	if json.Unmarshal(message, &msg) != nil {
		return inFrame{}
	}
	f := inFrame{T: msg.T, OK: msg.OK, Code: msg.Code}
	switch {
	case msg.T == "text":
		f.Text = msg.Text
	case msg.T == "whisper" && msg.Out:
		f.Text = "You whisper to " + msg.Who + ", \"" + msg.Text + "\""
	}
	return f
}

type Config struct {
	URL          string
	Count        int
	Duration     time.Duration // steady-state time, after admissions finish
	Interval     time.Duration // actions per bot; each action also sends a whisper fence
	Timeout      time.Duration // per dial/login/action
	JoinTimeout  time.Duration // entire admission phase
	LoginWorkers int
	Prefix       string
	Password     string
	Register     bool
	AllowRemote  bool // explicit opt-in; the default is loopback targets only
}

func (c Config) validate() error {
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" || u.User != nil {
		return errors.New("URL must be ws:// or wss:// without embedded credentials")
	}
	if !c.AllowRemote && !isLoopback(u.Hostname()) {
		return errors.New("remote load target refused; explicit AllowRemote is required")
	}
	if c.Count < 1 || c.Count > 100000 || c.LoginWorkers < 1 || c.LoginWorkers > c.Count {
		return errors.New("count must be 1..100000 and login workers must be 1..count")
	}
	if c.Duration <= 0 || c.Interval < 250*time.Millisecond || c.Timeout <= 0 || c.Timeout > time.Minute || c.JoinTimeout <= 0 {
		return errors.New("duration/join timeout must be positive, interval >=250ms, timeout in (0,1m]")
	}
	if !validPrefix.MatchString(c.Prefix) || c.Password == "" || strings.ContainsAny(c.Password, " \t\r\n") || len(c.Password) > 72 {
		return errors.New("prefix must be 1..32 alphanumeric/underscore characters; password must be 1..72 bytes without whitespace")
	}
	return nil
}

var validPrefix = regexp.MustCompile(`^[a-zA-Z0-9_]{1,32}$`)
var npcPattern = regexp.MustCompile(`\x1b\[1;31m([^\x1b]+)\x1b\[0m`)

func isLoopback(host string) bool {
	// Do not resolve arbitrary hostnames: accepting a DNS name here could direct
	// load to a remote address later. localhost is the only accepted name.
	return host == "localhost" || net.ParseIP(host).IsLoopback()
}

func Name(prefix string, index int) string { return fmt.Sprintf("%s%05d", prefix, index) }

type Report struct {
	Requested       int           `json:"requested"`
	Joined          int           `json:"joined"` // cumulative successful admissions, not concurrent count
	JoinFailures    int           `json:"join_failures"`
	ActiveAtStart   int64         `json:"active_at_start"`
	ActiveAtEnd     int64         `json:"active_at_end"`
	Disconnects     int           `json:"unexpected_disconnects"`
	Timeouts        int           `json:"action_timeouts"`
	ServerErrors    int           `json:"server_errors"`
	RateLimited     int           `json:"rate_limit_notices"`
	Actions         int           `json:"actions_sent"`
	Samples         int           `json:"completed_samples"`
	Uncompleted     int           `json:"uncompleted_actions"` // includes timeout/drop/end-of-window actions
	MinBotSamples   int           `json:"minimum_samples_per_joined_bot"`
	Moves           int           `json:"moves_sent"`
	Says            int           `json:"says_sent"`
	Whispers        int           `json:"whispers_sent"`
	Attacks         int           `json:"attacks_sent"`
	TargetedAttacks int           `json:"attacks_with_observed_target"`
	Hits            int           `json:"hit_messages"`
	Messages        int64         `json:"messages_received"`
	Bytes           int64         `json:"bytes_received"`
	P50MS           int64         `json:"p50_ms_upper_bound"`
	P95MS           int64         `json:"p95_ms_upper_bound"`
	P99MS           int64         `json:"p99_ms_upper_bound"`
	AdmissionTime   time.Duration `json:"admission_ns"`
	GameplayTime    time.Duration `json:"gameplay_ns"`
	Errors          []string      `json:"first_errors,omitempty"` // bounded to ten, no credentials/raw frames
}

type metrics struct {
	sync.Mutex
	r    Report
	hist []int // 1ms buckets, bounded by action timeout, shared across all bots
}

func (m *metrics) failure(kind string, index int) {
	m.Lock()
	defer m.Unlock()
	switch kind {
	case "join":
		m.r.JoinFailures++
	case "disconnect":
		m.r.Disconnects++
	case "timeout":
		m.r.Timeouts++
	}
	if len(m.r.Errors) < 10 {
		m.r.Errors = append(m.r.Errors, fmt.Sprintf("bot %d: %s", index, kind))
	}
}

func (m *metrics) sample(elapsed time.Duration) {
	m.Lock()
	defer m.Unlock()
	bucket := min(int((elapsed+time.Millisecond-1)/time.Millisecond), len(m.hist)-1)
	m.hist[max(0, bucket)]++
	m.r.Samples++
}

func percentile(hist []int, total, percent int) int64 {
	if total == 0 {
		return 0
	}
	wanted := (total*percent + 99) / 100
	seen := 0
	for bucket, count := range hist {
		seen += count
		if seen >= wanted {
			return int64(bucket)
		}
	}
	return int64(len(hist) - 1)
}

// Run separates bounded-concurrency admission from a common steady-state window.
// Every bot continuously drains input. The only latency sample is a correlated
// outgoing-whisper echo queued after an action: action+fence RTT, not broadcast
// delivery latency. Timeouts/drops are reported separately, never counted as fast
// successes. Run returns an error if the requested load was not sustained.
func Run(ctx context.Context, c Config) (Report, error) {
	if err := c.validate(); err != nil {
		return Report{}, err
	}
	m := &metrics{r: Report{Requested: c.Count}, hist: make([]int, int(c.Timeout/time.Millisecond)+2)}
	var active atomic.Int64
	clients := make([]*client, c.Count)
	joinCtx, cancelJoin := context.WithTimeout(ctx, c.JoinTimeout)
	start := time.Now()
	jobs := make(chan int)
	var workers sync.WaitGroup
	for w := 0; w < c.LoginWorkers; w++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				b, err := connect(joinCtx, c, index, m, &active)
				if err != nil {
					m.failure("join", index)
					continue
				}
				clients[index] = b
				m.Lock()
				m.r.Joined++
				m.Unlock()
			}
		}()
	}
	for index := 0; index < c.Count; index++ {
		if joinCtx.Err() != nil {
			for rest := index; rest < c.Count; rest++ {
				m.failure("join", rest)
			}
			break
		}
		select {
		case jobs <- index:
		case <-joinCtx.Done():
			for rest := index; rest < c.Count; rest++ {
				m.failure("join", rest)
			}
			index = c.Count
		}
	}
	close(jobs)
	workers.Wait()
	cancelJoin()
	m.Lock()
	m.r.AdmissionTime = time.Since(start)
	m.r.ActiveAtStart = active.Load()
	m.Unlock()

	playCtx, cancelPlay := context.WithTimeout(ctx, c.Duration)
	start = time.Now()
	var players sync.WaitGroup
	for _, b := range clients {
		if b == nil {
			continue
		}
		players.Add(1)
		go func(b *client) { defer players.Done(); b.play(playCtx) }(b)
	}
	players.Wait()
	cancelPlay()
	m.Lock()
	m.r.GameplayTime = time.Since(start)
	m.r.ActiveAtEnd = active.Load()
	m.Unlock()
	for _, b := range clients {
		if b != nil {
			b.close()
		}
	}
	for _, b := range clients {
		if b != nil {
			<-b.done
		}
	}
	m.Lock()
	r := m.r
	r.Uncompleted = r.Actions - r.Samples
	r.MinBotSamples = r.Samples
	for _, b := range clients {
		if b != nil {
			r.MinBotSamples = min(r.MinBotSamples, b.samples)
			r.Messages += b.messages
			r.Bytes += b.bytes
			r.ServerErrors += b.serverErrors
			r.RateLimited += b.rateLimited
			r.Hits += b.hits
		}
	}
	r.P50MS = percentile(m.hist, r.Samples, 50)
	r.P95MS = percentile(m.hist, r.Samples, 95)
	r.P99MS = percentile(m.hist, r.Samples, 99)
	m.Unlock()
	if ctx.Err() != nil {
		return r, ctx.Err()
	}
	if r.Joined != c.Count || r.ActiveAtStart != int64(c.Count) || r.ActiveAtEnd != int64(c.Count) || r.Disconnects > 0 || r.Timeouts > 0 || r.ServerErrors > 0 || r.RateLimited > 0 || r.Samples == 0 || r.MinBotSamples == 0 {
		return r, errors.New("requested load was not sustained; inspect join failures, active counts, timeouts and disconnects")
	}
	return r, nil
}

type client struct {
	conn     *websocket.Conn
	config   Config
	index    int
	metrics  *metrics
	active   *atomic.Int64
	stopping atomic.Bool
	done     chan struct{}
	ack      chan time.Duration
	sync.Mutex
	pending string
	sent    time.Time
	npc     string
	samples int // play-owned, read by Run only after all players finish
	// Reader-owned counters avoid a shared metrics lock on every broadcast.
	// Run reads these only after all readers finish.
	messages     int64
	bytes        int64
	serverErrors int
	rateLimited  int
	hits         int
}

func connect(ctx context.Context, c Config, index int, m *metrics, active *atomic.Int64) (*client, error) {
	dialCtx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{subprotocol}
	conn, _, err := dialer.DialContext(dialCtx, c.URL, nil)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(dialCtx, func() { conn.Close() })
	defer stop()
	failed := true
	defer func() {
		if failed {
			conn.Close()
		}
	}()
	deadline, _ := dialCtx.Deadline()
	conn.SetReadLimit(4 << 20)
	conn.SetReadDeadline(deadline)
	conn.SetWriteDeadline(deadline)
	verb := "login"
	if c.Register {
		verb = "register"
	}
	if err := conn.WriteMessage(websocket.TextMessage, frame(verb, map[string]string{"username": Name(c.Prefix, index), "password": c.Password})); err != nil {
		return nil, err
	}
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		f := parseFrame(message)
		if f.T == "auth" {
			if !f.OK {
				return nil, errors.New("authentication rejected")
			}
			break
		}
		if f.T != "text" && f.T != "hello" {
			return nil, errors.New("unexpected message before authentication: " + f.T)
		}
	}
	if dialCtx.Err() != nil {
		return nil, dialCtx.Err()
	}
	conn.SetReadDeadline(time.Time{})
	b := &client{conn: conn, config: c, index: index, metrics: m, active: active, done: make(chan struct{}), ack: make(chan time.Duration, 1)}
	active.Add(1)
	failed = false
	go b.read()
	return b, nil
}

func (b *client) close() { b.stopping.Store(true); b.conn.Close() }

func (b *client) read() {
	defer close(b.done)
	defer b.conn.Close()
	defer b.active.Add(-1)
	for {
		_, message, err := b.conn.ReadMessage()
		if err != nil {
			if !b.stopping.Load() {
				b.metrics.failure("disconnect", b.index)
			}
			return
		}
		f := parseFrame(message)
		text := f.Text // only game text is matched below; other types carry no text
		b.messages++
		b.bytes += int64(len(message))
		if strings.HasPrefix(text, "You hit ") {
			b.hits++
		}
		if f.T == "error" && f.Code == "internal" {
			b.serverErrors++
		}
		if f.T == "error" && f.Code == "rate_limited" {
			b.rateLimited++
		}
		b.Lock()
		if strings.HasPrefix(text, "You are in '") {
			b.npc = ""
		}
		if strings.Contains(text, "\x1b[1;31m") {
			if match := npcPattern.FindStringSubmatch(text); match != nil {
				b.npc = match[1]
			}
		}
		if b.pending != "" && strings.Contains(text, b.pending) {
			elapsed := time.Since(b.sent)
			b.pending = ""
			b.ack <- elapsed // one pending command, capacity one; never waits for a consumer
		}
		b.Unlock()
	}
}

func (b *client) play(ctx context.Context) {
	// Spread bots across the interval rather than issuing synchronized bursts.
	delay := time.NewTimer(time.Duration(b.index) * b.config.Interval / time.Duration(b.config.Count))
	defer delay.Stop()
	select {
	case <-ctx.Done():
		return
	case <-b.done:
		return
	case <-delay.C:
	}
	sequence := 0
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case <-b.done:
			return
		default:
		}
		command, kind := b.action(sequence)
		target := Name(b.config.Prefix, (b.index+1)%b.config.Count)
		if b.config.Count == 1 {
			target = b.config.Prefix + "sink"
		}
		marker := fmt.Sprintf("mudbot:%s:%d:%d", b.config.Prefix, b.index, sequence)
		b.Lock()
		b.pending = "You whisper to " + target + ", \"" + marker + "\""
		b.sent = time.Now()
		b.Unlock()
		b.metrics.Lock()
		b.metrics.r.Actions++
		b.metrics.r.Whispers++
		switch kind {
		case "move":
			b.metrics.r.Moves++
		case "say":
			b.metrics.r.Says++
		case "attack":
			b.metrics.r.Attacks++
			if command != "ATTACK" {
				b.metrics.r.TargetedAttacks++
			}
		}
		b.metrics.Unlock()
		b.conn.SetWriteDeadline(time.Now().Add(b.config.Timeout))
		if err := b.conn.WriteMessage(websocket.TextMessage, frame("cmd", map[string]string{"line": command})); err != nil {
			b.conn.Close()
			<-b.done
			return
		}
		if err := b.conn.WriteMessage(websocket.TextMessage, frame("cmd", map[string]string{"line": "WHISPER " + target + " " + marker})); err != nil {
			b.conn.Close()
			<-b.done
			return
		}
		timeout := time.NewTimer(b.config.Timeout)
		select {
		case elapsed := <-b.ack:
			timeout.Stop()
			if elapsed > b.config.Timeout {
				b.metrics.failure("timeout", b.index)
				b.close()
				<-b.done
				return
			}
			b.metrics.sample(elapsed)
			b.samples++
		case <-timeout.C:
			b.metrics.failure("timeout", b.index)
			b.close()
			<-b.done
			return
		case <-b.done:
			timeout.Stop()
			return
		case <-ctx.Done():
			timeout.Stop()
			return
		}
		sequence++
		delay.Reset(b.config.Interval)
		select {
		case <-ctx.Done():
			return
		case <-b.done:
			return
		case <-delay.C:
		}
	}
}

func (b *client) action(sequence int) (string, string) {
	// The seed world's route: Start -> Balcony -> Arena -> Balcony -> Start.
	// A missing exit still exercises command processing and is not a tool failure.
	switch sequence % 8 {
	case 0:
		return "MOVE N", "move"
	case 1:
		return "SAY mudbot hello", "say"
	case 2:
		return "MOVE E", "move"
	case 3, 4:
		b.Lock()
		npc := b.npc
		b.Unlock()
		return strings.TrimSpace("ATTACK " + npc), "attack"
	case 5:
		return "MOVE W", "move"
	case 6:
		return "SAY mudbot goodbye", "say"
	default:
		return "MOVE S", "move"
	}
}
