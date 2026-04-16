package tsanalyzer

import (
	"path/filepath"
	"testing"
)

// TestScanAnnotations_NextLine writes a fixture with a mutator-disable-
// next-line comment and confirms the following source line is disabled.
func TestScanAnnotations_NextLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.ts")
	src := []byte(`function f(x: number): number {
    // mutator-disable-next-line
    if (x > 0) { return 1; } else { return 0; }
}
`)
	if err := writeFile(path, src); err != nil {
		t.Fatal(err)
	}
	disabled, err := annotationScannerImpl{}.ScanAnnotations(path)
	if err != nil {
		t.Fatal(err)
	}
	if !disabled[3] {
		t.Errorf("expected line 3 disabled, got %v", disabled)
	}
	if disabled[4] {
		t.Errorf("line 4 should not be disabled (unrelated), got %v", disabled)
	}
}

// TestScanAnnotations_FuncWide asserts `mutator-disable-func` marks every
// line of the enclosing function — including the signature line.
func TestScanAnnotations_FuncWide(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.ts")
	src := []byte(`// mutator-disable-func
function top(x: number): number {
    return x + 1;
}

function other(x: number): number {
    return x * 2;
}
`)
	if err := writeFile(path, src); err != nil {
		t.Fatal(err)
	}
	disabled, err := annotationScannerImpl{}.ScanAnnotations(path)
	if err != nil {
		t.Fatal(err)
	}
	// top spans lines 2-4.
	for _, line := range []int{2, 3, 4} {
		if !disabled[line] {
			t.Errorf("expected line %d disabled in top, got %v", line, disabled)
		}
	}
	// other (lines 6-8) must not be touched.
	for _, line := range []int{6, 7, 8} {
		if disabled[line] {
			t.Errorf("line %d in other should not be disabled, got %v", line, disabled)
		}
	}
}

// TestScanAnnotations_UnrelatedComments: ordinary comments must not
// toggle anything.
func TestScanAnnotations_UnrelatedComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.ts")
	src := []byte(`// just a regular comment
function f(x: number): number {
    // another regular comment
    return x;
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

// TestScanAnnotations_FuncInsideComment: comment INSIDE the function body
// still applies to the enclosing function.
func TestScanAnnotations_FuncInsideComment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.ts")
	src := []byte(`function only(x: number): number {
    // mutator-disable-func
    return x + 1;
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
