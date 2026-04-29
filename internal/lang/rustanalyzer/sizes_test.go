package rustanalyzer

import (
	"math"
	"path/filepath"
	"sort"
	"testing"

	"github.com/0xPolygon/diffguard/internal/diff"
)

// fullRegion returns a FileChange covering every line so tests can assert
// against every function in the fixture without threading line numbers.
func fullRegion(path string) diff.FileChange {
	return diff.FileChange{
		Path:    path,
		Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: math.MaxInt32}},
	}
}

func TestExtractFunctions_AllForms(t *testing.T) {
	absPath, err := filepath.Abs("testdata/functions.rs")
	if err != nil {
		t.Fatal(err)
	}
	s := sizesImpl{}
	fns, fsize, err := s.ExtractFunctions(absPath, fullRegion("testdata/functions.rs"))
	if err != nil {
		t.Fatalf("ExtractFunctions: %v", err)
	}
	if fsize == nil {
		t.Fatal("expected non-nil file size")
	}
	if fsize.Lines == 0 {
		t.Error("file size reports zero lines")
	}

	// Collect names and assert the expected set appears. Tolerate order
	// by sorting; collectFunctions already sorts by (line, name) but
	// asserting on a set is more resilient to minor CST shape changes.
	names := make([]string, 0, len(fns))
	for _, fn := range fns {
		names = append(names, fn.Name)
	}
	sort.Strings(names)

	expected := map[string]bool{
		"standalone":           false,
		"Counter::new":         false,
		"Counter::increment":   false,
		"nested_helper":        false, // nested fns are separate entries
		"Named::name":          false, // default (trait-declared) method is not in this fixture
		"Counter::name":        false, // trait-impl methods attach to the impl type, not the trait
	}
	for _, name := range names {
		if _, ok := expected[name]; ok {
			expected[name] = true
		}
	}

	mustHave := []string{"standalone", "Counter::new", "Counter::increment", "nested_helper", "Counter::name"}
	for _, n := range mustHave {
		if !expected[n] {
			t.Errorf("missing expected function %q (got %v)", n, names)
		}
	}
}

func TestExtractFunctions_LineRanges(t *testing.T) {
	absPath, _ := filepath.Abs("testdata/functions.rs")
	fns, _, err := sizesImpl{}.ExtractFunctions(absPath, fullRegion("testdata/functions.rs"))
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
	absPath, _ := filepath.Abs("testdata/functions.rs")

	// Narrow region that only covers the standalone fn (lines 5-7 in the
	// fixture). The impl methods should be filtered out.
	fc := diff.FileChange{
		Path:    "testdata/functions.rs",
		Regions: []diff.ChangedRegion{{StartLine: 5, EndLine: 7}},
	}
	fns, _, err := sizesImpl{}.ExtractFunctions(absPath, fc)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, fn := range fns {
		names = append(names, fn.Name)
	}
	sort.Strings(names)

	// Must contain "standalone" and exclude the impl methods.
	foundStandalone := false
	for _, n := range names {
		if n == "standalone" {
			foundStandalone = true
		}
		if n == "Counter::new" || n == "Counter::name" {
			t.Errorf("unexpected function %q in narrow region, got %v", n, names)
		}
	}
	if !foundStandalone {
		t.Errorf("expected standalone in narrow region, got %v", names)
	}
}

func TestExtractFunctions_EmptyFile(t *testing.T) {
	// Tree-sitter tolerates an empty file and produces an empty source_file
	// node — we should return no functions and a 0-line file size.
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.rs")
	if err := writeFile(empty, []byte("")); err != nil {
		t.Fatal(err)
	}
	fns, fsize, err := sizesImpl{}.ExtractFunctions(empty, fullRegion("empty.rs"))
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
