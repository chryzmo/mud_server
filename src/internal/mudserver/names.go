package mudserver

import (
	"bufio"
	_ "embed"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Guest names (plans/guest-sessions.md): 3 to 16 letters, digits, "_" and "-",
// unique ignoring case, not reserved, and not on the blocked-words list.

const (
	minNameLength = 3
	maxNameLength = 16
)

//go:embed blocked_words.txt
var blockedWordsFile string

// reservedNames can't be taken by guests: they look like staff or the system.
var reservedNames = []string{
	"gm", "gamemaster", "admin", "administrator", "mod", "moderator", "staff", "server", "system",
	"mud", "console", "root", "magfest", "you", "me", "all", "everyone", "nobody", "someone",
}

// nameRules checks proposed guest names.
type nameRules struct {
	reserved map[string]bool
	contains []*regexp.Regexp // words blocked anywhere in a name
	parts    []*regexp.Regexp // words blocked when a whole part (split at _ and -) equals them
}

// leet reads the digits people use for letters.
var leet = map[rune]rune{'0': 'o', '1': 'i', '3': 'e', '4': 'a', '5': 's', '7': 't'}

// normalise lower-cases s, reads look-alike digits as letters and drops "_"
// and "-", so "Sh1t" and "s_h_i_t" both become "shit".
func normalise(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r == '_' || r == '-' {
			continue
		}
		if l, ok := leet[r]; ok {
			r = l
		}
		b.WriteRune(r)
	}
	return b.String()
}

// wordPattern matches a word with any letter stretched: "shit" matches "shiit"
// and "shhhit", while "fagg" still needs two g's, so it doesn't match "Fagin".
// Returns nil for an entry that isn't plain letters.
func wordPattern(word string, anchored bool) *regexp.Regexp {
	word = normalise(word)
	if word == "" {
		return nil
	}
	var b strings.Builder
	if anchored {
		b.WriteString("^")
	}
	for _, r := range word {
		if r < 'a' || r > 'z' {
			return nil
		}
		b.WriteRune(r)
		b.WriteString("+")
	}
	if anchored {
		b.WriteString("$")
	}
	return regexp.MustCompile(b.String())
}

// newNameRules parses a blocked-words file (format in blocked_words.txt).
func newNameRules(blockedWords string) *nameRules {
	rules := &nameRules{reserved: make(map[string]bool)}
	for _, name := range reservedNames {
		rules.reserved[normalise(name)] = true
	}
	scanner := bufio.NewScanner(strings.NewReader(blockedWords))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		word, whole := strings.CutPrefix(line, "=")
		if p := wordPattern(word, whole); p != nil {
			if whole {
				rules.parts = append(rules.parts, p)
			} else {
				rules.contains = append(rules.contains, p)
			}
		}
	}
	return rules
}

// defaultNameRules uses the list compiled into the server.
func defaultNameRules() *nameRules { return newNameRules(blockedWordsFile) }

// check returns "" if name is acceptable, otherwise a short, player-safe
// reason. The reason never says which blocked word matched.
func (r *nameRules) check(name string) (code, reason string) {
	if n := utf8.RuneCountInString(name); n < minNameLength {
		return codeInvalidName, "Names need at least 3 characters."
	} else if n > maxNameLength {
		return codeInvalidName, "Names can be at most 16 characters."
	}
	hasLetter := false
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			hasLetter = true
		case c >= '0' && c <= '9', c == '_', c == '-':
		default:
			return codeInvalidName, "Names can use only letters, digits, _ and -."
		}
	}
	if !hasLetter {
		return codeInvalidName, "Names need at least one letter."
	}
	whole := normalise(name)
	if r.reserved[whole] {
		return codeInvalidName, "That name isn't allowed."
	}
	for _, p := range r.contains {
		if p.MatchString(whole) {
			return codeInvalidName, "That name isn't allowed."
		}
	}
	for _, part := range strings.FieldsFunc(name, func(c rune) bool { return c == '_' || c == '-' }) {
		part = normalise(part)
		for _, p := range r.parts {
			if p.MatchString(part) {
				return codeInvalidName, "That name isn't allowed."
			}
		}
	}
	return "", ""
}
