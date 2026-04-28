package goanalyzer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/0xPolygon/diffguard/internal/diff"
)

// fileChange is a small helper to avoid the verbose literal struct in every
// test. The single-region path covers all our cases — multi-region overlap
// is exercised by the diff package's own tests.
func fileChange(path string, startLine, endLine int) diff.FileChange {
	return diff.FileChange{
		Path:    path,
		Regions: []diff.ChangedRegion{{StartLine: startLine, EndLine: endLine}},
	}
}

// writePkg writes a single Go file into a temp directory and returns the
// directory plus the absolute path. Caller is responsible for nothing —
// t.TempDir() handles cleanup.
func writePkg(t *testing.T, filename, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeFile writes content to dir/filename and returns nothing — used to
// add a sibling file to a package after the initial setup.
func writeFile(t *testing.T, dir, filename, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestFindDeadCode_UnusedUnexportedFunction(t *testing.T) {
	dir := writePkg(t, "a.go", `package main

func unused() {}

func main() {
	_ = 1
}
`)
	got, err := deadcodeImpl{}.FindDeadCode(dir, fileChange("a.go", 1, 100))
	if err != nil {
		t.Fatalf("FindDeadCode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d unused symbols, want 1: %+v", len(got), got)
	}
	if got[0].Name != "unused" || got[0].Kind != "func" {
		t.Errorf("got %+v, want unused/func", got[0])
	}
}

func TestFindDeadCode_UsedFunction(t *testing.T) {
	dir := writePkg(t, "a.go", `package main

func used() {}

func main() {
	used()
}
`)
	got, _ := deadcodeImpl{}.FindDeadCode(dir, fileChange("a.go", 1, 100))
	if len(got) != 0 {
		t.Errorf("expected no unused symbols, got %+v", got)
	}
}

func TestFindDeadCode_ExportedSkipped(t *testing.T) {
	dir := writePkg(t, "a.go", `package p

// Exported but unused inside the package — we skip exported names because
// external packages might import them.
func PublicUnused() {}
`)
	got, _ := deadcodeImpl{}.FindDeadCode(dir, fileChange("a.go", 1, 100))
	if len(got) != 0 {
		t.Errorf("exported names should be skipped, got %+v", got)
	}
}

func TestFindDeadCode_MethodSkipped(t *testing.T) {
	dir := writePkg(t, "a.go", `package p

type t struct{}

func (t) unused() {}
`)
	got, _ := deadcodeImpl{}.FindDeadCode(dir, fileChange("a.go", 1, 100))
	if len(got) != 0 {
		t.Errorf("methods should be skipped (may satisfy interfaces), got %+v", got)
	}
}

func TestFindDeadCode_FrameworkSpecialFuncs(t *testing.T) {
	dir := writePkg(t, "a.go", `package main

func init() {}
func main() {}
`)
	got, _ := deadcodeImpl{}.FindDeadCode(dir, fileChange("a.go", 1, 100))
	if len(got) != 0 {
		t.Errorf("init/main in package main should be skipped, got %+v", got)
	}
}

func TestFindDeadCode_TestFunctionsSkipped(t *testing.T) {
	// Even though the candidate file is a regular .go file, a function named
	// TestFoo there is treated as a test entry point and skipped — the
	// detector's framework-special check is name-based, not path-based.
	dir := writePkg(t, "a.go", `package p

func TestFoo() {}
func BenchmarkBar() {}
func ExampleBaz() {}
func FuzzQux() {}
`)
	got, _ := deadcodeImpl{}.FindDeadCode(dir, fileChange("a.go", 1, 100))
	// Note: TestFoo etc. are exported anyway, so they'd be skipped on the
	// exported check before reaching the framework-special branch. This test
	// pins that behavior — exported test-shaped names stay quiet.
	if len(got) != 0 {
		t.Errorf("test-shaped exported names should be skipped, got %+v", got)
	}
}

func TestFindDeadCode_MainOutsideMainPackageNotSkipped(t *testing.T) {
	// `main` is special only in package main. In any other package it's an
	// ordinary unexported function and should be flagged when unused.
	dir := writePkg(t, "a.go", `package notmain

func main() {}
`)
	got, _ := deadcodeImpl{}.FindDeadCode(dir, fileChange("a.go", 1, 100))
	if len(got) != 1 || got[0].Name != "main" {
		t.Errorf("main in non-main package should be flagged, got %+v", got)
	}
}

func TestFindDeadCode_UsedFromOtherFileInPackage(t *testing.T) {
	dir := writePkg(t, "a.go", `package p

func helper() {}
`)
	writeFile(t, dir, "b.go", `package p

func consumer() {
	helper()
}
`)
	got, _ := deadcodeImpl{}.FindDeadCode(dir, fileChange("a.go", 1, 100))
	if len(got) != 0 {
		t.Errorf("helper used from sibling file should not be flagged, got %+v", got)
	}
}

func TestFindDeadCode_UsedOnlyFromTestFile(t *testing.T) {
	// Test files are part of the package for reference scanning purposes.
	// A function used only by tests is considered "used".
	dir := writePkg(t, "a.go", `package p

func helper() int { return 1 }
`)
	writeFile(t, dir, "a_test.go", `package p

import "testing"

func TestFoo(t *testing.T) {
	helper()
}
`)
	got, _ := deadcodeImpl{}.FindDeadCode(dir, fileChange("a.go", 1, 100))
	if len(got) != 0 {
		t.Errorf("helper used only from tests should not be flagged, got %+v", got)
	}
}

func TestFindDeadCode_UnusedVarsAndConsts(t *testing.T) {
	dir := writePkg(t, "a.go", `package p

var unusedVar = 1
const unusedConst = "x"
var (
	usedVar = 2
	noTouch = 3
)

func sink() int { return usedVar }
`)
	got, _ := deadcodeImpl{}.FindDeadCode(dir, fileChange("a.go", 1, 100))

	names := map[string]string{}
	for _, u := range got {
		names[u.Name] = u.Kind
	}
	if names["unusedVar"] != "var" {
		t.Errorf("unusedVar missing or wrong kind: %+v", got)
	}
	if names["unusedConst"] != "const" {
		t.Errorf("unusedConst missing or wrong kind: %+v", got)
	}
	if names["noTouch"] != "var" {
		t.Errorf("noTouch in multi-spec block missing: %+v", got)
	}
	if _, ok := names["usedVar"]; ok {
		t.Errorf("usedVar should not be flagged, got %+v", got)
	}
}

func TestFindDeadCode_OutsideChangedRegionIgnored(t *testing.T) {
	// `unused` is on line 3; if the diff only covers lines 100+ we should
	// not report it as a candidate.
	dir := writePkg(t, "a.go", `package p

func unused() {}
`)
	got, _ := deadcodeImpl{}.FindDeadCode(dir, fileChange("a.go", 100, 200))
	if len(got) != 0 {
		t.Errorf("symbol outside changed region should be skipped, got %+v", got)
	}
}

func TestFindDeadCode_UnderscoreIdentifierSkipped(t *testing.T) {
	// `var _ = something` is the canonical "compile-time use" pattern — we
	// should not flag the underscore identifier (it isn't a symbol).
	dir := writePkg(t, "a.go", `package p

import "fmt"

var _ = fmt.Println
`)
	got, _ := deadcodeImpl{}.FindDeadCode(dir, fileChange("a.go", 1, 100))
	if len(got) != 0 {
		t.Errorf("underscore identifier should not be flagged, got %+v", got)
	}
}

func TestFindDeadCode_ParseErrorReturnsNoResults(t *testing.T) {
	dir := writePkg(t, "a.go", `this is not valid go`)
	got, err := deadcodeImpl{}.FindDeadCode(dir, fileChange("a.go", 1, 100))
	if err != nil {
		t.Errorf("parse error should be swallowed, got err=%v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no results on parse error, got %+v", got)
	}
}

func TestIsExported(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"Foo", true},
		{"foo", false},
		{"_foo", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isExported(tt.name); got != tt.want {
			t.Errorf("isExported(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestIsFrameworkSpecialFunc(t *testing.T) {
	tests := []struct {
		name, pkg string
		want      bool
	}{
		{"init", "anything", true},
		{"main", "main", true},
		{"main", "other", false},
		{"Test", "p", true},
		{"TestFoo", "p", true},
		{"Testify", "p", false}, // lowercase rune after Test
		{"BenchmarkBar", "p", true},
		{"ExampleBaz", "p", true},
		{"FuzzQux", "p", true},
		{"Test1", "p", true}, // digit qualifies per `go test`
		{"plain", "p", false},
	}
	for _, tt := range tests {
		if got := isFrameworkSpecialFunc(tt.name, tt.pkg); got != tt.want {
			t.Errorf("isFrameworkSpecialFunc(%q, %q) = %v, want %v", tt.name, tt.pkg, got, tt.want)
		}
	}
}
