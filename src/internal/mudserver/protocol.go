package mudserver

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	msgCmd     = "cmd"
	msgLogin   = "login"
	msgJoin    = "join"
	msgResume  = "resume"
	msgRecover = "recover"
)

// Server-to-client message types.
const (
	msgHello     = "hello"
	msgText      = "text"
	msgSay       = "say"
	msgChat      = "chat"
	msgAnnounce  = "announce"
	msgArrive    = "arrive"
	msgLeave     = "leave"
	msgWhisper   = "whisper"
	msgAuth      = "auth"
	msgStats     = "stats"
	msgRoom      = "room"
	msgInventory = "inventory"
	msgCosmetics = "cosmetics"
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
	codeInvalidName     = "invalid_name"
	codeNameTaken       = "name_taken"
	codeNameOnline      = "name_online"
	codeUnknownUser     = "unknown_user"
	codeBadPassword     = "bad_password" // pre-commit: allow-secret (an error code, not a secret)
	codeFailed          = "failed"
	codeRateLimited     = "rate_limited"
	codeUnknownCommand  = "unknown_command"
	codeInternal        = "internal"
	codeRestarting      = "restarting"
	codeAtCapacity      = "at_capacity"
	codeBadToken        = "bad_token"
	codeReplaced        = "replaced" // error: another connection took this character over
	codeBadRecovery     = "bad_recovery"
	codeRecoveryLocked  = "recovery_locked"
	codeKicked          = "kicked" // a GM removed the character for a while: an error before the close, an auth refusal during the break
)

// inbound is every client-to-server message. Pointer fields tell "missing"
// from "empty".
type inbound struct {
	T        string  `json:"t"`
	Line     *string `json:"line"`
	Username string  `json:"username"`
	Password string  `json:"password"`
	Name     string  `json:"name"`     // join
	Token    string  `json:"token"`    // resume
	Received *uint64 `json:"received"` // resume: counted frames the client has processed; absent means it remembers none
	Code     string  `json:"code"`     // recover: the recovery code
}

// receivedCount is the resume count, or noFramesSeen if the client sent none (a
// freshly loaded page): it gets a gap and a fresh look, not stale replayed frames.
func (in *inbound) receivedCount() uint64 {
	if in.Received == nil {
		return noFramesSeen
	}
	return *in.Received
}

