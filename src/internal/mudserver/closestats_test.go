package mudserver

import (
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return false }

var _ net.Error = timeoutError{}

func TestClassifyReadError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"message too big", websocket.ErrReadLimit, closeReadLimit},
		{"wrapped message too big", fmt.Errorf("read: %w", websocket.ErrReadLimit), closeReadLimit},
		{"normal close", &websocket.CloseError{Code: websocket.CloseNormalClosure}, closeClientClosed},
		{"going away", &websocket.CloseError{Code: websocket.CloseGoingAway}, closeClientClosed},
		{"no status", &websocket.CloseError{Code: websocket.CloseNoStatusReceived}, closeClientClosed},
		{"abnormal close", &websocket.CloseError{Code: websocket.CloseAbnormalClosure}, closeReadError},
		{"protocol error", &websocket.CloseError{Code: websocket.CloseProtocolError}, closeReadError},
		{"read deadline", timeoutError{}, closeReadTimeout},
		{"wrapped read deadline", fmt.Errorf("read: %w", timeoutError{}), closeReadTimeout},
		{"anything else", errors.New("connection reset by peer"), closeReadError},
	} {
		if got := classifyReadError(tc.err); got != tc.want {
			t.Errorf("%s: classifyReadError = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestSessionRecordsOnlyTheFirstCloseReason(t *testing.T) {
	stats := newCloseStats()
	s := &Session{out: make(chan []byte, 1), closed: make(chan struct{}), stats: stats}
	s.CloseWith(closeQueueFull)
	s.CloseWith(closeReadError) // the reader noticing the connection the writer just dropped
	s.Close()

	if s.reason != closeQueueFull {
		t.Errorf("reason = %q, want %q", s.reason, closeQueueFull)
	}
	if got := stats.Counts(); len(got) != 1 || got[closeQueueFull] != 1 {
		t.Errorf("counts = %v, want only queue_full=1", got)
	}
}

func TestFullQueueClosesWithQueueFull(t *testing.T) {
	stats := newCloseStats()
	s := &Session{out: make(chan []byte, 1), closed: make(chan struct{}), stats: stats}
	if err := s.Send([]byte("fits")); err != nil {
		t.Fatal(err)
	}
	if err := s.Send([]byte("overflows")); !errors.Is(err, errSessionClosed) {
		t.Fatalf("overflow Send = %v, want errSessionClosed", err)
	}
	if got := stats.Counts()[closeQueueFull]; got != 1 {
		t.Errorf("queue_full = %d, want 1", got)
	}
	// Sends to a closed session don't count again.
	s.Send([]byte("late"))
	if got := stats.Counts()[closeQueueFull]; got != 1 {
		t.Errorf("queue_full = %d after a late send, want 1", got)
	}
}

func TestCloseStatsSummaryAndNilSafety(t *testing.T) {
	var none *closeStats
	none.record(closeQueueFull) // a bare Session has no stats; this must not panic
	if got := none.Counts(); len(got) != 0 {
		t.Errorf("nil counts = %v", got)
	}

	stats := newCloseStats()
	if got := stats.summary(); got != "" {
		t.Errorf("empty summary = %q", got)
	}
	for _, r := range []string{closeQueueFull, closeClientClosed, closeQueueFull, closeReadTimeout} {
		stats.record(r)
	}
	if got, want := stats.summary(), "client_closed=1 queue_full=2 read_timeout=1"; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

func TestCloseStatsLogsUntilDone(t *testing.T) {
	stats := newCloseStats()
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() { stats.logPeriodically(done, time.Millisecond); close(finished) }()
	stats.record(closeClientClosed)
	close(done)
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("logPeriodically did not stop when done closed")
	}
}
