package tsanalyzer

import (
	"math"
	"path/filepath"
	"sort"
	"testing"

	"github.com/0xPolygon/diffguard/internal/diff"
)

// fullRegion returns a FileChange covering every line so tests can assert
// against every function in a fixture without threading line numbers.
func fullRegion(path string) diff.FileChange {
	return diff.FileChange{
		Path:    path,
		Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: math.MaxInt32}},
	}
}

func TestExtractFunctions_AllForms(t *testing.T) {
	absPath, err := filepath.Abs("testdata/functions.ts")
	if err != nil {
		t.Fatal(err)
	}
	s := sizesImpl{}
	fns, fsize, err := s.ExtractFunctions(absPath, fullRegion("testdata/functions.ts"))
	if err != nil {
		t.Fatalf("ExtractFunctions: %v", err)
	}
	if fsize == nil {
		t.Fatal("expected non-nil file size")
	}
	if fsize.Lines == 0 {
		t.Error("file size reports zero lines")
	}

	names := make([]string, 0, len(fns))
	for _, fn := range fns {
		names = append(names, fn.Name)
	}
	sort.Strings(names)

	// The fixture declares: standalone, arrowConst (arrow assigned to
	// const), fnExpr (function expression assigned to const), the Counter
	// class (constructor + increment + make + reset), the nested
	// arrow inside increment (as its own bare name since it's an
	// arrow assigned to const), and the gen generator.
	mustHave := []string{
		"standalone",
		"arrowConst",
		"fnExpr",
		"Counter.constructor",
		"Counter.increment",
		"Counter.make",
		"Counter.reset",
		"nestedHelper",
		"gen",
	}
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	for _, want := range mustHave {
		if !set[want] {
			t.Errorf("missing expected function %q (got %v)", want, names)
		}
	}
}

func TestExtractFunctions_LineRanges(t *testing.T) {
	absPath, _ := filepath.Abs("testdata/functions.ts")
	fns, _, err := sizesImpl{}.ExtractFunctions(absPath, fullRegion("testdata/functions.ts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range fns {
		if fn.Line <= 0 {
			t.Errorf("%s: Line = %d, want > 0 (1-based)", fn.Name, fn.Line)
		}
		if fn.EndLine < fn.Line {
			t.Errorf("%s: EndLine %d < Line %d", fn.Name, fn.EndLine, fn.Line)
		}
		if fn.Lines != fn.EndLine-fn.Line+1 {
			t.Errorf("%s: Lines = %d, want %d", fn.Name, fn.Lines, fn.EndLine-fn.Line+1)
		}
	}
}

func TestExtractFunctions_FilterToChangedRegion(t *testing.T) {
	absPath, _ := filepath.Abs("testdata/functions.ts")

	// Narrow region that only covers the first function (standalone)
	// — around lines 6-8 in the fixture.
	fc := diff.FileChange{
		Path:    "testdata/functions.ts",
		Regions: []diff.ChangedRegion{{StartLine: 6, EndLine: 8}},
	}
	fns, _, err := sizesImpl{}.ExtractFunctions(absPath, fc)
	if err != nil {
		t.Fatal(err)
	}
	foundStandalone := false
	for _, fn := range fns {
		if fn.Name == "standalone" {
			foundStandalone = true
		}
		if fn.Name == "Counter.reset" || fn.Name == "gen" {
			t.Errorf("unexpected function %q in narrow region", fn.Name)
		}
	}
	if !foundStandalone {
		t.Errorf("expected standalone in narrow region")
	}
}

func TestExtractFunctions_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.ts")
	if err := writeFile(empty, []byte("")); err != nil {
		t.Fatal(err)
	}
	fns, fsize, err := sizesImpl{}.ExtractFunctions(empty, fullRegion("empty.ts"))
	if err != nil {
		t.Fatalf("ExtractFunctions: %v", err)
	}
	if len(fns) != 0 {
		t.Errorf("empty file: got %d fns, want 0", len(fns))
	}
	if fsize == nil {
		t.Fatal("expected non-nil file size for empty file")
	}
	if fsize.Lines != 0 {
		t.Errorf("empty file: Lines = %d, want 0", fsize.Lines)
	}
}

// TestExtractFunctions_TSXGrammar exercises the .tsx grammar path. The
// fixture contains JSX that the plain typescript grammar would reject;
// a successful extraction here proves parse.go routes .tsx to the tsx
// grammar.
func TestExtractFunctions_TSXGrammar(t *testing.T) {
	absPath, err := filepath.Abs("testdata/component.tsx")
	if err != nil {
		t.Fatal(err)
	}
	fns, fsize, err := sizesImpl{}.ExtractFunctions(absPath, fullRegion("testdata/component.tsx"))
	if err != nil {
		t.Fatalf("ExtractFunctions on .tsx: %v", err)
	}
	if fsize == nil || fsize.Lines == 0 {
		t.Error("expected non-empty file size for .tsx fixture")
	}
	names := map[string]bool{}
	for _, fn := range fns {
		names[fn.Name] = true
	}
	for _, want := range []string{"Hello", "Count"} {
		if !names[want] {
			t.Errorf("missing %q in .tsx extraction, got %v", want, names)
		}
	}
}

func TestCountLines(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"x", 1},
		{"x\n", 1},
		{"x\ny", 2},
		{"x\ny\n", 2},
		{"\n", 1},
	}
	for _, tc := range cases {
		got := countLines([]byte(tc.in))
		if got != tc.want {
			t.Errorf("countLines(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
