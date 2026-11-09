package mudserver

import "testing"

func TestNPCSpeechColoursOnlyTheQuotedWords(t *testing.T) {
	const on, off = "\x1b[1;35m", "\x1b[0m"
	for in, want := range map[string]string{
		`"Welcome."`:                                on + `"Welcome."` + off,
		`The Gatewarden mutters, "Forty."`:          `The Gatewarden mutters, ` + on + `"Forty."` + off,
		`"One," says Mira, "and two."`:              on + `"One,"` + off + ` says Mira, ` + on + `"and two."` + off,
		`A lantern swings.`:                         `A lantern swings.`,
		`An unclosed "quote stays plain`:            `An unclosed "quote stays plain`,
		"A quote across\nlines \"stays\nplain\" ok": "A quote across\nlines \"stays\nplain\" ok",
	} {
		if got := npcSpeech(in); got != want {
			t.Errorf("npcSpeech(%q) = %q, want %q", in, got, want)
		}
	}
}
