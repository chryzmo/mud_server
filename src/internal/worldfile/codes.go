package worldfile

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strings"
)

/*
Sticker codes (plans/event-systems.md section 5). A code is eight characters from a 32-letter
alphabet (40 bits) printed as XXXX-XXXX. Content never holds a code, only its HMAC-SHA256 under
a secret kept on the host (MUD_CODE_SECRET), so a copy of the content or of the image does not
reveal the stickers, and a guess cannot be tested offline without the secret. A code grants
cosmetics, an item, a flag or a message; never points.
*/

// CodeAlphabet leaves out I and O so a printed code cannot be misread as 1 or 0.
const CodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// CodeLength is the number of characters in a code, without the dash.
const CodeLength = 8

// Colors and Badges are the fixed sets the client can draw. A code names one by key, never CSS
// or text, so a code cannot inject styling and every colour can be contrast-tested in the client.
var (
	Colors = []string{"gold", "crimson", "azure", "emerald", "violet", "amber", "rose", "silver"}
	Badges = []string{"star", "crown", "key", "lantern", "bell", "gear", "quill", "flame"}
)

// MaxCodeMessage is the longest message a code may show.
const MaxCodeMessage = 300

var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Code is what a sticker grants. Hash is the code's HMAC (see HashCode); at least one reward is required.
type Code struct {
	Hash    string `yaml:"hash"`
	Color   string `yaml:"color"`
	Badge   string `yaml:"badge"`
	Item    string `yaml:"item"`    // an item key, put in the player's inventory
	Flag    string `yaml:"flag"`    // a per-player flag, for rules to read
	Message string `yaml:"message"` // shown on redeeming
}

// NormalizeCode reads a typed code: case, spaces, dashes and other punctuation do not matter.
// It reports false unless what remains is a well-formed code.
func NormalizeCode(s string) (string, bool) {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '-', r == '_', r == '.':
		default:
			return "", false
		}
	}
	n := b.String()
	if len(n) != CodeLength {
		return "", false
	}
	for _, r := range n {
		if !strings.ContainsRune(CodeAlphabet, r) {
			return "", false
		}
	}
	return n, true
}

// HashCode is the hex HMAC-SHA256 of a normalized code under secret.
func HashCode(secret, normalized string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(normalized))
	return hex.EncodeToString(m.Sum(nil))
}

// NewCode makes a random code, normalized, from r (crypto/rand in production).
func NewCode(r io.Reader) (string, error) {
	var b [CodeLength]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", err
	}
	out := make([]byte, CodeLength)
	for i, v := range b {
		out[i] = CodeAlphabet[int(v)%len(CodeAlphabet)] // 256 is a multiple of 32: no bias
	}
	return string(out), nil
}

// FormatCode shows a normalized code as XXXX-XXXX.
func FormatCode(n string) string { return n[:4] + "-" + n[4:] }

// RunNewCodes is the -new-codes command: it prints N fresh codes with their hashes under
// MUD_CODE_SECRET (passed as secret), one "CODE hash" pair per line. The codes go on stickers and
// are never committed; the hashes go into the content's codes: section.
func RunNewCodes(args []string, secret string, stdout, stderr io.Writer) int {
	n := 0
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: MUD_CODE_SECRET=... mud_server -new-codes N")
		return 2
	}
	if _, err := fmt.Sscanf(args[0], "%d", &n); err != nil || n < 1 || n > 1000 {
		fmt.Fprintln(stderr, "new-codes: N must be a number from 1 to 1000")
		return 2
	}
	if secret == "" {
		fmt.Fprintln(stderr, "new-codes: MUD_CODE_SECRET is not set (use the same secret as the server)")
		return 2
	}
	seen := map[string]bool{}
	for len(seen) < n {
		c, err := NewCode(rand.Reader)
		if err != nil {
			fmt.Fprintln(stderr, "new-codes:", err)
			return 1
		}
		if !seen[c] {
			seen[c] = true
			fmt.Fprintf(stdout, "%s %s\n", FormatCode(c), HashCode(secret, c))
		}
	}
	return 0
}

func (w *World) validateCodes(res *Result) {
	byHash := map[string]string{}
	for _, key := range sortedKeys(w.Codes) {
		c := w.Codes[key]
		f, p := w.source["code:"+key], "codes."+key
		if !keyPattern.MatchString(key) {
			res.errorf(f, p, "code keys are lower case words joined by dashes")
		}
		switch {
		case !hashPattern.MatchString(c.Hash):
			res.errorf(f, p+".hash", "must be 64 lower-case hex characters (make one with mud_server -new-codes)")
		case byHash[c.Hash] != "":
			res.errorf(f, p+".hash", "the same hash as code %q", byHash[c.Hash])
		default:
			byHash[c.Hash] = key
		}
		if c.Color == "" && c.Badge == "" && c.Item == "" && c.Flag == "" && c.Message == "" {
			res.errorf(f, p, "a code must grant something (color, badge, item, flag or message)")
		}
		if c.Color != "" && !contains(Colors, c.Color) {
			res.errorf(f, p+".color", "%q is not a colour (%s)", c.Color, strings.Join(Colors, ", "))
		}
		if c.Badge != "" && !contains(Badges, c.Badge) {
			res.errorf(f, p+".badge", "%q is not a badge (%s)", c.Badge, strings.Join(Badges, ", "))
		}
		if c.Item != "" {
			if _, ok := w.Items[c.Item]; !ok {
				res.errorf(f, p+".item", "%q is not an item", c.Item)
			}
		}
		if c.Flag != "" && !keyPattern.MatchString(c.Flag) {
			res.errorf(f, p+".flag", "flag names are lower case words joined by dashes")
		}
		if len(c.Message) > MaxCodeMessage {
			res.warnf(f, p+".message", "is %d characters; keep it under %d", len(c.Message), MaxCodeMessage)
		}
	}
}
