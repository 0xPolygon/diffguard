package rustanalyzer

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
)

// writeAndGenerate is a small harness: write `src` to a temp .rs file,
// generate mutants over the entire file, and return them.
func writeAndGenerate(t *testing.T, src string, disabled map[int]bool) []lang.MutantSite {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "a.rs")
	if err := writeFile(path, []byte(src)); err != nil {
		t.Fatal(err)
	}
	fc := diff.FileChange{
		Path:    "a.rs",
		Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: math.MaxInt32}},
	}
	mutants, err := mutantGeneratorImpl{}.GenerateMutants(path, fc, disabled)
	if err != nil {
		t.Fatal(err)
	}
	return mutants
}

// collectOps returns the sorted set of operator names from a mutant list.
func collectOps(mutants []lang.MutantSite) map[string]int {
	m := map[string]int{}
	for _, x := range mutants {
		m[x.Operator]++
	}
	return m
}

func TestGenerate_BinaryOps(t *testing.T) {
	src := `fn f(x: i32) -> bool {
    x > 0
}
`
	m := writeAndGenerate(t, src, nil)
	ops := collectOps(m)
	if ops["conditional_boundary"] == 0 {
		t.Errorf("expected conditional_boundary mutant, got %v", ops)
	}
}

func TestGenerate_EqualityAndMath(t *testing.T) {
	src := `fn g(a: i32, b: i32) -> bool {
    a == b
}

fn h(a: i32, b: i32) -> i32 {
    a + b
}
`
	m := writeAndGenerate(t, src, nil)
	ops := collectOps(m)
	if ops["negate_conditional"] == 0 {
		t.Errorf("expected negate_conditional for ==, got %v", ops)
	}
	if ops["math_operator"] == 0 {
		t.Errorf("expected math_operator for +, got %v", ops)
	}
}

func TestGenerate_BooleanLiteral(t *testing.T) {
	src := `fn g() -> bool { true }
`
	m := writeAndGenerate(t, src, nil)
	if collectOps(m)["boolean_substitution"] == 0 {
		t.Errorf("expected boolean_substitution, got %v", collectOps(m))
	}
}

func TestGenerate_ReturnValue(t *testing.T) {
	src := `fn g() -> i32 {
    return 42;
}
`
	m := writeAndGenerate(t, src, nil)
	if collectOps(m)["return_value"] == 0 {
		t.Errorf("expected return_value mutant, got %v", collectOps(m))
	}
}

func TestGenerate_SomeToNone(t *testing.T) {
	src := `fn g(x: i32) -> Option<i32> {
    return Some(x);
}
`
	m := writeAndGenerate(t, src, nil)
	ops := collectOps(m)
	if ops["some_to_none"] == 0 {
		t.Errorf("expected some_to_none mutant, got %v", ops)
	}
	// The generator also emits a generic return_value on the same line —
	// that's expected.
	if ops["return_value"] == 0 {
		t.Errorf("expected return_value companion, got %v", ops)
	}
}

func TestGenerate_UnwrapRemoval(t *testing.T) {
	src := `fn g(x: Option<i32>) -> i32 {
    x.unwrap()
}
`
	m := writeAndGenerate(t, src, nil)
	if collectOps(m)["unwrap_removal"] == 0 {
		t.Errorf("expected unwrap_removal mutant, got %v", collectOps(m))
	}
}

func TestGenerate_ExpectBecomesUnwrapRemoval(t *testing.T) {
	src := `fn g(x: Option<i32>) -> i32 {
    x.expect("boom")
}
`
	m := writeAndGenerate(t, src, nil)
	if collectOps(m)["unwrap_removal"] == 0 {
		t.Errorf("expected unwrap_removal mutant for .expect, got %v", collectOps(m))
	}
}

func TestGenerate_QuestionMarkRemoval(t *testing.T) {
	src := `fn g(x: Result<i32, ()>) -> Result<i32, ()> {
    let v = x?;
    Ok(v)
}
`
	m := writeAndGenerate(t, src, nil)
	if collectOps(m)["question_mark_removal"] == 0 {
		t.Errorf("expected question_mark_removal mutant, got %v", collectOps(m))
	}
}

func TestGenerate_BranchRemovalAndStatementDeletion(t *testing.T) {
	// Uses a plain function call (not a macro) for the statement-deletion
	// case. Tree-sitter models `println!(...)` as a macro_invocation, so
	// we'd miss it; bare `side_effect()` is parsed as a call_expression
	// wrapped in an expression_statement, which is what the generator
	// looks for.
	src := `fn side_effect() {}

fn g(x: i32) {
    if x > 0 {
        side_effect();
    }
    side_effect();
}
`
	m := writeAndGenerate(t, src, nil)
	ops := collectOps(m)
	if ops["branch_removal"] == 0 {
		t.Errorf("expected branch_removal, got %v", ops)
	}
	if ops["statement_deletion"] == 0 {
		t.Errorf("expected statement_deletion for bare call, got %v", ops)
	}
}

// TestGenerate_RespectsChangedRegion asserts out-of-region mutants are
// dropped.
func TestGenerate_RespectsChangedRegion(t *testing.T) {
	src := `fn in_region(x: i32) -> bool { x > 0 }
fn out_of_region(x: i32) -> bool { x > 0 }
`
	dir := t.TempDir()
	path := filepath.Join(dir, "a.rs")
	if err := writeFile(path, []byte(src)); err != nil {
		t.Fatal(err)
	}
	// Region covers only line 1. Line 2's binary_expression should be dropped.
	fc := diff.FileChange{
		Path:    "a.rs",
		Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 1}},
	}
	mutants, err := mutantGeneratorImpl{}.GenerateMutants(path, fc, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mutants {
		if m.Line != 1 {
			t.Errorf("got out-of-region mutant at line %d: %+v", m.Line, m)
		}
	}
}

// TestGenerate_RespectsDisabledLines asserts disabledLines suppress
// mutants on those lines.
func TestGenerate_RespectsDisabledLines(t *testing.T) {
	src := `fn g(a: i32, b: i32) -> bool {
    a > b
}
`
	disabled := map[int]bool{2: true}
	m := writeAndGenerate(t, src, disabled)
	for _, x := range m {
		if x.Line == 2 {
			t.Errorf("mutant on disabled line 2: %+v", x)
		}
	}
}

// TestGenerate_Deterministic asserts repeated calls produce byte-identical
// results. Stable ordering is a critical property for the exit-code gate.
func TestGenerate_Deterministic(t *testing.T) {
	src := `fn g(a: i32, b: i32) -> bool {
    a > b && b < 10
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "a.rs")
	if err := writeFile(path, []byte(src)); err != nil {
		t.Fatal(err)
	}
	fc := diff.FileChange{Path: "a.rs", Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 100}}}
	first, _ := mutantGeneratorImpl{}.GenerateMutants(path, fc, nil)
	second, _ := mutantGeneratorImpl{}.GenerateMutants(path, fc, nil)
	if len(first) != len(second) {
		t.Fatalf("lengths differ: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("row %d differs: %+v vs %+v", i, first[i], second[i])
		}
	}
}
