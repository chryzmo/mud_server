package mudserver

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	muddatabase "mud_server/src/internal/database"
	"strings"
)

// Guest sessions (plans/guest-sessions.md). A guest joins with a name only and
// is handed a resume token and a recovery code once, at join. The database keeps
// only their hashes.

// recoveryAlphabet has no I, O, 0 or 1, so a code written down by hand can't be
// misread. 32 symbols, so a random byte maps to one without bias.
const recoveryAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

const recoveryLength = 12 // 60 bits, shown as XXXX-XXXX-XXXX

// welcome is what a successful admission tells the client.
type welcome struct {
	message  string
	token    string // resume token; guests only, sent once
	recovery string // recovery code; guests only, sent once

	// Set when a guest resumes without a live session to continue (a restart,
	// or the grace period ran out): their frame count restarts at zero.
	resumed bool
	gap     bool
	base    uint64
}

// tokenLength is the length of a resume token: 32 random bytes as unpadded base64.
const tokenLength = 43

// lookupGuestByToken finds the guest whose resume token hashes to hash. Reader
// goroutine only (it reads the database, like login).
func (mp *MudPlayer) lookupGuestByToken(db *muddatabase.MudDatabase, hash string) (id int32, username string, err error) {
	err = db.Connection.QueryRow(db.Context, "SELECT id, username FROM player WHERE guest AND resumetokenhash=$1", hash).Scan(&id, &username)
	return id, username, err
}

// newCredentials makes a resume token (256 random bits, URL-safe) and a
// recovery code.
func newCredentials() (token, recovery string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(raw)

	codeBytes := make([]byte, recoveryLength)
	if _, err = rand.Read(codeBytes); err != nil {
		return "", "", err
	}
	var code strings.Builder
	for i, b := range codeBytes {
		if i > 0 && i%4 == 0 {
			code.WriteByte('-')
		}
		code.WriteByte(recoveryAlphabet[int(b)%len(recoveryAlphabet)])
	}
	return token, code.String(), nil
}

// hashSecret is how tokens and recovery codes are stored. They are high-entropy
// random values, so a fast hash is enough and keeps join cheap at an arrival
// spike (a slow password hash would not be).
func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// normaliseRecovery accepts a code as typed: any case, with or without dashes
// or spaces.
func normaliseRecovery(code string) string {
	code = strings.ToUpper(code)
	return strings.NewReplacer("-", "", " ", "").Replace(code)
}

// join runs on the connection's reader goroutine, like register. It creates a
// guest, places them, and tells them their token and recovery code.
func (ms *MudServer) join(player *MudPlayer, name string) bool {
	if code, reason := ms.names.check(name); code != "" {
		player.SendAuth(false, code, reason)
		return false
	}

	// Don't create a row for someone we can't place.
	var space bool
	ms.onLoop(func() { space = ms.World.canAdmit(player.savedRoomID) })
	if !space {
		player.SendAuth(false, codeAtCapacity, "The event is at capacity right now. Please try again in a moment.")
		return false
	}

	token, recovery, err := newCredentials()
	if err != nil {
		fmt.Println("join: credentials: " + err.Error())
		player.SendAuth(false, codeFailed, "Joining failed. Please try again.")
		return false
	}
	player.Username = name
	player.guest = true
	player.tokenHash = hashSecret(token)
	player.recoveryHash = hashSecret(normaliseRecovery(recovery))
	if err := player.InsertIntoDB(&ms.DB); err != nil {
		player.Username = ""
		if errors.Is(err, errUsernameTaken) {
			player.SendAuth(false, codeNameTaken, "That name is taken. Please pick another.")
		} else {
			fmt.Println("join: insert: " + err.Error())
			player.SendAuth(false, codeFailed, "Joining failed. Please try again.")
		}
		return false
	}

	if refusal := ms.admit(player, welcome{message: "Welcome, " + name + "!", token: token, recovery: recovery}); refusal != "" {
		// Someone took the last space between the check and now. Don't leave a
		// guest nobody was told about holding the name.
		player.DeleteFromDB(&ms.DB)
		player.Username = ""
		if refusal == codeAtCapacity {
			player.SendAuth(false, refusal, "The event is at capacity right now. Please try again in a moment.")
		} else {
			player.SendAuth(false, codeNameTaken, "That name is taken. Please pick another.")
		}
		return false
	}
	ms.logIP(player.Session, "Guest joined: %s", name)
	return true
}
