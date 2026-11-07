package worldfile

import (
	"strings"
	"testing"
)

const goodPuzzle = `puzzles:
  lever:
    title: The Stubborn Lever
    steps:
      - {flag: done, line: "The lever clicked twice."}
    solved: {flag: done, line: "The trapdoor opened."}
    hints: ["Try the lever.", "Pull it more than once."]
`

func withPuzzle(puzzle string) map[string]string {
	files := ruled(goodRules, "")
	files["p.yaml"] = puzzle
	return files
}

func TestAGoodPuzzleDeclarationIsClean(t *testing.T) {
	res := validate(t, withPuzzle(goodPuzzle))
	if len(res.Errors) != 0 || len(res.Warnings) != 0 {
		t.Fatalf("errors %v warnings %v", res.Errors, res.Warnings)
	}
}

func TestPuzzleProblems(t *testing.T) {
	long := strings.Repeat("x", 400)
	cases := []struct{ name, puzzle, want string }{
		{"no title", strings.Replace(goodPuzzle, "title: The Stubborn Lever", "title: ''", 1), "a title of 1 to 60"},
		{"bad key", strings.Replace(goodPuzzle, "  lever:", "  Bad Key:", 1), "lower case words"},
		{"no steps", strings.Replace(goodPuzzle, `      - {flag: done, line: "The lever clicked twice."}`, "      []", 1), "0 steps"},
		{"step flag nothing sets", strings.Replace(goodPuzzle, "{flag: done, line: \"The lever", "{flag: nope, line: \"The lever", 1), `flag "nope" is never set`},
		{"step without a line", strings.Replace(goodPuzzle, `, line: "The lever clicked twice."`, "", 1), "a flag and a line are both required"},
		{"no solved flag", strings.Replace(goodPuzzle, `    solved: {flag: done, line: "The trapdoor opened."}`, "", 1), "a flag and a line are both required"},
		{"long line", strings.Replace(goodPuzzle, "The trapdoor opened.", long, 1), "at most 200"},
		{"too many hints", strings.Replace(goodPuzzle, `["Try the lever.", "Pull it more than once."]`, `[a, b, c, d, e, f]`, 1), "6 hints; at most 5"},
		{"long hint", strings.Replace(goodPuzzle, "Try the lever.", long, 1), "a hint of 1 to 300"},
	}
	for _, c := range cases {
		res := validate(t, withPuzzle(c.puzzle))
		if !has(res.Errors, c.want) {
			t.Errorf("%s: want an error containing %q, got %v", c.name, c.want, res.Errors)
		}
	}
	res := validate(t, withPuzzle(strings.Replace(goodPuzzle, `    hints: ["Try the lever.", "Pull it more than once."]`, "", 1)))
	if len(res.Errors) != 0 || !has(res.Warnings, "no hints") {
		t.Errorf("a puzzle without hints should only warn: errors %v warnings %v", res.Errors, res.Warnings)
	}
}

func TestDuplicatePuzzleAcrossFilesAndLongRegion(t *testing.T) {
	files := withPuzzle(goodPuzzle)
	files["q.yaml"] = goodPuzzle
	if res := validate(t, files); !has(res.Errors, "duplicate puzzle key") {
		t.Errorf("errors %v", res.Errors)
	}
	files = ruled(strings.Replace(goodRules, "    rules:", "    region: "+strings.Repeat("r", 50)+"\n    rules:", 1), "")
	if res := validate(t, files); !has(res.Errors, "a region name is at most 40") {
		t.Errorf("errors %v", res.Errors)
	}
}

func TestRenderShowsPuzzlesAndRegions(t *testing.T) {
	files := withPuzzle(goodPuzzle)
	files["w.yaml"] = strings.Replace(files["w.yaml"], "    exits: {E: yard}\n", "    exits: {E: yard}\n    region: Atrium\n", 1)
	out := render(t, files)
	for _, want := range []string{"region Atrium", "## Puzzles", "The Stubborn Lever", "1. The lever clicked twice. (flag `done`)", "**Solved:** The trapdoor opened.", "- hint 2: Pull it more than once."} {
		if !strings.Contains(out, want) {
			t.Errorf("overview is missing %q\n%s", want, out)
		}
	}
}
