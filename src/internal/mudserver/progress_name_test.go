package mudserver

import "testing"

func TestWithThe(t *testing.T) {
	for in, want := range map[string]string{
		"Founders' Fountain": "the Founders' Fountain",
		"lever":              "the lever",
		"The Gargoyles":      "the Gargoyles",
		"the clock":          "the clock",
		"Theatre":            "the Theatre",
	} {
		if got := withThe(in); got != want {
			t.Errorf("withThe(%q) = %q, want %q", in, got, want)
		}
	}
}
