package mudserver

import (
	"encoding/json"
	"testing"

	"mud_server/src/internal/mudutils"
)

// queuedText returns the game text inside a queued text frame.
func queuedText(frame []byte) string {
	var env struct {
		T string `json:"t"`
		D struct {
			Text string `json:"text"`
		} `json:"d"`
	}
	if json.Unmarshal(frame, &env) != nil || env.T != msgText {
		return string(frame)
	}
	return env.D.Text
}

func TestTextKindFollowsLeadingColour(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{mudutils.ColorString("Grak", mudutils.COLOR_RED) + " looks at you.", "combat"},
		{mudutils.ColorString("bob", mudutils.COLOR_BLUE) + " says: \"hi\"", "player"},
		{mudutils.ColorString("sword", mudutils.COLOR_GREEN) + " lies here.", "item"},
		{mudutils.ColorString("You whisper", mudutils.COLOR_MAGENTA), "plain"},
		{"A plain line.", "plain"},
		{"A line with " + mudutils.ColorString("Grak", mudutils.COLOR_RED) + " inside.", "plain"},
		{"", "plain"},
	} {
		if got := textKind(tc.text); got != tc.want {
			t.Errorf("textKind(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestEnvelopeShapes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame []byte
		want  string
	}{
		{"text", encodeText("\x1b[1;31mGrak\x1b[0m"), `{"v":1,"t":"text","d":{"text":"\u001b[1;31mGrak\u001b[0m","kind":"combat"}}`},
		{"auth ok", encodeEnvelope(msgAuth, authPayload{OK: true, Message: "hi"}), `{"v":1,"t":"auth","d":{"ok":true,"message":"hi"}}`},
		{"auth refused", encodeEnvelope(msgAuth, authPayload{Code: codeNameTaken, Message: "taken"}), `{"v":1,"t":"auth","d":{"ok":false,"code":"name_taken","message":"taken"}}`},
		{"error", encodeEnvelope(msgError, errorPayload{Code: codeRateLimited, Message: "slow"}), `{"v":1,"t":"error","d":{"code":"rate_limited","message":"slow"}}`},
		{"room", encodeEnvelope(msgRoom, roomPayload{Name: "Hall", Exits: []string{"N", "S"}}), `{"v":1,"t":"room","d":{"name":"Hall","exits":["N","S"]}}`},
		{"room without exits", encodeEnvelope(msgRoom, roomPayload{Name: "Closet", Exits: []string{}}), `{"v":1,"t":"room","d":{"name":"Closet","exits":[]}}`},
	} {
		if got := string(tc.frame); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

// Fan-out formats once: every recipient's queue holds the same bytes, not a
// copy marshalled per player (capacity rule 2).
func TestBroadcastQueuesOneSharedFrame(t *testing.T) {
	room := &Room{UserMap: make(map[string]*MudPlayer)}
	speaker := npcTestPlayer("speaker")
	room.AddPlayer(speaker)
	listeners := []*MudPlayer{npcTestPlayer("a"), npcTestPlayer("b"), npcTestPlayer("c")}
	for _, p := range listeners {
		room.AddPlayer(p)
	}
	room.Broadcast("hello", "speaker")

	if len(speaker.Session.out) != 0 {
		t.Error("the sender received their own broadcast")
	}
	first := <-listeners[0].Session.out
	if queuedText(first) != "hello" {
		t.Fatalf("broadcast text = %q", queuedText(first))
	}
	for _, p := range listeners[1:] {
		got := <-p.Session.out
		if &got[0] != &first[0] {
			t.Errorf("%s was queued a separate copy of the frame", p.Username)
		}
	}
}
