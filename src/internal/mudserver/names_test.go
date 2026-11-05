package mudserver

import (
	"regexp"
	"strings"
	"testing"
)

func TestNameRules(t *testing.T) {
	rules := defaultNameRules()
	for _, ok := range []string{
		"Ann", "bob", "Zed_1", "Mr-Fluffy", "Player42", "A1b", "Sixteen_chars_ok", "NINJA",
		// Innocent names that contain or resemble blocked words must pass.
		"Nigel", "Fagin", "Classic", "Cocktail", "Peacock", "Assassin", "Dickens", "Grape", "Therapist",
		"Analyst", "Hitchcock", "Bassist", "Essex", "Titan", "Cumberland", "Sussex", "Passage", "Sushi",
	} {
		if code, reason := rules.check(ok); code != "" {
			t.Errorf("%q refused: %s %s", ok, code, reason)
		}
	}
}

func TestNameRulesRefuseBadShapes(t *testing.T) {
	rules := defaultNameRules()
	for name, want := range map[string]string{
		"":                   "at least 3",
		"ab":                 "at least 3",
		"Seventeen_chars_xx": "at most 16",
		"two words":          "only letters",
		"tab\there":          "only letters",
		"emoji😀":             "only letters",
		"café":               "only letters",
		"a.b.c":              "only letters",
		"<b>bold":            "only letters",
		"12345":              "at least one letter",
		"___":                "at least one letter",
		"-1-":                "at least one letter",
	} {
		code, reason := rules.check(name)
		if code != codeInvalidName || !strings.Contains(reason, want) {
			t.Errorf("%q: got %q %q, want invalid_name containing %q", name, code, reason, want)
		}
	}
}

func TestNameRulesReserved(t *testing.T) {
	rules := defaultNameRules()
	for _, name := range []string{"Admin", "ADMIN", "a-d-m-i-n", "4dmin", "Server", "System", "Moderator", "MAGFest", "m_o_d", "Staff", "gm_", "G-M"} {
		if code, _ := rules.check(name); code == "" {
			t.Errorf("reserved name %q was accepted", name)
		}
	}
}

// The substring list trades a few innocent names for catching disguised words:
// "Scunthorpe" contains "cunt". Pin the known case so it is a decision, not a surprise.
func TestKnownFalsePositive(t *testing.T) {
	if code, _ := defaultNameRules().check("Scunthorpe"); code == "" {
		t.Error("Scunthorpe is now allowed; if the list changed on purpose, update this test")
	}
}

func TestBlockedWordsMatchDisguisesButNotInnocentNames(t *testing.T) {
	rules := newNameRules("# comment\n\nshit\nfagg\n=dick\n=ass\n")
	for _, name := range []string{"shit", "Shit", "SHIT", "sh1t", "shiiit", "s_h_i_t", "s-h-i-t", "xxshitxx", "Big_Dick", "dIcK", "d1ck", "diiick", "faggot", "faaagg", "Ass", "big-ass", "a55"} {
		if len(name) < minNameLength {
			t.Fatalf("test name %q is too short to reach the word check", name)
		}
		if code, _ := rules.check(name); code == "" {
			t.Errorf("%q should be blocked", name)
		}
	}
	for _, name := range []string{"Fagin", "Fagan", "Dickens", "Classic", "Assassin", "Passage", "Sushi"} {
		if code, _ := rules.check(name); code != "" {
			t.Errorf("%q should be allowed", name)
		}
	}
}

func TestBlockedWordsRefusalNeverRevealsTheWord(t *testing.T) {
	rules := newNameRules("shit\n")
	code, reason := rules.check("xshitx")
	if code != codeInvalidName || strings.Contains(strings.ToLower(reason), "shit") {
		t.Errorf("got %q %q", code, reason)
	}
}

func TestShippedBlockedWordsParse(t *testing.T) {
	rules := defaultNameRules()
	if len(rules.contains) < 10 || len(rules.parts) < 10 {
		t.Errorf("shipped list parsed to %d substring and %d whole-part entries", len(rules.contains), len(rules.parts))
	}
	for _, line := range strings.Split(blockedWordsFile, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		word := strings.TrimPrefix(line, "=")
		if !regexp.MustCompile(`^[a-z0-9]+$`).MatchString(word) {
			t.Errorf("blocked_words.txt entry %q is not lower-case letters and digits, so it can never match", line)
		}
	}
}
