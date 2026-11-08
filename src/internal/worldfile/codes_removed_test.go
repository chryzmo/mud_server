package worldfile

import (
	"strings"
	"testing"
)

// Sticker codes were dropped (plans/event-systems.md, decision 33): a content file that still has a
// codes block is refused, and says why, instead of the codes being quietly ignored.
func TestAContentFileWithAStickerCodesBlockIsRefused(t *testing.T) {
	_, res, _ := ValidateDir(write(t, map[string]string{"w.yaml": roomWith + "codes:\n  old:\n    hash: abc\n    color: gold\n"}))
	var all []string
	for _, p := range res.Errors {
		all = append(all, p.String())
	}
	if !strings.Contains(strings.Join(all, "\n"), "codes") {
		t.Errorf("a codes block should be an error, got %v", all)
	}
}
