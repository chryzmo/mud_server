package mudserver

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	muddatabase "mud_server/src/internal/database"
)

/*
Recovery (plans/guest-sessions.md). A guest who has lost their resume token (cleared
browser storage, a new phone) can take their character back with the recovery code
they were shown at join. A successful recovery issues a fresh token and a fresh
code and invalidates the old ones, so a stale device cannot take the character
back, and the character resumes as after a restart: the client's frame count is
unknown, so it gets a gap and a fresh look.
*/

const (
	maxRecoveryFailures = 5
	defaultRecoveryLock = 15 * time.Minute
)

// noFramesSeen is the received count of a client that remembers nothing: more than
// the server ever sent, so framesAfter reports a gap.
const noFramesSeen = ^uint64(0)

// recoveryGuard limits guessing of recovery codes per name: after
// maxRecoveryFailures failures within the lock duration, the name refuses
// recovery until it ends. Only existing names are recorded, so random names
// cannot grow it. Connection readers call it concurrently.
type recoveryGuard struct {
	mu      sync.Mutex
	lock    time.Duration
	now     func() time.Time
	entries map[string]*recoveryEntry
}

type recoveryEntry struct {
	failures    int
	last        time.Time
	lockedUntil time.Time
}

func newRecoveryGuard(lock time.Duration) *recoveryGuard {
	if lock <= 0 {
		lock = defaultRecoveryLock
	}
	return &recoveryGuard{lock: lock, now: time.Now, entries: make(map[string]*recoveryEntry)}
}

// locked reports whether the name refuses recovery now, and for how much longer.
func (g *recoveryGuard) locked(name string) (time.Duration, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.entries[strings.ToLower(name)]
	if e == nil {
		return 0, false
	}
	if remaining := e.lockedUntil.Sub(g.now()); remaining > 0 {
		return remaining, true
	}
	return 0, false
}

// fail records a wrong code. Failures older than the lock duration are forgotten.
func (g *recoveryGuard) fail(name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	key, now := strings.ToLower(name), g.now()
	e := g.entries[key]
	if e == nil {
		e = &recoveryEntry{}
		g.entries[key] = e
	}
	if now.Sub(e.last) > g.lock {
		e.failures = 0
	}
	e.failures++
	e.last = now
	if e.failures >= maxRecoveryFailures {
		e.lockedUntil = now.Add(g.lock)
		e.failures = 0
	}
}

func (g *recoveryGuard) succeed(name string) {
	g.mu.Lock()
	delete(g.entries, strings.ToLower(name))
	g.mu.Unlock()
}

// lookupGuestForRecovery finds a guest by name, ignoring case. Reader goroutine only.
func lookupGuestForRecovery(db *muddatabase.MudDatabase, name string) (id int32, username, recoveryHash string, err error) {
	err = db.Connection.QueryRow(db.Context,
		"SELECT id, username, recoveryhash FROM player WHERE guest AND recoveryhash <> '' AND lower(username)=lower($1)", name).
		Scan(&id, &username, &recoveryHash)
	return id, username, recoveryHash, err
}

// rotateCredentials swaps in a new token and code, but only if the stored code
// is still the one that was just verified, so two recoveries with the same code
// cannot both succeed. It reports whether this call won.
func rotateCredentials(db *muddatabase.MudDatabase, id int32, oldRecoveryHash, newTokenHash, newRecoveryHash string) (bool, error) {
	tag, err := db.Connection.Exec(db.Context,
		"UPDATE player SET resumetokenhash=$1, recoveryhash=$2 WHERE id=$3 AND guest AND recoveryhash=$4",
		newTokenHash, newRecoveryHash, id, oldRecoveryHash)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (ms *MudServer) recoveryLock() time.Duration {
	if ms.Configuration.Connection.RecoveryLockMS > 0 {
		return time.Duration(ms.Configuration.Connection.RecoveryLockMS) * time.Millisecond
	}
	return defaultRecoveryLock
}

const recoverFailed = "That name and recovery code don't match."

// recoverGuest runs on the connection's reader goroutine. It returns the player
// this connection now belongs to, or nil after telling the client why not.
func (ms *MudServer) recoverGuest(shell *MudPlayer, session *Session, name, code string) *MudPlayer {
	if name == "" || len(name) > 64 || code == "" || len(code) > 64 {
		session.sendAuth(false, codeBadRequest, "A recover needs a name and a recovery code.")
		return nil
	}
	id, username, storedHash, err := lookupGuestForRecovery(&ms.DB, name)
	if errors.Is(err, pgx.ErrNoRows) {
		session.sendAuth(false, codeBadRecovery, recoverFailed)
		return nil
	}
	if err != nil {
		fmt.Println("recover: lookup: " + err.Error())
		session.sendAuth(false, codeFailed, "Recovery failed. Please try again.")
		return nil
	}
	if remaining, locked := ms.recoveries.locked(username); locked {
		ms.logIP(session, "Recovery refused (name locked): %s", username)
		session.Send(encode(authMsg{T: msgAuth, Code: codeRecoveryLocked, RetryMS: int(remaining.Milliseconds()),
			Message: fmt.Sprintf("Too many wrong codes for that name. Try again in %d minutes.", int(remaining.Minutes())+1)}))
		return nil
	}
	if subtle.ConstantTimeCompare([]byte(hashSecret(normaliseRecovery(code))), []byte(storedHash)) != 1 {
		ms.recoveries.fail(username)
		ms.logIP(session, "Recovery failed (wrong code): %s", username)
		session.sendAuth(false, codeBadRecovery, recoverFailed)
		return nil
	}

	token, recovery, err := newCredentials()
	if err != nil {
		fmt.Println("recover: credentials: " + err.Error())
		session.sendAuth(false, codeFailed, "Recovery failed. Please try again.")
		return nil
	}
	won, err := rotateCredentials(&ms.DB, id, storedHash, hashSecret(token), hashSecret(normaliseRecovery(recovery)))
	if err != nil {
		fmt.Println("recover: rotate: " + err.Error())
		session.sendAuth(false, codeFailed, "Recovery failed. Please try again.")
		return nil
	}
	if !won { // someone else recovered with this code first
		session.sendAuth(false, codeBadRecovery, recoverFailed)
		return nil
	}
	ms.recoveries.succeed(username)
	ms.logIP(session, "Recovery succeeded: %s", username)
	fresh := welcome{token: token, recovery: recovery}

	// Online or dormant: the new credentials replace the old, and this connection
	// takes the character over (closing any connection still holding it).
	var existing *MudPlayer
	ms.onLoop(func() {
		p := ms.players[username]
		if p == nil || !p.guest {
			return
		}
		existing = p
		delete(ms.byToken, p.tokenHash)
		p.tokenHash = hashSecret(token)
		ms.byToken[p.tokenHash] = p
		ms.attach(p, session, noFramesSeen, fresh)
	})
	if existing != nil {
		return existing
	}

	// Not in the world: load them and admit as an ordinary arrival.
	online, saved := ms.isOnline(username)
	if online {
		session.sendAuth(false, codeFailed, "That character is busy. Please try again in a moment.")
		return nil
	}
	if saved != nil {
		select {
		case <-saved:
		case <-ms.done:
			return nil
		}
	}
	if err := shell.LoadFromDB(&ms.DB, shell, id); err != nil {
		fmt.Println("recover: load: " + err.Error())
		session.sendAuth(false, codeFailed, "Recovery failed. Please try again.")
		return nil
	}
	fresh.message, fresh.resumed, fresh.gap = "Welcome back, "+username+"!", true, true
	if refusal := ms.admit(shell, fresh); refusal != "" {
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
