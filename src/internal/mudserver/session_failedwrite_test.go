package mudserver

import (
	"errors"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// After a client's close frame the library has already replied and refuses further writes with
// ErrCloseSent. That used to be counted as a write_error whenever the writer lost the race to the
// reader; the close was the client's, and must be counted as theirs.

func bareSession() (*Session, *closeStats) {
	stats := newCloseStats()
	return &Session{out: make(chan []byte, 1), closed: make(chan struct{}), stats: stats}, stats
}

func TestAWriteRefusedAfterTheClientClosedWaitsForTheReadersReason(t *testing.T) {
	s, stats := bareSession()
	go func() {
		time.Sleep(50 * time.Millisecond) // the reader gets to the close frame a little later
		s.CloseWith(closeClientClosed)
	}()
	s.failedWrite(websocket.ErrCloseSent, closeWriteError)
	if got := stats.counts[closeClientClosed]; got != 1 || stats.counts[closeWriteError] != 0 {
		t.Errorf("counts = %v, want one client_closed and no write_error", stats.counts)
	}
}

func TestAnyOtherFailedWriteIsCountedAtOnce(t *testing.T) {
	s, stats := bareSession()
	start := time.Now()
	s.failedWrite(errors.New("broken pipe"), closeWriteError)
	if stats.counts[closeWriteError] != 1 || time.Since(start) > closeReplyWait/2 {
		t.Errorf("counts = %v after %v, want an immediate write_error", stats.counts, time.Since(start))
	}
	p, pstats := bareSession()
	p.failedWrite(errors.New("i/o timeout"), closePingError)
	if pstats.counts[closePingError] != 1 {
		t.Errorf("counts = %v, want a ping_error", pstats.counts)
	}
}
