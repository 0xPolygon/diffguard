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

func TestComplexityAndScorer_Agree(t *testing.T) {
	// ComplexityScorer.ScoreFile currently delegates to AnalyzeFile, so the
	// per-function scores must match exactly. This is the invariant the
	// churn analyzer relies on.
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

	analyze, _ := complexityImpl{}.AnalyzeFile(fp, fc)
	score, _ := complexityImpl{}.ScoreFile(fp, fc)
	if len(analyze) != len(score) {
		t.Fatalf("len mismatch: %d vs %d", len(analyze), len(score))
	}
	for i := range analyze {
		if analyze[i].Complexity != score[i].Complexity {
			t.Errorf("[%d] complexity mismatch: %d vs %d", i, analyze[i].Complexity, score[i].Complexity)
		}
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
