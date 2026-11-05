package mudserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/gorilla/websocket"
)

// Subprotocol is the websocket subprotocol clients must offer. It carries the
// protocol version (once per connection, not in every frame). Every frame, both
// ways, is a flat JSON object with a type: {"t":"say","who":"bob","text":"hi"}.
// See plans/json-protocol.md.
const Subprotocol = "mud.v1"

// Client-to-server message types.
const (
	msgCmd      = "cmd"
	msgRegister = "register"
	msgLogin    = "login"
)

// Server-to-client message types.
const (
	msgText      = "text"
	msgSay       = "say"
	msgArrive    = "arrive"
	msgLeave     = "leave"
	msgWhisper   = "whisper"
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

// inbound is every client-to-server message. Pointer fields tell "missing"
// from "empty".
type inbound struct {
	T        string  `json:"t"`
	Line     *string `json:"line"`
	Username string  `json:"username"`
	Password string  `json:"password"`
}

// Outbound messages. Each struct is one frame; T is the type.
type (
	textMsg struct {
		T    string `json:"t"`
		Text string `json:"text"`
	}
	sayMsg struct {
		T    string `json:"t"`
		Who  string `json:"who"`
		Text string `json:"text"`
	}
	arriveMsg struct {
		T    string `json:"t"`
		Who  string `json:"who"`
		From string `json:"from,omitempty"` // direction entered from; empty when they simply appeared
	}
	leaveMsg struct {
		T   string `json:"t"`
		Who string `json:"who"`
		To  string `json:"to,omitempty"` // direction left toward; empty when they vanished
	}
	whisperMsg struct {
		T    string `json:"t"`
		Who  string `json:"who"`
		Text string `json:"text"`
		Out  bool   `json:"out,omitempty"` // your own echo: you whispered to Who
	}
	authMsg struct {
		T       string `json:"t"`
		OK      bool   `json:"ok"`
		Code    string `json:"code,omitempty"`
		Message string `json:"message"`
	}
	errorMsg struct {
		T       string `json:"t"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	statsMsg struct {
		T        string `json:"t"`
		HP       int    `json:"hp"`
		MaxHP    int    `json:"maxhp"`
		MP       int    `json:"mp"`
		MaxMP    int    `json:"maxmp"`
		Atk      int    `json:"atk"`
		AtkBonus int    `json:"atkbonus"`
		Def      int    `json:"def"`
		DefBonus int    `json:"defbonus"`
	}
	roomMsg struct {
		T     string   `json:"t"`
		Name  string   `json:"name"`
		Exits []string `json:"exits"`
	}
	inventoryMsg struct {
		T    string `json:"t"`
		Text string `json:"text"`
	}
)

// encode builds one outbound frame. Fan-out calls it once and queues the same
// bytes to every recipient; nothing may modify the slice. HTML escaping is off:
// text is shown as text, and escaping < > & would only add bytes.
func encode(msg any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(msg); err != nil {
		// Every message is a plain struct of strings, numbers and bools.
		panic(fmt.Sprintf("encode %T: %v", msg, err))
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

func encodeText(message string) []byte { return encode(textMsg{T: msgText, Text: message}) }

func encodeSay(who, text string) []byte { return encode(sayMsg{T: msgSay, Who: who, Text: text}) }

func encodeArrive(who, from string) []byte {
	return encode(arriveMsg{T: msgArrive, Who: who, From: from})
}

func encodeLeave(who, to string) []byte { return encode(leaveMsg{T: msgLeave, Who: who, To: to}) }

// SendMessage queues a line of game text. It never blocks; it fails only if the
// session is closed or the client has fallen too far behind.
func (mp *MudPlayer) SendMessage(message string) error {
	return mp.Session.Send(encodeText(message))
}

// SendWhisper delivers a whisper; out marks the sender's own echo.
func (mp *MudPlayer) SendWhisper(who, text string, out bool) error {
	return mp.Session.Send(encode(whisperMsg{T: msgWhisper, Who: who, Text: text, Out: out}))
}

// SendAuth answers a register or login attempt.
func (mp *MudPlayer) SendAuth(ok bool, code, message string) error {
	return mp.Session.Send(encode(authMsg{T: msgAuth, OK: ok, Code: code, Message: message}))
}

// SendError reports a protocol or server problem that isn't game text.
func (mp *MudPlayer) SendError(code, message string) error {
	return mp.Session.Send(encode(errorMsg{T: msgError, Code: code, Message: message}))
}

// SendInventory sends the inventory listing for the client's inventory view.
func (mp *MudPlayer) SendInventory(text string) error {
	return mp.Session.Send(encode(inventoryMsg{T: msgInventory, Text: text}))
}

// SendStatUpdate pushes the player's current stats for the stats bar.
func (mp *MudPlayer) SendStatUpdate() {
	mp.Session.Send(encode(statsMsg{
		T: msgStats, HP: mp.HP, MaxHP: mp.EffectiveMaxHP(),
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
	mp.Session.Send(encode(roomMsg{T: msgRoom, Name: room.Name, Exits: exits}))
}

// RefuseUnlessProtocol checks that the client negotiated Subprotocol. If not it
// sends one bad_version error and closes the connection, and returns false.
func RefuseUnlessProtocol(conn *websocket.Conn) bool {
	if conn.Subprotocol() == Subprotocol {
		return true
	}
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	conn.WriteMessage(websocket.TextMessage, encode(errorMsg{T: msgError, Code: codeBadVersion,
		Message: "Unsupported protocol; connect with the websocket subprotocol " + Subprotocol + "."}))
	conn.Close()
	return false
}
