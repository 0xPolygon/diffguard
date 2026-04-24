package rustanalyzer

import (
	"path/filepath"
	"testing"
)

// TestScanAnnotations_NextLine writes a fixture with a mutator-disable-
// next-line comment and confirms the following source line is disabled.
func TestScanAnnotations_NextLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.rs")
	src := []byte(`fn f(x: i32) -> i32 {
    // mutator-disable-next-line
    if x > 0 { 1 } else { 0 }
}
`)
	if err := writeFile(path, src); err != nil {
		t.Fatal(err)
	}
	disabled, err := annotationScannerImpl{}.ScanAnnotations(path)
	if err != nil {
		t.Fatal(err)
	}
	// Line 3 (the `if` line) should be disabled.
	if !disabled[3] {
		t.Errorf("expected line 3 disabled, got %v", disabled)
	}
	if disabled[4] {
		t.Errorf("line 4 should not be disabled (unrelated), got %v", disabled)
	}
}

// TestScanAnnotations_FuncWide asserts that `mutator-disable-func`
// marks every line of the enclosing function — including the signature
// line.
func TestScanAnnotations_FuncWide(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.rs")
	src := []byte(`// mutator-disable-func
fn top(x: i32) -> i32 {
    x + 1
}

fn other(x: i32) -> i32 {
    x * 2
}
`)
	if err := writeFile(path, src); err != nil {
		t.Fatal(err)
	}
	disabled, err := annotationScannerImpl{}.ScanAnnotations(path)
	if err != nil {
		t.Fatal(err)
	}
	// The `top` function spans lines 2-4. All three must be disabled.
	for _, line := range []int{2, 3, 4} {
		if !disabled[line] {
			t.Errorf("expected line %d disabled in top, got %v", line, disabled)
		}
	}
	// The `other` function (lines 6-8) must not be touched.
	for _, line := range []int{6, 7, 8} {
		if disabled[line] {
			t.Errorf("line %d in other should not be disabled, got %v", line, disabled)
		}
	}
}

// TestScanAnnotations_UnrelatedComments is a negative control: ordinary
// comments must not toggle anything.
func TestScanAnnotations_UnrelatedComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.rs")
	src := []byte(`// just a regular comment
fn f(x: i32) -> i32 {
    // another regular comment
    x
}
`)
	if err := writeFile(path, src); err != nil {
		t.Fatal(err)
	}
	disabled, err := annotationScannerImpl{}.ScanAnnotations(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(disabled) != 0 {
		t.Errorf("expected empty disabled map, got %v", disabled)
	}
}

// TestScanAnnotations_FuncInsideComment is a coverage test for the case
// where the disable-func comment lives inside the function body rather
// than preceding it. The Go analyzer accepts both positions.
func TestScanAnnotations_FuncInsideComment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.rs")
	src := []byte(`fn only(x: i32) -> i32 {
    // mutator-disable-func
    x + 1
}
`)
	if err := writeFile(path, src); err != nil {
		t.Fatal(err)
	}
	disabled, err := annotationScannerImpl{}.ScanAnnotations(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []int{1, 2, 3, 4} {
		if !disabled[line] {
			t.Errorf("expected line %d disabled, got %v", line, disabled)
		}
	}
}
