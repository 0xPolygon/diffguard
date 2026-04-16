package rustanalyzer

import (
	"path/filepath"
	"testing"

	sitter "github.com/smacker/go-tree-sitter"
)

// TestCognitiveComplexity_ByFixture asserts per-function scores on
// testdata/complexity.rs. The fixture docstrings record each function's
// expected score; this test is the canonical place to assert them.
func TestCognitiveComplexity_ByFixture(t *testing.T) {
	absPath, _ := filepath.Abs("testdata/complexity.rs")
	scores, err := complexityImpl{}.AnalyzeFile(absPath, fullRegion("testdata/complexity.rs"))
	if err != nil {
		t.Fatal(err)
	}
	scoreByName := map[string]int{}
	for _, s := range scores {
		scoreByName[s.Name] = s.Complexity
	}

	cases := []struct {
		name string
		want int
	}{
		{"empty", 0},
		{"one_if", 1},
		{"guarded", 3},
		{"nested", 3},
		{"logical", 3},
		{"unsafe_and_try", 1},
	}
	for _, tc := range cases {
		got, ok := scoreByName[tc.name]
		if !ok {
			t.Errorf("missing score for %q (have %v)", tc.name, scoreByName)
			continue
		}
		if got != tc.want {
			t.Errorf("complexity(%s) = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestComplexityScorer_ReusesCalculator asserts the Scorer (used by the
// churn analyzer) returns the same values as the Calculator — the design
// note explicitly allows reuse and a future refactor to a separate
// approximation would need a deliberate update here.
func TestComplexityScorer_ReusesCalculator(t *testing.T) {
	absPath, _ := filepath.Abs("testdata/complexity.rs")
	calc, err := complexityImpl{}.AnalyzeFile(absPath, fullRegion("testdata/complexity.rs"))
	if err != nil {
		t.Fatal(err)
	}
	score, err := complexityImpl{}.ScoreFile(absPath, fullRegion("testdata/complexity.rs"))
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

// TestLogicalOpChain asserts the operator-switch counter directly. A run
// of the same operator counts as 1; each switch to the other adds 1.
func TestLogicalOpChain(t *testing.T) {
	cases := []struct {
		src  string
		want int
	}{
		{"fn f(a: bool, b: bool) -> bool { a && b }", 1},
		{"fn f(a: bool, b: bool, c: bool) -> bool { a && b && c }", 1},
		{"fn f(a: bool, b: bool, c: bool) -> bool { a && b || c }", 2},
		{"fn f(a: bool, b: bool, c: bool, d: bool) -> bool { a || b && c || d }", 3},
		{"fn f(a: i32) -> bool { a == 1 }", 0},
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

// findFirstLogical returns the outermost binary_expression whose operator
// is && or || — i.e. the root of the logical chain in the source. If no
// such chain is present, returns nil so callers can still exercise the
// "no logical ops" branch of conditionLogicalOps.
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