// Outbound messages. Each struct is one frame; T is the type.
type (
	textMsg struct {
		T    string `json:"t"`
		Text string `json:"text"`
	}
	// chatMsg is a global chat line; GM marks a game master's.
	chatMsg struct {
		T     string `json:"t"`
		Who   string `json:"who"`
		Text  string `json:"text"`
		Ch    string `json:"ch"`
		GM    bool   `json:"gm,omitempty"`
		Color string `json:"color,omitempty"` // the sender's active name colour key (cosmetics.go)
		Badge string `json:"badge,omitempty"`
	}
	// announceMsg is a broadcast from the GM (From "gm") or an event ("event"): the client shows a banner.
	announceMsg struct {
		T    string `json:"t"`
		Text string `json:"text"`
		From string `json:"from"`
	}
	sayMsg struct {
		T     string `json:"t"`
		Who   string `json:"who"`
		Text  string `json:"text"`
		Color string `json:"color,omitempty"` // the sender's active name colour key (cosmetics.go)
		Badge string `json:"badge,omitempty"`
	}
	arriveMsg struct {
		T     string `json:"t"`
		Who   string `json:"who"`
		From  string `json:"from,omitempty"`  // direction entered from; empty when they simply appeared
		Color string `json:"color,omitempty"` // the sender's active name colour key (cosmetics.go)
		Badge string `json:"badge,omitempty"`
	}
	leaveMsg struct {
		T     string `json:"t"`
		Who   string `json:"who"`
		To    string `json:"to,omitempty"`    // direction left toward; empty when they vanished
		Color string `json:"color,omitempty"` // the sender's active name colour key (cosmetics.go)
		Badge string `json:"badge,omitempty"`
	}
	whisperMsg struct {
		T     string `json:"t"`
		Who   string `json:"who"`
		Text  string `json:"text"`
		Out   bool   `json:"out,omitempty"`   // your own echo: you whispered to Who
		Color string `json:"color,omitempty"` // the sender's active name colour key (cosmetics.go)
		Badge string `json:"badge,omitempty"`
	}
	authMsg struct {
		T       string `json:"t"`
		OK      bool   `json:"ok"`
		Code    string `json:"code,omitempty"`
		Message string `json:"message"`
		// Guests only, sent once at join: the resume token and the recovery code.
		Token    string `json:"token,omitempty"`
		Recovery string `json:"recovery,omitempty"`
		// Resume only. Gap: some frames could not be replayed, so the client should
		// treat what it shows as stale (a fresh look and stats follow). Base is the
		// client's frame count before any replayed frames; Replayed how many follow.
		Resumed  bool   `json:"resumed,omitempty"`
		Gap      bool   `json:"gap,omitempty"`
		Replayed int    `json:"replayed,omitempty"`
		Base     uint64 `json:"base,omitempty"`
		RetryMS  int    `json:"retry_ms,omitempty"` // recovery_locked: how long until the name accepts codes again
	}
	helloMsg struct {
		T       string `json:"t"`
		Message string `json:"message"`
	}
	errorMsg struct {
		T       string `json:"t"`
		Code    string `json:"code"`
		Message string `json:"message"`
		RetryMS int    `json:"retry_ms,omitempty"` // rate_limited: when to try again
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
		Level    int    `json:"level"`
		XP       int    `json:"xp"`             // total XP
		Next     int    `json:"next,omitempty"` // total XP at which the next level starts; absent at the top
	}
	roomMsg struct {
		T     string   `json:"t"`
		Name  string   `json:"name"`
		Exits []string `json:"exits"`
		// Added by the structured frame; all optional, so an old client ignores them.
		Locked []string    `json:"locked,omitempty"`
		People []roomThing `json:"people,omitempty"`
		Things []roomThing `json:"things,omitempty"`
		Items  []roomItem  `json:"items,omitempty"`
	}
	// cosmeticsMsg is the player's own unlocks and what they wear, for the picker.
	cosmeticsMsg struct {
		T      string   `json:"t"`
		Colors []string `json:"colors"`
		Badges []string `json:"badges"`
		Color  string   `json:"color,omitempty"`
		Badge  string   `json:"badge,omitempty"`
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

func encodeSay(p *MudPlayer, text string) []byte {
	color, badge := p.look()
	return encode(sayMsg{T: msgSay, Who: p.Username, Text: text, Color: color, Badge: badge})
}

func encodeArrive(p *MudPlayer, from string) []byte {
	color, badge := p.look()
	return encode(arriveMsg{T: msgArrive, Who: p.Username, From: from, Color: color, Badge: badge})
}

func encodeLeave(p *MudPlayer, to string) []byte {
	color, badge := p.look()
	return encode(leaveMsg{T: msgLeave, Who: p.Username, To: to, Color: color, Badge: badge})
}

// SendMessage queues a line of game text. It never blocks; it fails only if the
// session is closed or the client has fallen too far behind.
func (mp *MudPlayer) SendMessage(message string) error {
	return mp.sendFrame(encodeText(message))
}

// sendFrame queues a counted frame: it is recorded for replay after a resume.
// Every frame except hello, auth and error is counted, by the server here and by
// the client as it processes them (plans/guest-sessions.md, "Replay"). Fan-out
// calls it with one shared encoding, so the ring holds references, not copies.
func (mp *MudPlayer) sendFrame(frame []byte) error {
	mp.record(frame)
	return mp.Session.Send(frame)
}

// SendWhisper delivers a whisper; out marks the sender's own echo. other is the other party,
// nil when they are not online (their cosmetics are then left out).
func (mp *MudPlayer) SendWhisper(who, text string, out bool, other *MudPlayer) error {
	color, badge := other.look()
	return mp.sendFrame(encode(whisperMsg{T: msgWhisper, Who: who, Text: text, Out: out, Color: color, Badge: badge}))
}

// SendAuth answers a login attempt (join, resume and recover have their own replies).
func (mp *MudPlayer) SendAuth(ok bool, code, message string) error {
	return mp.Session.Send(encode(authMsg{T: msgAuth, OK: ok, Code: code, Message: message}))
}

// SendWelcome tells a player they are in. A new guest's token and recovery code
// travel here, once.
func (mp *MudPlayer) SendWelcome(w welcome) error {
	return mp.Session.Send(encode(authMsg{T: msgAuth, OK: true, Message: w.message, Token: w.token, Recovery: w.recovery,
		Resumed: w.resumed, Gap: w.gap, Base: w.base}))
}

// SendError reports a protocol or server problem that isn't game text.
func (mp *MudPlayer) SendError(code, message string) error {
	return mp.Session.Send(encode(errorMsg{T: msgError, Code: code, Message: message}))
}

// SendInventory sends the inventory listing for the client's inventory view.
func (mp *MudPlayer) SendInventory(text string) error {
	return mp.sendFrame(encode(inventoryMsg{T: msgInventory, Text: text}))
}

// SendStatUpdate pushes the player's current stats for the stats bar.
func (mp *MudPlayer) SendStatUpdate() {
	level, xp, next := mp.xpFields()
	mp.sendFrame(encode(statsMsg{
		Level: level, XP: xp, Next: next,
		T: msgStats, HP: mp.HP, MaxHP: mp.EffectiveMaxHP(),
		MP: mp.MP, MaxMP: mp.EffectiveMaxMP(),
		Atk: mp.EffectiveAttackPower(), AtkBonus: mp.Equipment.TotalBonusAttackPower(),
		Def: mp.EffectiveDefense(), DefBonus: mp.Equipment.TotalBonusDefense(),
	}))
}

// SendLook describes the player's room: the text, then the room's name and exits.
func (mp *MudPlayer) SendLook() {
	room := mp.Location
	mp.SendMessage(room.lookDescription(mp))
	mp.sendRoomFrame()
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

// Session-level replies for the connection's reader goroutine. They go to this
// session whoever the player becomes, and are never counted or replayed.

func (s *Session) sendAuth(ok bool, code, message string) {
	s.Send(encode(authMsg{T: msgAuth, OK: ok, Code: code, Message: message}))
}

func (s *Session) sendError(code, message string) {
	s.Send(encode(errorMsg{T: msgError, Code: code, Message: message}))
}
