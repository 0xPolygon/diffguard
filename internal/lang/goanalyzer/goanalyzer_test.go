package goanalyzer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/0xPolygon/diffguard/internal/diff"
)

// TestLanguage_Name pins the registered name. Other packages (CLI
// suffixing, tiers.go) key on this string.
func TestLanguage_Name(t *testing.T) {
	l := &Language{}
	if l.Name() != "go" {
		t.Errorf("Name() = %q, want go", l.Name())
	}
}

func TestLanguage_FileFilter(t *testing.T) {
	f := (&Language{}).FileFilter()
	if len(f.Extensions) != 1 || f.Extensions[0] != ".go" {
		t.Errorf("Extensions = %v, want [.go]", f.Extensions)
	}
	if !f.IsTestFile("foo_test.go") {
		t.Error("IsTestFile(foo_test.go) = false, want true")
	}
	if f.IsTestFile("foo.go") {
		t.Error("IsTestFile(foo.go) = true, want false")
	}
	if len(f.DiffGlobs) != 1 || f.DiffGlobs[0] != "*.go" {
		t.Errorf("DiffGlobs = %v, want [*.go]", f.DiffGlobs)
	}
}

// TestFuncName covers all three canonical forms: free function, value
// receiver method, pointer receiver method. funcName used to live in three
// places pre-split; this test is the canary that the consolidation didn't
// drop a case.
func TestFuncName(t *testing.T) {
	tests := []struct {
		code     string
		expected string
	}{
		{`package p; func Foo() {}`, "Foo"},
		{`package p; type T struct{}; func (t T) Bar() {}`, "(T).Bar"},
		{`package p; type T struct{}; func (t *T) Baz() {}`, "(T).Baz"},
	}
	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			dir := t.TempDir()
			fp := filepath.Join(dir, "test.go")
			if err := os.WriteFile(fp, []byte(tt.code), 0644); err != nil {
				t.Fatal(err)
			}
			fc := diff.FileChange{
				Path:    "test.go",
				Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 100}},
			}
			results, _ := complexityImpl{}.AnalyzeFile(fp, fc)
			if len(results) == 0 {
				t.Fatal("no results")
			}
			if results[0].Name != tt.expected {
				t.Errorf("Name = %q, want %q", results[0].Name, tt.expected)
			}
		})
	}
}

func TestExtractFunctions_SharesShape(t *testing.T) {
	code := `package p

func f() {
	x := 1
	_ = x
}
`
	dir := t.TempDir()
	fp := filepath.Join(dir, "f.go")
	os.WriteFile(fp, []byte(code), 0644)

	fc := diff.FileChange{
		Path:    "f.go",
		Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 100}},
	}
	fns, fsz, err := sizesImpl{}.ExtractFunctions(fp, fc)
	if err != nil {
		t.Fatalf("ExtractFunctions: %v", err)
	}
	if len(fns) != 1 {
		t.Fatalf("len(fns) = %d, want 1", len(fns))
	}
	if fns[0].Name != "f" {
		t.Errorf("Name = %q, want f", fns[0].Name)
	}
	if fsz == nil || fsz.Lines == 0 {
		t.Error("expected non-nil fsz with non-zero Lines")
	}
}

// TestScorer_SimpleCounter locks in the ScoreFile behavior: it's the
// simpler "bump by 1 per branch" counter, not the full cognitive walker.
// Two nested if statements score 2 (not 3 — no nesting penalty).
func TestScorer_SimpleCounter(t *testing.T) {
	code := `package p
func f(x int) {
	if x > 0 {
		if x > 1 {}
	}
}
`
	dir := t.TempDir()
	fp := filepath.Join(dir, "f.go")
	os.WriteFile(fp, []byte(code), 0644)
	fc := diff.FileChange{
		Path:    "f.go",
		Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 100}},
	}

	score, _ := complexityImpl{}.ScoreFile(fp, fc)
	if len(score) != 1 {
		t.Fatalf("len(score) = %d, want 1", len(score))
	}
	if score[0].Complexity != 2 {
		t.Errorf("score = %d, want 2 (+1 per if, no nesting)", score[0].Complexity)
	}

	// The full calculator gives the same code a higher score due to nesting.
	analyze, _ := complexityImpl{}.AnalyzeFile(fp, fc)
	if analyze[0].Complexity != 3 {
		t.Errorf("AnalyzeFile = %d, want 3 (cognitive with nesting)", analyze[0].Complexity)
	}
}

func TestDetectModulePath(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/foo\n\ngo 1.21\n"), 0644)
	mod, err := depsImpl{}.DetectModulePath(dir)
	if err != nil {
		t.Fatalf("DetectModulePath: %v", err)
	}
	if mod != "example.com/foo" {
		t.Errorf("mod = %q, want example.com/foo", mod)
	}
}

func TestDetectModulePath_Missing(t *testing.T) {
	dir := t.TempDir()
	_, err := depsImpl{}.DetectModulePath(dir)
	if err == nil {
		t.Error("expected error when go.mod is missing")
	}
}

