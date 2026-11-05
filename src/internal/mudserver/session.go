package mudserver

import (
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

/*
A Session is one websocket connection. Its reader limits message size and
command rate before parsing or authentication. Everything sent to the client
goes through its bounded outbound queue; one writer goroutine drains it, so
a slow or stalled client can never block the game. When the queue is full,
the session is closed and the reader cleans up the player.

The writer also pings the client. Browsers answer pings automatically; the
pong extends the read deadline, so a client that has silently gone away
(phone asleep, Wi-Fi dropped) is noticed within PongTimeout.
*/
type Session struct {
	conn      *websocket.Conn
	out       chan []byte
	closed    chan struct{}
	closeOnce sync.Once
	settings  SessionSettings
	limiter   commandLimiter // reader-owned, including before login
	stats     *closeStats    // where the close reason is counted; may be nil
	reason    string         // why it closed; valid once closed is closed
}

// SessionSettings tunes connection limits, the outbound queue and keepalives.
// Zero fields take the defaults.
type SessionSettings struct {
	ReadLimitBytes    int64         // per message, including fragments; default 4096
	CommandsPerSecond int           // reader-side token refill rate; default 10
	CommandBurst      int           // token bucket capacity; default 20
	SayPerSecond      int           // per-player SAY refill rate; default 1
	SayBurst          int           // per-player SAY bucket capacity; default 5
	OutboundQueueSize int           // messages; default 256
	WriteTimeout      time.Duration // per write; default 10s
	PingInterval      time.Duration // default 30s; Cloudflare closes websockets idle for 100s
	PongTimeout       time.Duration // default 60s; must exceed PingInterval
}

var errSessionClosed = errors.New("session closed")

func (s SessionSettings) withDefaults() SessionSettings {
	if s.ReadLimitBytes <= 0 {
		s.ReadLimitBytes = 4096
	}
	if s.CommandsPerSecond <= 0 {
		s.CommandsPerSecond = 10
	}
	if s.CommandBurst <= 0 {
		s.CommandBurst = 20
	}
	if s.SayPerSecond <= 0 {
		s.SayPerSecond = 1
	}
	if s.SayBurst <= 0 {
		s.SayBurst = 5
	}
	if s.OutboundQueueSize <= 0 {
		s.OutboundQueueSize = 256
	}
	if s.WriteTimeout <= 0 {
		s.WriteTimeout = 10 * time.Second
	}
	if s.PingInterval <= 0 {
		s.PingInterval = 30 * time.Second
	}
	if s.PongTimeout <= 0 {
		s.PongTimeout = 60 * time.Second
	}
	return s
}

func NewSession(conn *websocket.Conn, settings SessionSettings, stats *closeStats) *Session {
	s := &Session{
		stats:    stats,
		conn:     conn,
		settings: settings.withDefaults(),
		closed:   make(chan struct{}),
	}
	s.out = make(chan []byte, s.settings.OutboundQueueSize)
	s.limiter = newCommandLimiter(s.settings, time.Now())

	conn.SetReadLimit(s.settings.ReadLimitBytes)
	conn.SetReadDeadline(time.Now().Add(s.settings.PongTimeout))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(s.settings.PongTimeout))
	})

	go s.writeLoop()
	return s
}

// Send queues a message without blocking. If the queue is full, the client has
// fallen too far behind: the session is closed and errSessionClosed returned.
func (s *Session) Send(message []byte) error {
	select {
	case <-s.closed:
		return errSessionClosed
	default:
	}
	select {
	case s.out <- message:
		return nil
	default:
		s.CloseWith(closeQueueFull)
		return errSessionClosed
	}
}

// ReadMessage reads the next text frame. Only the session's reader goroutine calls it.
//
// The silence timer starts here, when we begin listening, so time the reader
// spent handling the previous message (a bcrypt check at login, say) doesn't
// count against the client. Pongs extend it while we wait.
func (s *Session) ReadMessage() ([]byte, error) {
	s.conn.SetReadDeadline(time.Now().Add(s.settings.PongTimeout))
	_, message, err := s.conn.ReadMessage()
	if err != nil {
		s.CloseWith(classifyReadError(err))
	}
	return message, err
}

// allowCommand runs only on the reader, before parsing or authentication. Notices
// are coalesced to at most one per second, so a flood cannot flood the output too.
func (s *Session) allowCommand(now time.Time) bool {
	allowed, notify := s.limiter.allow(now)
	if notify {
		s.Send(encode(errorMsg{T: msgError, Code: codeRateLimited, Message: "Slow down. Too many commands."}))
	}
	return allowed
}

// Close stops the session for a reason the game chose. See CloseWith.
func (s *Session) Close() { s.CloseWith(closeServer) }

// CloseWith stops the session and counts why. The writer flushes what's already
// queued (briefly) and closes the connection, which also ends the reader. Safe
// to call repeatedly; the first reason wins.
func (s *Session) CloseWith(reason string) {
	s.closeOnce.Do(func() {
		s.reason = reason
		s.stats.record(reason)
		close(s.closed)
	})
}

func (s *Session) writeLoop() {
	ping := time.NewTicker(s.settings.PingInterval)
	defer func() {
		ping.Stop()
		s.Close()
		s.conn.Close()
	}()
	defer recoverPanic("session writer", nil)

	for {
		select {
		case message := <-s.out:
			if !s.write(websocket.TextMessage, message, time.Now().Add(s.settings.WriteTimeout)) {
				s.CloseWith(closeWriteError)
				return
			}
		case <-ping.C:
			if !s.write(websocket.PingMessage, nil, time.Now().Add(s.settings.WriteTimeout)) {
				s.CloseWith(closePingError)
				return
			}
		case <-s.closed:
			s.flush()
			return
		}
	}
}

// flush delivers anything still queued (such as a "server restarting" notice)
// and a close frame, giving up after one second in total.
func (s *Session) flush() {
	deadline := time.Now().Add(time.Second)
	for {
		select {
		case message := <-s.out:
			if !s.write(websocket.TextMessage, message, deadline) {
				return
			}
		default:
			s.write(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, ""), deadline)
			return
		}
	}
}

func (s *Session) write(messageType int, data []byte, deadline time.Time) bool {
	s.conn.SetWriteDeadline(deadline)
	return s.conn.WriteMessage(messageType, data) == nil
}
