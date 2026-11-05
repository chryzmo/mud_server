package mudserver

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Why a session ended. The first reason recorded wins; later closes of the same
// session (the writer noticing the connection is gone, the reader cleaning up)
// don't overwrite it.
const (
	closeServer       = "server"        // closed by game code (e.g. a login that lost a race)
	closeShutdown     = "shutdown"      // the server is stopping
	closeQueueFull    = "queue_full"    // the client fell behind its outbound queue
	closeWriteError   = "write_error"   // a message write failed or timed out
	closePingError    = "ping_error"    // a keepalive ping write failed or timed out
	closeClientClosed = "client_closed" // the client sent a close frame
	closeReadTimeout  = "read_timeout"  // no pong or message within PongTimeout
	closeReadLimit    = "read_limit"    // a message exceeded ReadLimitBytes
	closeReadError    = "read_error"    // the connection broke (reset, EOF, bad frame)
)

// classifyReadError names why a websocket read failed.
func classifyReadError(err error) string {
	var closeErr *websocket.CloseError
	var netErr net.Error
	switch {
	case errors.Is(err, websocket.ErrReadLimit):
		return closeReadLimit
	case errors.As(err, &closeErr) && (closeErr.Code == websocket.CloseNormalClosure ||
		closeErr.Code == websocket.CloseGoingAway || closeErr.Code == websocket.CloseNoStatusReceived):
		return closeClientClosed
	case errors.As(err, &netErr) && netErr.Timeout():
		return closeReadTimeout
	}
	return closeReadError
}

// closeStats counts session closes by reason. Safe for concurrent use and for a
// nil receiver, so tests that build bare Sessions need no setup.
type closeStats struct {
	mu     sync.Mutex
	counts map[string]int64
}

func newCloseStats() *closeStats { return &closeStats{counts: make(map[string]int64)} }

// routine closes aren't worth a log line each.
func routine(reason string) bool {
	return reason == closeClientClosed || reason == closeShutdown || reason == closeServer
}

func (c *closeStats) record(reason string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.counts[reason]++
	first := c.counts[reason] == 1
	c.mu.Unlock()
	if first && !routine(reason) {
		fmt.Printf("Session closed: first %s\n", reason)
	}
}

// Counts returns a copy of the per-reason totals.
func (c *closeStats) Counts() map[string]int64 {
	out := make(map[string]int64)
	if c == nil {
		return out
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range c.counts {
		out[k] = v
	}
	return out
}

// summary renders the totals as "queue_full=12 client_closed=3", or "" if none.
func (c *closeStats) summary() string {
	counts := c.Counts()
	reasons := make([]string, 0, len(counts))
	for r := range counts {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	parts := make([]string, len(reasons))
	for i, r := range reasons {
		parts[i] = fmt.Sprintf("%s=%d", r, counts[r])
	}
	return strings.Join(parts, " ")
}

// logPeriodically prints the totals every interval while they change, and once
// more when done closes.
func (c *closeStats) logPeriodically(done <-chan struct{}, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	last := ""
	for {
		select {
		case <-ticker.C:
			if s := c.summary(); s != last && s != "" {
				fmt.Printf("Session closes so far: %s\n", s)
				last = s
			}
		case <-done:
			if s := c.summary(); s != "" {
				fmt.Printf("Session closes total: %s\n", s)
			}
			return
		}
	}
}