// TestLanguage_Accessors_ReturnWorkingImpls pins each accessor to the real
// impl by exercising its primary entry point. This catches `return_value`
// mutations that zero out the return without the tests noticing.
func TestLanguage_Accessors_ReturnWorkingImpls(t *testing.T) {
	l := &Language{}

	dir := t.TempDir()
	fp := filepath.Join(dir, "f.go")
	if err := os.WriteFile(fp, []byte("package p\nfunc f(x int) int { if x > 0 { return x }; return -x }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	fc := diff.FileChange{Path: "f.go", Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 100}}}

	cc := l.ComplexityCalculator()
	if cc == nil {
		t.Fatal("ComplexityCalculator returned nil")
	}
	res, err := cc.AnalyzeFile(fp, fc)
	if err != nil {
		t.Fatalf("AnalyzeFile: %v", err)
	}
	if len(res) == 0 {
		t.Error("ComplexityCalculator produced no results")
	}

	cs := l.ComplexityScorer()
	if cs == nil {
		t.Fatal("ComplexityScorer returned nil")
	}
	scored, err := cs.ScoreFile(fp, fc)
	if err != nil {
		t.Fatalf("ScoreFile: %v", err)
	}
	if len(scored) == 0 {
		t.Error("ComplexityScorer produced no results")
	}

	fe := l.FunctionExtractor()
	if fe == nil {
		t.Fatal("FunctionExtractor returned nil")
	}
	fns, fsize, err := fe.ExtractFunctions(fp, fc)
	if err != nil {
		t.Fatalf("ExtractFunctions: %v", err)
	}
	if len(fns) == 0 || fsize == nil {
		t.Error("FunctionExtractor produced no output")
	}

	ir := l.ImportResolver()
	if ir == nil {
		t.Fatal("ImportResolver returned nil")
	}

	mg := l.MutantGenerator()
	if mg == nil {
		t.Fatal("MutantGenerator returned nil")
	}
	if _, err := mg.GenerateMutants(fp, fc, nil); err != nil {
		t.Fatalf("GenerateMutants: %v", err)
	}

	ma := l.MutantApplier()
	if ma == nil {
		t.Fatal("MutantApplier returned nil")
	}

	as := l.AnnotationScanner()
	if as == nil {
		t.Fatal("AnnotationScanner returned nil")
	}
	if _, err := as.ScanAnnotations(fp); err != nil {
		t.Fatalf("ScanAnnotations: %v", err)
	}

	tr := l.TestRunner()
	if tr == nil {
		t.Fatal("TestRunner returned nil")
	}
}

// TestScanPackageImports_ParsesAndReportsEdges exercises the happy path and
// verifies external imports and _test packages are filtered out.
func TestScanPackageImports_ParsesAndReportsEdges(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	code := `package pkg

import (
	"fmt"
	"example.com/mod/other"
)

var _ = fmt.Println
var _ = other.X
`
	if err := os.WriteFile(filepath.Join(pkgDir, "a.go"), []byte(code), 0644); err != nil {
		t.Fatal(err)
	}
	edges := depsImpl{}.ScanPackageImports(dir, "pkg", "example.com/mod")
	if edges == nil {
		t.Fatal("expected non-nil edges for valid package")
	}
	deps := edges["example.com/mod/pkg"]
	if deps == nil {
		t.Fatalf("expected edges for example.com/mod/pkg, got %+v", edges)
	}
	if !deps["example.com/mod/other"] {
		t.Errorf("expected internal edge to example.com/mod/other, got %+v", deps)
	}
	if deps["fmt"] {
		t.Errorf("external import fmt should be excluded, got %+v", deps)
	}
}

// TestScanPackageImports_ParseError returns nil when the directory contains
// unparseable Go. Exercises the `if err != nil { return nil }` branch.
func TestScanPackageImports_ParseError(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Malformed Go — parser will fail on ParseDir.
	if err := os.WriteFile(filepath.Join(pkgDir, "a.go"), []byte("this is not go"), 0644); err != nil {
		t.Fatal(err)
	}
	edges := depsImpl{}.ScanPackageImports(dir, "pkg", "example.com/mod")
	if edges != nil {
		t.Errorf("expected nil for parse error, got %+v", edges)
	}
}

// TestScanPackageImports_SkipsTestPackages verifies _test packages don't
// contribute edges.
func TestScanPackageImports_SkipsTestPackages(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Put only a _test package; ensure no edges are produced.
	code := `package pkg_test

import "example.com/mod/other"

var _ = other.X
`
	if err := os.WriteFile(filepath.Join(pkgDir, "a_test.go"), []byte(code), 0644); err != nil {
		t.Fatal(err)
	}
	edges := depsImpl{}.ScanPackageImports(dir, "pkg", "example.com/mod")
	if len(edges) != 0 {
		t.Errorf("expected no edges for _test package, got %+v", edges)
	}
}

// TestIsGoTestFile covers both branches of the suffix check.
func TestIsGoTestFile(t *testing.T) {
	if !isGoTestFile("x_test.go") {
		t.Error("x_test.go should be a test file")
	}
	if isGoTestFile("x.go") {
		t.Error("x.go should not be a test file")
	}
	if isGoTestFile("test.go") {
		t.Error("test.go should not be a test file (no _ prefix)")
	}
}

// TestHasSuffix covers both branches of the suffix helper.
func TestHasSuffix(t *testing.T) {
	if !hasSuffix("abc", "bc") {
		t.Error("abc should have suffix bc")
	}
	if hasSuffix("ab", "abc") {
		t.Error("short string cannot have a longer suffix")
	}
	if hasSuffix("abc", "ac") {
		t.Error("abc should not have suffix ac")
	}
}
