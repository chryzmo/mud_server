package mudserver

import (
	"bytes"
	"encoding/json"
	"testing"
)

// queuedText returns the game text inside a queued text frame.
func queuedText(frame []byte) string {
	var m struct {
		T    string `json:"t"`
		Text string `json:"text"`
	}
	if json.Unmarshal(frame, &m) != nil || m.T != msgText {
		return string(frame)
	}
	return m.Text
}

func TestFrameShapes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame []byte
		want  string
	}{
		{"text", encodeText("\x1b[1;31mGrak\x1b[0m"), `{"t":"text","text":"\u001b[1;31mGrak\u001b[0m"}`},
		{"text is not HTML-escaped", encodeText("a <b> & c"), `{"t":"text","text":"a <b> & c"}`},
		{"say", encodeSay(&MudPlayer{Username: "bob"}, `hi "there"`), `{"t":"say","who":"bob","text":"hi \"there\""}`},
		{"arrive from a direction", encodeArrive(&MudPlayer{Username: "bob"}, "N"), `{"t":"arrive","who":"bob","from":"N"}`},
		{"arrive from nowhere", encodeArrive(&MudPlayer{Username: "bob"}, ""), `{"t":"arrive","who":"bob"}`},
		{"leave toward a direction", encodeLeave(&MudPlayer{Username: "bob"}, "S"), `{"t":"leave","who":"bob","to":"S"}`},
		{"leave vanishing", encodeLeave(&MudPlayer{Username: "bob"}, ""), `{"t":"leave","who":"bob"}`},
		{"say with cosmetics", encodeSay(wearing("bob", "gold", "star"), "hi"), `{"t":"say","who":"bob","text":"hi","color":"gold","badge":"star"}`},
		{"cosmetics", encode(cosmeticsMsg{T: msgCosmetics, Colors: []string{"gold"}, Badges: []string{}, Color: "gold"}), `{"t":"cosmetics","colors":["gold"],"badges":[],"color":"gold"}`},
		{"whisper", encode(whisperMsg{T: msgWhisper, Who: "bob", Text: "psst"}), `{"t":"whisper","who":"bob","text":"psst"}`},
		{"whisper echo", encode(whisperMsg{T: msgWhisper, Who: "bob", Text: "psst", Out: true}), `{"t":"whisper","who":"bob","text":"psst","out":true}`},
		{"auth ok", encode(authMsg{T: msgAuth, OK: true, Message: "hi"}), `{"t":"auth","ok":true,"message":"hi"}`},
		{"auth refused", encode(authMsg{T: msgAuth, Code: codeNameTaken, Message: "taken"}), `{"t":"auth","ok":false,"code":"name_taken","message":"taken"}`},
		{"error", encode(errorMsg{T: msgError, Code: codeRateLimited, Message: "slow"}), `{"t":"error","code":"rate_limited","message":"slow"}`},
		{"room", encode(roomMsg{T: msgRoom, Name: "Hall", Exits: []string{"N", "S"}}), `{"t":"room","name":"Hall","exits":["N","S"]}`},
		{"room without exits", encode(roomMsg{T: msgRoom, Name: "Closet", Exits: []string{}}), `{"t":"room","name":"Closet","exits":[]}`},
	} {
		if got := string(tc.frame); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

// The typed events exist to keep chatter small. Pin the sizes so an envelope
// or escaping change that doubles bytes again fails here, not in a load test.
func TestHotPathFramesStayCompact(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame []byte
		max   int
	}{
		{"heard SAY", encodeSay(&MudPlayer{Username: "bot00042"}, "mudbot hello"), 56},
		{"arrival", encodeArrive(&MudPlayer{Username: "bot00042"}, ""), 36},
		{"departure", encodeLeave(&MudPlayer{Username: "bot00042"}, "N"), 44},
		{"whisper", encode(whisperMsg{T: msgWhisper, Who: "bot00007", Text: "mudbot:b:7:12", Out: true}), 76},
	} {
		if len(tc.frame) > tc.max {
			t.Errorf("%s is %d bytes (%s), want at most %d", tc.name, len(tc.frame), tc.frame, tc.max)
		}
		if bytes.Contains(tc.frame, []byte(`\u001b`)) {
			t.Errorf("%s carries an escaped ANSI code: %s", tc.name, tc.frame)
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

func wearing(name, color, badge string) *MudPlayer {
	p := &MudPlayer{Username: name}
	p.st().active["color"], p.st().active["badge"] = color, badge
	return p
}
