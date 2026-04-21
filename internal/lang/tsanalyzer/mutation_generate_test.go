package tsanalyzer

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
)

// writeAndGenerate is a small harness: write `src` to a temp .ts file,
// generate mutants over the entire file, and return them.
func writeAndGenerate(t *testing.T, src string, disabled map[int]bool) []lang.MutantSite {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "a.ts")
	if err := writeFile(path, []byte(src)); err != nil {
		t.Fatal(err)
	}
	fc := diff.FileChange{
		Path:    "a.ts",
		Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: math.MaxInt32}},
	}
	mutants, err := mutantGeneratorImpl{}.GenerateMutants(path, fc, disabled)
	if err != nil {
		t.Fatal(err)
	}
	return mutants
}

// collectOps returns the counts of operator names from a mutant list.
func collectOps(mutants []lang.MutantSite) map[string]int {
	m := map[string]int{}
	for _, x := range mutants {
		m[x.Operator]++
	}
	return m
}

func TestGenerate_BinaryOps(t *testing.T) {
	src := `function f(x: number): boolean {
    return x > 0;
}
`
	m := writeAndGenerate(t, src, nil)
	ops := collectOps(m)
	if ops["conditional_boundary"] == 0 {
		t.Errorf("expected conditional_boundary, got %v", ops)
	}
}

func TestGenerate_EqualityAndStrict(t *testing.T) {
	src := `function f(a: number, b: number): boolean {
    return a === b;
}
`
	m := writeAndGenerate(t, src, nil)
	ops := collectOps(m)
	// === gets TWO mutants: negate_conditional (flip to !==) and
	// strict_equality (flip to ==).
	if ops["negate_conditional"] == 0 {
		t.Errorf("expected negate_conditional for ===, got %v", ops)
	}
	if ops["strict_equality"] == 0 {
		t.Errorf("expected strict_equality for ===, got %v", ops)
	}
}

func TestGenerate_LooseEquality(t *testing.T) {
	src := `function f(a: any, b: any): boolean {
    return a == b;
}
`
	m := writeAndGenerate(t, src, nil)
	ops := collectOps(m)
	if ops["negate_conditional"] == 0 {
		t.Errorf("expected negate_conditional for ==, got %v", ops)
	}
	if ops["strict_equality"] == 0 {
		t.Errorf("expected strict_equality for ==, got %v", ops)
	}
}

func TestGenerate_Math(t *testing.T) {
	src := `function g(a: number, b: number): number {
    return a + b;
}
`
	m := writeAndGenerate(t, src, nil)
	if collectOps(m)["math_operator"] == 0 {
		t.Errorf("expected math_operator for +, got %v", collectOps(m))
	}
}

func TestGenerate_BooleanLiteral(t *testing.T) {
	src := `function g(): boolean { return true; }
`
	m := writeAndGenerate(t, src, nil)
	if collectOps(m)["boolean_substitution"] == 0 {
		t.Errorf("expected boolean_substitution, got %v", collectOps(m))
	}
}

func TestGenerate_IncDec(t *testing.T) {
	src := `function g(): void {
    let x = 0;
    x++;
}
`
	m := writeAndGenerate(t, src, nil)
	if collectOps(m)["incdec"] == 0 {
		t.Errorf("expected incdec, got %v", collectOps(m))
	}
}

func TestGenerate_ReturnValue(t *testing.T) {
	src := `function g(): number {
    return 42;
}
`
	m := writeAndGenerate(t, src, nil)
	if collectOps(m)["return_value"] == 0 {
		t.Errorf("expected return_value mutant, got %v", collectOps(m))
	}
}

func TestGenerate_NullishToLogicalOr(t *testing.T) {
	src := `function g(a: number | null, b: number): number {
    return a ?? b;
}
`
	m := writeAndGenerate(t, src, nil)
	if collectOps(m)["nullish_to_logical_or"] == 0 {
		t.Errorf("expected nullish_to_logical_or, got %v", collectOps(m))
	}
}

func TestGenerate_OptionalChainRemoval(t *testing.T) {
	src := `function g(x: { a?: number } | null): number | undefined {
    return x?.a;
}
`
	m := writeAndGenerate(t, src, nil)
	if collectOps(m)["optional_chain_removal"] == 0 {
		t.Errorf("expected optional_chain_removal, got %v", collectOps(m))
	}
}

func TestGenerate_BranchRemovalAndStatementDeletion(t *testing.T) {
	src := `function side(): void {}

function g(x: number): void {
    if (x > 0) {
        side();
    }
    side();
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

func TestGenerate_RespectsChangedRegion(t *testing.T) {
	src := `function inRegion(x: number): boolean { return x > 0; }
function outOfRegion(x: number): boolean { return x > 0; }
`
	dir := t.TempDir()
	path := filepath.Join(dir, "a.ts")
	if err := writeFile(path, []byte(src)); err != nil {
		t.Fatal(err)
	}
	fc := diff.FileChange{
		Path:    "a.ts",
		Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 1}},
	}
	mutants, err := mutantGeneratorImpl{}.GenerateMutants(path, fc, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mutants {
		if m.Line != 1 {
			t.Errorf("out-of-region mutant at line %d: %+v", m.Line, m)
		}
	}
}

func TestGenerate_RespectsDisabledLines(t *testing.T) {
	src := `function g(a: number, b: number): boolean {
    return a > b;
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

func TestGenerate_Deterministic(t *testing.T) {
	src := `function g(a: number, b: number): boolean {
    return a > b && b < 10;
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "a.ts")
	if err := writeFile(path, []byte(src)); err != nil {
		t.Fatal(err)
	}
	fc := diff.FileChange{Path: "a.ts", Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 100}}}
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

// TestGenerate_TSXFileProducesMutants smoke-tests that the generator
// works on a .tsx file (the parser picks up the tsx grammar).
func TestGenerate_TSXFileProducesMutants(t *testing.T) {
	absPath, _ := filepath.Abs("testdata/component.tsx")
	fc := diff.FileChange{
		Path:    "testdata/component.tsx",
		Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: math.MaxInt32}},
	}
	mutants, err := mutantGeneratorImpl{}.GenerateMutants(absPath, fc, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The fixture's `if (props.name.length > 0)` produces at least one
	// binary-comparison mutant (conditional_boundary or negate_conditional).
	ops := collectOps(mutants)
	if ops["conditional_boundary"] == 0 {
		t.Errorf("expected conditional_boundary mutant on .tsx, got %v", ops)
	}
}
