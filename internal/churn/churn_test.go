package churn

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/report"
)

func TestComputeComplexity(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		expected int
	}{
		{"empty", `package p; func f() {}`, 0},
		{"single if", `package p; func f(x int) { if x > 0 {} }`, 1},
		{"for loop", `package p; func f() { for i := 0; i < 10; i++ {} }`, 1},
		{"switch", `package p; func f(x int) { switch x { case 1: } }`, 1},
		{"range", `package p; func f(s []int) { for range s {} }`, 1},
		{"select", `package p; func f(c chan int) { select { case <-c: } }`, 1},
		{"type switch", `package p; func f(x any) { switch x.(type) { case int: } }`, 1},
		{"logical and", `package p; func f(a, b bool) { if a && b {} }`, 2},
		{"logical or", `package p; func f(a, b bool) { if a || b {} }`, 2},
		{"nested", `package p; func f(x int) { if x > 0 { for x > 0 {} } }`, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "test.go", tt.code, 0)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}

			var fn *ast.FuncDecl
			for _, decl := range f.Decls {
				if fd, ok := decl.(*ast.FuncDecl); ok {
					fn = fd
					break
				}
			}

			got := computeComplexity(fn.Body)
			if got != tt.expected {
				t.Errorf("computeComplexity = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestComputeComplexity_NilBody(t *testing.T) {
	if got := computeComplexity(nil); got != 0 {
		t.Errorf("computeComplexity(nil) = %d, want 0", got)
	}
}

func TestCollectChurnFindings(t *testing.T) {
	results := []FunctionChurn{
		{File: "a.go", Line: 1, Name: "hot", Commits: 10, Complexity: 15, Score: 150},
		{File: "b.go", Line: 1, Name: "warm", Commits: 3, Complexity: 12, Score: 36},
		{File: "c.go", Line: 1, Name: "cold", Commits: 1, Complexity: 2, Score: 2},
		{File: "d.go", Line: 1, Name: "zero", Commits: 0, Complexity: 0, Score: 0},
	}

	findings, warnCount := collectChurnFindings(results, 10)

	// "hot" should warn (complexity>10 && commits>5)
	if warnCount != 1 {
		t.Errorf("warnCount = %d, want 1", warnCount)
	}
	// zero-score entries are skipped
	if len(findings) != 3 {
		t.Errorf("findings = %d, want 3", len(findings))
	}
}

func TestCollectChurnFindings_LimitExceeds(t *testing.T) {
	// Fewer results than limit of 10
	results := []FunctionChurn{
		{File: "a.go", Score: 5, Commits: 1, Complexity: 5},
	}
	findings, _ := collectChurnFindings(results, 10)
	if len(findings) != 1 {
		t.Errorf("findings = %d, want 1", len(findings))
	}
}

func TestCollectChurnFindings_BoundaryCondition(t *testing.T) {
	// Exactly at threshold — should NOT warn
	results := []FunctionChurn{
		{File: "a.go", Score: 60, Commits: 6, Complexity: 10},
	}
	_, warnCount := collectChurnFindings(results, 10)
	if warnCount != 0 {
		t.Errorf("warnCount = %d, want 0 (complexity at threshold, not over)", warnCount)
	}

	// Over threshold and commits > 5 — should warn
	results2 := []FunctionChurn{
		{File: "a.go", Score: 66, Commits: 6, Complexity: 11},
	}
	_, warnCount2 := collectChurnFindings(results2, 10)
	if warnCount2 != 1 {
		t.Errorf("warnCount = %d, want 1", warnCount2)
	}
}

func TestBuildSection_Empty(t *testing.T) {
	s := buildSection(nil, 10)
	if s.Severity != report.SeverityPass {
		t.Errorf("severity = %v, want PASS", s.Severity)
	}
	if s.Name != "Churn-Weighted Complexity" {
		t.Errorf("name = %q", s.Name)
	}
}

func TestBuildSection_WithWarnings(t *testing.T) {
	results := []FunctionChurn{
		{File: "a.go", Line: 1, Name: "hot", Commits: 10, Complexity: 15, Score: 150},
		{File: "b.go", Line: 1, Name: "ok", Commits: 1, Complexity: 2, Score: 2},
	}

	s := buildSection(results, 10)
	if s.Severity != report.SeverityWarn {
		t.Errorf("severity = %v, want WARN", s.Severity)
	}
}

func TestBuildSection_NoWarnings(t *testing.T) {
	results := []FunctionChurn{
		{File: "a.go", Line: 1, Name: "ok", Commits: 1, Complexity: 2, Score: 2},
	}

	s := buildSection(results, 10)
	if s.Severity != report.SeverityPass {
		t.Errorf("severity = %v, want PASS", s.Severity)
	}
}

func TestBuildSection_SortedByScore(t *testing.T) {
	results := []FunctionChurn{
		{File: "a.go", Score: 10, Commits: 1, Complexity: 10},
		{File: "b.go", Score: 50, Commits: 5, Complexity: 10},
		{File: "c.go", Score: 30, Commits: 3, Complexity: 10},
	}

	s := buildSection(results, 10)
	if len(s.Findings) < 3 {
		t.Fatalf("expected 3 findings, got %d", len(s.Findings))
	}
	if s.Findings[0].Value != 50 {
		t.Errorf("first finding score = %v, want 50", s.Findings[0].Value)
	}
}

func TestFormatTopScore(t *testing.T) {
	if got := formatTopScore(nil); got != "N/A" {
		t.Errorf("formatTopScore(nil) = %q, want N/A", got)
	}

	results := []FunctionChurn{{Score: 42.0}}
	if got := formatTopScore(results); got != "42" {
		t.Errorf("formatTopScore = %q, want 42", got)
	}
}

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
		fset := token.NewFileSet()
		f, _ := parser.ParseFile(fset, "test.go", tt.code, 0)
		for _, decl := range f.Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok {
				if got := funcName(fd); got != tt.expected {
					t.Errorf("funcName = %q, want %q", got, tt.expected)
				}
			}
		}
	}
}

