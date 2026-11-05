package mudserver

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"mud_server/src/internal/mudutils"
)

// protocolVersion is the only envelope version the server speaks. See
// plans/json-protocol.md: every websocket text frame, in both directions, is
// {"v":1,"t":"<type>","d":{...}}.
const protocolVersion = 1

// Client-to-server message types.
const (
	msgCmd      = "cmd"
	msgRegister = "register"
	msgLogin    = "login"
)

// Server-to-client message types.
const (
	msgText      = "text"
	msgAuth      = "auth"
	msgStats     = "stats"
	msgRoom      = "room"
	msgInventory = "inventory"
	msgError     = "error"
)

// Error and auth codes.
const (
	codeBadFrame        = "bad_frame"
	codeBadVersion      = "bad_version"
	codeBadRequest      = "bad_request"
	codeNotLoggedIn     = "not_logged_in"
	codeAlreadyLoggedIn = "already_logged_in"
	codeInvalidUsername = "invalid_username"
	codeNameTaken       = "name_taken"
	codeNameOnline      = "name_online"
	codeUnknownUser     = "unknown_user"
	codeBadPassword     = "bad_password" // pre-commit: allow-secret (an error code, not a secret)
	codeFailed          = "failed"
	codeRateLimited     = "rate_limited"
	codeUnknownCommand  = "unknown_command"
	codeInternal        = "internal"
	codeRestarting      = "restarting"
)

type envelope struct {
	V int             `json:"v"`
	T string          `json:"t"`
	D json.RawMessage `json:"d,omitempty"`
}

type outEnvelope struct {
	V int    `json:"v"`
	T string `json:"t"`
	D any    `json:"d,omitempty"`
}

// encodeEnvelope builds one outbound frame. A broadcast calls it once and
// queues the same bytes to every recipient.
func encodeEnvelope(t string, d any) []byte {
	b, err := json.Marshal(outEnvelope{V: protocolVersion, T: t, D: d})
	if err != nil {
		// Every payload is a plain struct or map of strings and numbers.
		panic(fmt.Sprintf("encode %s envelope: %v", t, err))
	}
	return b
}

type textPayload struct {
	Text string `json:"text"`
	Kind string `json:"kind"`
}

type authPayload struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

type errorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type statsPayload struct {
	HP       int `json:"hp"`
	MaxHP    int `json:"maxhp"`
	MP       int `json:"mp"`
	MaxMP    int `json:"maxmp"`
	Atk      int `json:"atk"`
	AtkBonus int `json:"atkbonus"`
	Def      int `json:"def"`
	DefBonus int `json:"defbonus"`
}

type roomPayload struct {
	Name  string   `json:"name"`
	Exits []string `json:"exits"`
}

type credentialsPayload struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type cmdPayload struct {
	Line string `json:"line"`
}

// textKind classifies a game line by its leading colour escape, the same rule
// the client used to apply to the raw text.
func textKind(text string) string {
	switch {
	case strings.HasPrefix(text, "\033[1;"+mudutils.COLOR_RED):
		return "combat"
	case strings.HasPrefix(text, "\033[1;"+mudutils.COLOR_BLUE):
		return "player"
	case strings.HasPrefix(text, "\033[1;"+mudutils.COLOR_GREEN):
		return "item"
	}
	return "plain"
}

// SendMessage queues a line of game text. It never blocks; it fails only if the
// session is closed or the client has fallen too far behind.
func (mp *MudPlayer) SendMessage(message string) error {
	return mp.Session.Send(encodeText(message))
}

// encodeText builds the frame for a line of game text. Fan-out calls it once and
// queues the same bytes to every recipient; nothing may modify the slice.
func encodeText(message string) []byte {
	return encodeEnvelope(msgText, textPayload{Text: message, Kind: textKind(message)})
}

// SendAuth answers a register or login attempt.
func (mp *MudPlayer) SendAuth(ok bool, code, message string) error {
	return mp.Session.Send(encodeEnvelope(msgAuth, authPayload{OK: ok, Code: code, Message: message}))
}

// SendError reports a protocol or server problem that isn't game text.
func (mp *MudPlayer) SendError(code, message string) error {
	return mp.Session.Send(encodeEnvelope(msgError, errorPayload{Code: code, Message: message}))
}

// SendInventory sends the inventory listing for the client's inventory view.
func (mp *MudPlayer) SendInventory(text string) error {
	return mp.Session.Send(encodeEnvelope(msgInventory, textPayload{Text: text}))
}

// SendStatUpdate pushes the player's current stats for the stats bar.
func (mp *MudPlayer) SendStatUpdate() {
	mp.Session.Send(encodeEnvelope(msgStats, statsPayload{
		HP: mp.HP, MaxHP: mp.EffectiveMaxHP(),
		MP: mp.MP, MaxMP: mp.EffectiveMaxMP(),
		Atk: mp.EffectiveAttackPower(), AtkBonus: mp.Equipment.TotalBonusAttackPower(),
		Def: mp.EffectiveDefense(), DefBonus: mp.Equipment.TotalBonusDefense(),
	}))
}

// SendLook describes the player's room: the text, then the room's name and exits.
func (mp *MudPlayer) SendLook() {
	room := mp.Location
	mp.SendMessage(room.LookDescription())
	exits := make([]string, 0, len(room.ExitMap))
	for dir := range room.ExitMap {
		exits = append(exits, dir)
	}
	sort.Strings(exits)
	mp.Session.Send(encodeEnvelope(msgRoom, roomPayload{Name: room.Name, Exits: exits}))
}
