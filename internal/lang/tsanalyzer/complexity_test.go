package tsanalyzer

import (
	"path/filepath"
	"testing"

	sitter "github.com/smacker/go-tree-sitter"
)

// TestCognitiveComplexity_ByFixture asserts per-function scores on
// testdata/complexity.ts. The fixture documents each function's expected
// score inline; this test locks them in.
func TestCognitiveComplexity_ByFixture(t *testing.T) {
	absPath, _ := filepath.Abs("testdata/complexity.ts")
	scores, err := complexityImpl{}.AnalyzeFile(absPath, fullRegion("testdata/complexity.ts"))
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]int{}
	for _, s := range scores {
		byName[s.Name] = s.Complexity
	}

	cases := []struct {
		name string
		want int
	}{
		{"empty", 0},
		{"oneIf", 1},
		{"ifElse", 2},
		{"sw", 5},
		{"tryCatch", 2},
		{"ternary", 3},
		{"logical", 3},
		{"notCounted", 1}, // `?.`, `??`, `await`, `async` don't count
		{"promiseCatch", 1},
	}
	for _, tc := range cases {
		got, ok := byName[tc.name]
		if !ok {
			t.Errorf("missing score for %q (have %v)", tc.name, byName)
			continue
		}
		if got != tc.want {
			t.Errorf("complexity(%s) = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestComplexityScorer_ReusesCalculator asserts the Scorer returns the
// same values as the Calculator — matches the design note's reuse policy.
func TestComplexityScorer_ReusesCalculator(t *testing.T) {
	absPath, _ := filepath.Abs("testdata/complexity.ts")
	calc, err := complexityImpl{}.AnalyzeFile(absPath, fullRegion("testdata/complexity.ts"))
	if err != nil {
		t.Fatal(err)
	}
	score, err := complexityImpl{}.ScoreFile(absPath, fullRegion("testdata/complexity.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(calc) != len(score) {
		t.Fatalf("counts differ: calc=%d score=%d", len(calc), len(score))
	}
	for i := range calc {
		if calc[i].Name != score[i].Name || calc[i].Complexity != score[i].Complexity {
			t.Errorf("row %d differs: calc=%+v score=%+v", i, calc[i], score[i])
		}
	}
}

// TestLogicalOpChain directly asserts the operator-switch counter.
func TestLogicalOpChain(t *testing.T) {
	cases := []struct {
		src  string
		want int
	}{
		{"const f = (a: boolean, b: boolean) => a && b", 1},
		{"const f = (a: boolean, b: boolean, c: boolean) => a && b && c", 1},
		{"const f = (a: boolean, b: boolean, c: boolean) => a && b || c", 2},
		{"const f = (a: boolean, b: boolean, c: boolean, d: boolean) => a || b && c || d", 3},
		{"const f = (a: number) => a === 1", 0},
	}
	for _, tc := range cases {
		tree, err := parseBytes([]byte(tc.src))
		if err != nil {
			t.Fatalf("parseBytes(%q): %v", tc.src, err)
		}
		target := findFirstLogical(tree.RootNode())
		got := conditionLogicalOps(target)
		if got != tc.want {
			t.Errorf("conditionLogicalOps(%q) = %d, want %d", tc.src, got, tc.want)
		}
		tree.Close()
	}
}

func findFirstLogical(root *sitter.Node) *sitter.Node {
	var hit *sitter.Node
	walk(root, func(n *sitter.Node) bool {
		if hit != nil {
			return false
		}
		if n.Type() != "binary_expression" {
			return true
		}
		op := n.ChildByFieldName("operator")
		if op == nil {
			return true
		}
		if op.Type() == "&&" || op.Type() == "||" {
			hit = n
			return false
		}
		return true
	})
	return hit
}

// TestComplexity_OptionalChainingNotCounted is a regression guard that
// optional chaining `?.` and nullish coalescing `??` do NOT increment the
// score. A function containing only these constructs must score 0.
func TestComplexity_OptionalChainingNotCounted(t *testing.T) {
	src := `function f(x: { a?: { b?: number } } | null): number {
    const v = x?.a?.b ?? 0;
    return v;
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "a.ts")
	if err := writeFile(path, []byte(src)); err != nil {
		t.Fatal(err)
	}
	scores, err := complexityImpl{}.AnalyzeFile(path, fullRegion("a.ts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range scores {
		if s.Name == "f" && s.Complexity != 0 {
			t.Errorf("optional chain + nullish should score 0, got %d", s.Complexity)
		}
	}
}
