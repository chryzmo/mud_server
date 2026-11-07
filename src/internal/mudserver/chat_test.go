package mudserver

import "testing"

func TestMaskReplacesOnlyWordsContainingBlockedWords(t *testing.T) {
	rules := newNameRules("fuck\nshit\n=dick\n")
	for in, want := range map[string]string{
		"hello there":               "hello there",
		"what the FUCK":             "what the ****",
		"sh1t happens, sh_i_t!":     "**** happens, ******!",
		"Dickens wrote Dick-ensian": "Dickens wrote Dick-ensian", // =dick matches a whole word only, and "-" joins parts of a word
		"dick":                      "****",
		"fuuuuck you":               "******* you",
		"naïve café – fine":         "naïve café – fine",
		"  spaced   out  ":          "  spaced   out  ",
		"":                          "",
	} {
		if got := rules.mask(in); got != want {
			t.Errorf("mask(%q) = %q, want %q", in, got, want)
		}
	}
}
