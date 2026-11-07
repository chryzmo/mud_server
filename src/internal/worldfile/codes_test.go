package worldfile

import (
	"bytes"
	"strings"
	"testing"
)

func TestNormalizeCodeIgnoresCaseSpacingAndDashes(t *testing.T) {
	for _, in := range []string{"abcd-2345", "ABCD 2345", " a-b-c-d-2-3-4-5 ", "abcd_2345"} {
		if got, ok := NormalizeCode(in); !ok || got != "ABCD2345" {
			t.Errorf("NormalizeCode(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "ABCD-234", "ABCD-23456", "ABCD-2O45", "ABCD-2145", "ABCD-23!5", "ÅBCD-2345"} {
		if got, ok := NormalizeCode(in); ok {
			t.Errorf("NormalizeCode(%q) = %q, want a refusal", in, got)
		}
	}
}

func TestHashCodeDependsOnTheSecret(t *testing.T) {
	a, b := HashCode("one", "ABCD2345"), HashCode("two", "ABCD2345")
	if a == b || len(a) != 64 || a != HashCode("one", "ABCD2345") {
		t.Errorf("hashes %s %s", a, b)
	}
}

func TestNewCodesAreWellFormedAndUnique(t *testing.T) {
	var out, errs bytes.Buffer
	if code := RunNewCodes([]string{"50"}, "s", &out, &errs); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	seen := map[string]bool{}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 50 {
		t.Fatalf("%d lines", len(lines))
	}
	for _, l := range lines {
		f := strings.Fields(l)
		n, ok := NormalizeCode(f[0])
		if !ok || seen[n] || f[1] != HashCode("s", n) {
			t.Errorf("bad line %q", l)
		}
		seen[n] = true
	}
	if RunNewCodes([]string{"5"}, "", &out, &errs) != 2 || RunNewCodes([]string{"0"}, "s", &out, &errs) != 2 {
		t.Error("a missing secret or a bad count should be a usage error")
	}
}

func TestValidateCodes(t *testing.T) {
	good := strings.Repeat("a", 64)
	w, res, err := ValidateDir(write(t, map[string]string{"w.yaml": `
start_room: a
items: {gem: {name: gem, description: d}}
rooms: {a: {name: A, description: d}}
codes:
  fine: {hash: ` + good + `, color: gold, badge: star, item: gem, flag: seen-it, message: hi}
  no-hash: {color: gold}
  dup: {hash: ` + good + `, color: gold}
  nothing: {hash: ` + strings.Repeat("b", 64) + `}
  bad-color: {hash: ` + strings.Repeat("c", 64) + `, color: "red; background:url(x)"}
  bad-badge: {hash: ` + strings.Repeat("d", 64) + `, badge: skull}
  bad-item: {hash: ` + strings.Repeat("e", 64) + `, item: nope}
  Bad_Key: {hash: ` + strings.Repeat("f", 64) + `, flag: "Not A Flag"}
`}))
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Codes) != 8 {
		t.Fatalf("%d codes", len(w.Codes))
	}
	var errs []string
	for _, p := range res.Errors {
		errs = append(errs, p.String())
	}
	all := strings.Join(errs, "\n")
	for _, want := range []string{
		"codes.no-hash.hash: must be 64", "codes.fine.hash: the same hash as code \"dup\"",
		"codes.nothing: a code must grant something", "codes.bad-color.color: \"red; background:url(x)\" is not a colour",
		"codes.bad-badge.badge", "codes.bad-item.item: \"nope\" is not an item", "codes.Bad_Key: code keys", "codes.Bad_Key.flag",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "codes.fine.color") || strings.Contains(all, "codes.fine.item") || strings.Contains(all, "codes.fine.flag") {
		t.Errorf("the good code was flagged:\n%s", all)
	}
}

func TestRenderListsRewardsButNeverHashes(t *testing.T) {
	hash := strings.Repeat("9", 64)
	out := render(t, map[string]string{"w.yaml": `
start_room: a
rooms: {a: {name: A, description: d}}
codes:
  founder: {hash: ` + hash + `, color: gold, badge: star, message: "Hello."}
`})
	if !strings.Contains(out, "## Sticker codes") || !strings.Contains(out, "`founder`: colour `gold`; badge `star`; message: Hello.") {
		t.Errorf("overview:\n%s", out)
	}
	if strings.Contains(out, hash) {
		t.Error("the overview shows a code hash")
	}
}
