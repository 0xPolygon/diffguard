package goanalyzer

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

func TestScanAnnotations_DisableNextLine(t *testing.T) {
	code := `package p

func f() {
	// mutator-disable-next-line
	if true {
	}
}
`
	dir := t.TempDir()
	fp := filepath.Join(dir, "t.go")
	if err := os.WriteFile(fp, []byte(code), 0644); err != nil {
		t.Fatal(err)
	}
	disabled, err := annotationScannerImpl{}.ScanAnnotations(fp)
	if err != nil {
		t.Fatal(err)
	}
	if !disabled[5] {
		t.Errorf("expected line 5 disabled, got %v", disabled)
	}
	if disabled[4] {
		t.Error("comment line should not be disabled")
	}
	if disabled[6] {
		t.Error("line 6 should not be disabled")
	}
}

func TestScanAnnotations_DisableFunc(t *testing.T) {
	code := `package p

// mutator-disable-func
func f() {
	if true {
	}
	x := 1
	_ = x
}

func g() {
	if true {
	}
}
`
	dir := t.TempDir()
	fp := filepath.Join(dir, "t.go")
	os.WriteFile(fp, []byte(code), 0644)

	disabled, err := annotationScannerImpl{}.ScanAnnotations(fp)
	if err != nil {
		t.Fatal(err)
	}

	for i := 4; i <= 9; i++ {
		if !disabled[i] {
			t.Errorf("expected line %d disabled (inside f)", i)
		}
	}
	if disabled[12] {
		t.Error("g()'s line 12 should not be disabled")
	}
}

func TestScanAnnotations_NoAnnotations(t *testing.T) {
	code := `package p

func f() {
	if true {}
}
`
	dir := t.TempDir()
	fp := filepath.Join(dir, "t.go")
	os.WriteFile(fp, []byte(code), 0644)
	disabled, err := annotationScannerImpl{}.ScanAnnotations(fp)
	if err != nil {
		t.Fatal(err)
	}
	if len(disabled) != 0 {
		t.Errorf("expected empty disabled map, got %v", disabled)
	}
}

func TestScanAnnotations_IrrelevantComment(t *testing.T) {
	code := `package p

// this is just a regular comment
func f() {
	if true {}
}
`
	dir := t.TempDir()
	fp := filepath.Join(dir, "t.go")
	os.WriteFile(fp, []byte(code), 0644)
	disabled, err := annotationScannerImpl{}.ScanAnnotations(fp)
	if err != nil {
		t.Fatal(err)
	}
	if len(disabled) != 0 {
		t.Errorf("regular comments should not disable mutations, got %v", disabled)
	}
}

// TestFuncRanges_IncludesSignatureAndBody ensures funcRanges spans the
// whole FuncDecl (signature + body), since that's what mutator-disable-func
// should cover.
func TestFuncRanges_IncludesSignatureAndBody(t *testing.T) {
	code := `package p
func f() {
	if true {}
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "t.go", code, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	ranges := funcRanges(fset, f)
	if len(ranges) != 1 {
		t.Fatalf("expected 1 range, got %d", len(ranges))
	}
	if ranges[0].start != 2 {
		t.Errorf("start = %d, want 2", ranges[0].start)
	}
	if ranges[0].end < ranges[0].start {
		t.Errorf("end=%d < start=%d", ranges[0].end, ranges[0].start)
	}
}