func TestAnalyzeFileChurn(t *testing.T) {
	code := `package test

func simple() {
	x := 1
	_ = x
}

func complex_fn(a, b int) int {
	if a > b {
		return a
	}
	return b
}
`
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	os.WriteFile(fp, []byte(code), 0644)

	fc := diff.FileChange{
		Path:    "test.go",
		Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 100}},
	}

	results := analyzeFileChurn(dir, fc, 5)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Check that scores are computed correctly: commits * complexity
	for _, r := range results {
		expected := float64(r.Commits) * float64(r.Complexity)
		if r.Score != expected {
			t.Errorf("%s: score = %v, want %v", r.Name, r.Score, expected)
		}
	}
}

func TestAnalyzeFileChurn_ParseError(t *testing.T) {
	dir := t.TempDir()
	fc := diff.FileChange{
		Path:    "nonexistent.go",
		Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 10}},
	}

	results := analyzeFileChurn(dir, fc, 0)
	if results != nil {
		t.Error("expected nil for parse error")
	}
}

func TestCollectFileCommits(t *testing.T) {
	// Use the actual repo to test
	files := []diff.FileChange{
		{Path: "internal/churn/churn.go"},
	}
	// This will either work or return 0, both are valid
	commits := collectFileCommits("../..", files)
	if commits == nil {
		t.Error("expected non-nil map")
	}
}

func TestCollectChurnResults(t *testing.T) {
	code := `package test

func f() {}
`
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	os.WriteFile(fp, []byte(code), 0644)

	files := []diff.FileChange{
		{Path: "test.go", Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 10}}},
	}
	commits := map[string]int{"test.go": 3}

	results := collectChurnResults(dir, files, commits)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Commits != 3 {
		t.Errorf("commits = %d, want 3", results[0].Commits)
	}
}
