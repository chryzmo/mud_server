package mudserver

import (
	"fmt"
	"time"
)

/*
Resuming (plans/guest-sessions.md). A guest whose connection drops is not removed:
it goes dormant, still in its room, and nobody is told it left. A new connection
that presents the resume token reattaches to it, and is sent the frames it missed.
After the grace period the character is removed for real. A resume after that, or
after a restart, is an ordinary arrival.

Replay needs no sequence number inside frames, because every recipient of a
broadcast shares the same bytes. Instead each side counts. Every frame except
hello, auth and error is counted: the server as it queues them (sendFrame), the
client as it processes them. A player keeps the last few counted frames in a ring
(references to the shared bytes, not copies). On resume the client says how many
it has processed; the server replays the rest, or reports a gap if the ring no
longer holds them.
*/

const (
	defaultResumeGrace = 90 * time.Second
	defaultReplay      = 100
)

// record notes a counted frame for replay. Loop only (and the connection
// greeting path before admission, which sends no counted frames).
func (mp *MudPlayer) record(frame []byte) {
	if mp.replaySize > 0 {
		if mp.ring == nil {
			mp.ring = make([][]byte, mp.replaySize)
		}
		mp.ring[mp.sent%uint64(len(mp.ring))] = frame
	}
	mp.sent++
}

// framesAfter returns the counted frames a client that has processed `received`
// still needs, oldest first. ok is false if some are no longer held, or the
// client claims to have seen more than were ever sent.
func (mp *MudPlayer) framesAfter(received uint64) (frames [][]byte, ok bool) {
	if received > mp.sent {
		return nil, false
	}
	missing := mp.sent - received
	if missing > uint64(len(mp.ring)) {
		return nil, false
	}
	for i := received; i < mp.sent; i++ {
		frames = append(frames, mp.ring[i%uint64(len(mp.ring))])
	}
	return frames, true
}

// canResume reports whether this player can come back after a dropped
// connection: guests with a token.
func (mp *MudPlayer) canResume() bool { return mp.guest && mp.tokenHash != "" }

func (ms *MudServer) resumeGrace() time.Duration {
	if ms.Configuration.Connection.ResumeGraceMS > 0 {
		return time.Duration(ms.Configuration.Connection.ResumeGraceMS) * time.Millisecond
	}
	return defaultResumeGrace
}

// replayFrames is the ring size, kept well under the outbound queue so a full
// replay always fits in a new session's queue.
func (ms *MudServer) replayFrames() int {
	n := ms.Configuration.Connection.ReplayFrames
	if n <= 0 {
		n = defaultReplay
	}
	queue := ms.Configuration.SessionSettings().withDefaults().OutboundQueueSize
	return max(1, min(n, queue-16))
}

// connectionEnded runs on the loop when a connection's reader stops. A guest
// goes dormant; anyone else is removed at once. If a newer connection has
// already taken the player over, there is nothing to do.
func (ms *MudServer) connectionEnded(player *MudPlayer, session *Session) {
	session.Close()
	if ms.players[player.Username] != player || player.Session != session {
		return
	}
	if player.canResume() && ms.resumeGrace() > 0 {
		ms.goDormant(player)
		return
	}
	ms.disconnect(player)
}

func (ms *MudServer) goDormant(player *MudPlayer) {
	player.dormant = true
	if ms.dormant == nil {
		ms.dormant = make(map[*MudPlayer]time.Time)
	}
	ms.dormant[player] = time.Now().Add(ms.resumeGrace())
}

// expireDormant removes guests whose grace period has run out. Loop only.
func (ms *MudServer) expireDormant(now time.Time) {
	for player, until := range ms.dormant {
		if now.After(until) {
			ms.disconnect(player)
		}
	}
}

// attach gives an existing player (dormant, or still holding an older
// connection) a new session, and sends it what it missed. Loop only.
func (ms *MudServer) attach(player *MudPlayer, session *Session, received uint64, fresh welcome) {
	if old := player.Session; old != nil && old != session {
		// Say why before closing, so that client stops instead of reconnecting and
		// fighting for the character. A dead connection never reads it; that is fine.
		old.sendError(codeReplaced, "This character was opened on another device or tab.")
		old.CloseWith(closeReplaced)
	}
	player.Session = session
	player.dormant = false
	player.lastActive = time.Now() // coming back is activity
	delete(ms.dormant, player)

	frames, ok := player.framesAfter(received)
	hello := authMsg{T: msgAuth, OK: true, Message: "Welcome back, " + player.Username + "!", Resumed: true, Gap: !ok,
		Token: fresh.token, Recovery: fresh.recovery} // set only after a recovery
	if ok {
		hello.Replayed, hello.Base = len(frames), received
	} else {
		hello.Base = player.sent
	}
	session.Send(encode(hello))
	for _, frame := range frames {
		session.Send(frame) // already counted when first sent
	}
	if !ok {
		player.SendLook()
		player.SendStatUpdate()
	}
}

// resume runs on the connection's reader goroutine. It returns the player this
// connection now belongs to, or nil after telling the client why not.
func (ms *MudServer) resume(shell *MudPlayer, session *Session, token string, received uint64) *MudPlayer {
	if len(token) != tokenLength {
		session.sendAuth(false, codeBadToken, "That resume token isn't valid.")
		return nil
	}
	hash := hashSecret(token)

	// Online or dormant: reattach without touching the database.
	var existing *MudPlayer
	ms.onLoop(func() {
		if p := ms.byToken[hash]; p != nil && ms.players[p.Username] == p {
			existing = p
			ms.attach(p, session, received, welcome{})
		}
	})
	if existing != nil {
		return existing
	}

	// Not in memory: a restart, or the grace period ran out.
	id, username, err := shell.lookupGuestByToken(&ms.DB, hash)
	if err != nil {
		session.sendAuth(false, codeBadToken, "That resume token isn't valid.")
		return nil
	}
	if ms.refuseIfKicked(session, username) {
		return nil
	}
	online, saved := ms.isOnline(username)
	if online {
		session.sendAuth(false, codeFailed, "That character is busy. Please try again in a moment.")
		return nil
	}
	if saved != nil {
		select { // they just left; wait for that save so we load their latest state
		case <-saved:
		case <-ms.done:
			return nil
		}
	}
	if err := shell.LoadFromDB(&ms.DB, shell, id); err != nil {
		fmt.Println("resume: load: " + err.Error())
		session.sendAuth(false, codeFailed, "Resuming failed. Please try again.")
		return nil
	}
	hello := welcome{message: "Welcome back, " + username + "!", resumed: true, gap: true}
	if refusal := ms.admit(shell, hello); refusal != "" {
		shell.Username = ""
		if refusal == codeAtCapacity {
			session.sendAuth(false, refusal, "The event is at capacity right now. Please try again in a moment.")
		} else {
			session.sendAuth(false, codeFailed, "That character is busy. Please try again in a moment.")
		}
		return nil
	}
	return shell
}
