package rustanalyzer

import (
	"path/filepath"
	"testing"
)

// TestSimpleTypeName_Shapes exercises each of the type-expression shapes
// that simpleTypeNameFromShape dispatches on. Each subtest asserts the
// extractor returns the trailing identifier for an impl-type node of
// that shape.
func TestSimpleTypeName_Shapes(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantFn  string
	}{
		{
			name:   "plain type_identifier",
			src:    "struct Foo; impl Foo { fn a(&self) {} }\n",
			wantFn: "Foo::a",
		},
		{
			name:   "generic_type wraps type_identifier",
			src:    "struct Bag<T>(T); impl<T> Bag<T> { fn a(&self) {} }\n",
			wantFn: "Bag::a",
		},
		{
			name:   "reference_type wraps type_identifier",
			src:    "struct Zap; impl Zap { fn a(self: &Self) {} }\n",
			wantFn: "Zap::a",
		},
		{
			name:   "scoped_type_identifier uses trailing name",
			src:    "mod m { pub struct Inner; } impl m::Inner { fn a(&self) {} }\n",
			wantFn: "Inner::a",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "a.rs")
			if err := writeFile(path, []byte(tc.src)); err != nil {
				t.Fatal(err)
			}
			fns, _, err := sizesImpl{}.ExtractFunctions(path, fullRegion("a.rs"))
			if err != nil {
				t.Fatal(err)
			}
			found := false
			names := make([]string, 0, len(fns))
			for _, fn := range fns {
				names = append(names, fn.Name)
				if fn.Name == tc.wantFn {
					found = true
				}
			}
			if !found {
				t.Errorf("want function named %q, got %v", tc.wantFn, names)
			}
		})
	}
}

// TestAnalyzeFile_SortedByLineThenName asserts the complexity result
// ordering: primary key is line ascending, secondary key is name
// ascending. Mutating the comparator's `!=` or `<` would flip one of
// these invariants.
func TestAnalyzeFile_SortedByLineThenName(t *testing.T) {
	// Fixture: two functions on distinct lines (line ordering), plus a
	// nested function that shares a line with its parent (exercises the
	// name-tiebreak branch).
	src := []byte(`fn zulu() -> i32 {
    fn alpha() -> i32 { 0 }
    alpha()
}

fn alpha_top() -> i32 {
    1
}
`)
	dir := t.TempDir()
	path := filepath.Join(dir, "a.rs")
	if err := writeFile(path, src); err != nil {
		t.Fatal(err)
	}
	results, err := complexityImpl{}.AnalyzeFile(path, fullRegion("a.rs"))
	if err != nil {
		t.Fatal(err)
	}

	// Primary ordering: line ascending. Fail if a later result has a
	// smaller Line than an earlier one.
	for i := 1; i < len(results); i++ {
		if results[i].Line < results[i-1].Line {
			t.Fatalf("not sorted by line: %+v then %+v", results[i-1], results[i])
		}
	}

	// Secondary ordering: for any pair with the same Line, name must
	// be ascending. `alpha` (nested, line 1) must appear before any
	// other same-line result if more than one shares its line.
	for i := 1; i < len(results); i++ {
		if results[i].Line == results[i-1].Line && results[i].Name < results[i-1].Name {
			t.Fatalf("same-line tie broken incorrectly: %+v then %+v", results[i-1], results[i])
		}
	}

	// Top-level: confirm `alpha_top` (line 6) appears AFTER `zulu`
	// (line 1), catching a `!=` flip that would skip the line check.
	idxZulu, idxAlphaTop := -1, -1
	for i, r := range results {
		switch r.Name {
		case "zulu":
			idxZulu = i
		case "alpha_top":
			idxAlphaTop = i
		}
	}
	if idxZulu < 0 || idxAlphaTop < 0 {
		t.Fatalf("missing expected functions: zulu=%d alpha_top=%d in %+v", idxZulu, idxAlphaTop, results)
	}
	if idxAlphaTop < idxZulu {
		t.Errorf("alpha_top (line 6) must sort after zulu (line 1); got idx %d < %d", idxAlphaTop, idxZulu)
	}
}

// TestOpClassifiers asserts each mutant-site classifier returns true
// for an in-class token and false for an out-of-class token. Each
// case directly exercises one of the `==` / `!=` chains that the
// mutation generator uses to pick the right operator family; flipping
// any single equality there would be caught here.
func TestOpClassifiers(t *testing.T) {
	type row struct {
		name string
		fn   func(string) bool
		yes  []string
		no   []string
	}
	cases := []row{
		{"isBoundary", isBoundary, []string{">", ">=", "<", "<="}, []string{"==", "!=", "+", "&&"}},
		{"isComparison", isComparison, []string{"==", "!="}, []string{">", "<", "+", "&&"}},
		{"isMath", isMath, []string{"+", "-", "*", "/"}, []string{">", "==", "&&", "!"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, op := range tc.yes {
				if !tc.fn(op) {
					t.Errorf("%s(%q) = false, want true", tc.name, op)
				}
			}
			for _, op := range tc.no {
				if tc.fn(op) {
					t.Errorf("%s(%q) = true, want false", tc.name, op)
				}
			}
		})
	}
}

// TestNewTestRunner_DefaultsToCargo pins the default command — if a
// return-value mutation replaces the struct with zero, cmd becomes ""
// and this test breaks.
func TestNewTestRunner_DefaultsToCargo(t *testing.T) {
	r := newTestRunner()
	if r == nil {
		t.Fatal("newTestRunner returned nil")
	}
	if r.cmd != "cargo" {
		t.Errorf("cmd = %q, want %q", r.cmd, "cargo")
	}
}

// TestScanAnnotations_FuncWide_LastLineComment places the
// mutator-disable-func comment on the function's closing-brace line.
// That exercises the `commentLine <= r.end` boundary in
// isCommentForFunc, which a `<= -> <` mutation would break.
func TestScanAnnotations_FuncWide_LastLineComment(t *testing.T) {
	// Function spans lines 1-3. The disable-func comment sits on line
	// 3 (same as the closing brace), which is exactly r.end.
	src := []byte(`fn last(x: i32) -> i32 {
    x
} // mutator-disable-func
`)
	dir := t.TempDir()
	path := filepath.Join(dir, "a.rs")
	if err := writeFile(path, src); err != nil {
		t.Fatal(err)
	}
	disabled, err := annotationScannerImpl{}.ScanAnnotations(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []int{1, 2, 3} {
		if !disabled[line] {
			t.Errorf("expected line %d disabled (boundary at r.end), got %v", line, disabled)
		}
	}
}
