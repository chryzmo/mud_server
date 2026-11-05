package mudserver

import "time"

/*
Idle reclaim (plans/guest-sessions.md). A guest who has sent no command for
IdleTimeoutMS (default 30 minutes) is told so and disconnected, which sends them
through the usual dormant grace period and then out of the world. An open page
on a phone in a pocket would otherwise hold a room slot all weekend. Connection
keepalives are not activity; only commands are. Password accounts (GMs) are
never idle-kicked.

The client is told with an "idle" error before the close, so it can show "you
were away" and wait for a tap instead of reconnecting by itself and holding the
slot again. A resume or recovery counts as activity: someone chose to come back.
*/

const (
	defaultIdleTimeout = 30 * time.Minute
	codeIdle           = "idle"
	closeIdle          = "idle" // close reason: the guest sent no command for the idle timeout
)

// idleTimeout is how long a guest may go without a command; zero means never.
func (ms *MudServer) idleTimeout() time.Duration {
	switch v := ms.Configuration.Connection.IdleTimeoutMS; {
	case v < 0:
		return 0
	case v == 0:
		return defaultIdleTimeout
	default:
		return time.Duration(v) * time.Millisecond
	}
}

// reclaimIdle disconnects guests who have been idle too long. Loop only; cheap
// enough (one pass over the players) to run on every world tick.
func (ms *MudServer) reclaimIdle(now time.Time) {
	limit := ms.idleTimeout()
	if limit <= 0 {
		return
	}
	for _, player := range ms.players {
		if player.guest && !player.dormant && player.Session != nil && !player.Session.isClosed() &&
			now.Sub(player.lastActive) > limit {
			ms.kickIdle(player)
		}
	}
}

func (ms *MudServer) kickIdle(player *MudPlayer) {
	player.Session.Send(encode(errorMsg{T: msgError, Code: codeIdle,
		Message: "You were away for a while, so you were disconnected. Tap to rejoin."}))
	player.Session.CloseWith(closeIdle) // the reader ends and the player goes dormant
}
