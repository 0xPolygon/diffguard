package churn

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
	_ "github.com/0xPolygon/diffguard/internal/lang/goanalyzer"
	"github.com/0xPolygon/diffguard/internal/report"
)

func goScorer(t *testing.T) lang.ComplexityScorer {
	t.Helper()
	l, ok := lang.Get("go")
	if !ok {
		t.Fatal("go language not registered")
	}
	return l.ComplexityScorer()
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
	results := []FunctionChurn{
		{File: "a.go", Score: 5, Commits: 1, Complexity: 5},
	}
	findings, _ := collectChurnFindings(results, 10)
	if len(findings) != 1 {
		t.Errorf("findings = %d, want 1", len(findings))
	}
}

func TestCollectChurnFindings_BoundaryCondition(t *testing.T) {
	results := []FunctionChurn{
		{File: "a.go", Score: 60, Commits: 6, Complexity: 10},
	}
	_, warnCount := collectChurnFindings(results, 10)
	if warnCount != 0 {
		t.Errorf("warnCount = %d, want 0", warnCount)
	}

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

	results, err := analyzeFileChurn(dir, fc, 5, goScorer(t))
	if err != nil {
		t.Fatalf("analyzeFileChurn: %v", err)
	}
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

func TestCollectFileCommits(t *testing.T) {
	files := []diff.FileChange{
		{Path: "internal/churn/churn.go"},
	}
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

	results, err := collectChurnResults(dir, files, commits, goScorer(t))
	if err != nil {
		t.Fatalf("collectChurnResults: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Commits != 3 {
		t.Errorf("commits = %d, want 3", results[0].Commits)
	}
}

// erroringScorer returns a canned error so the error-handling branches in
// collectChurnResults and Analyze run.
type erroringScorer struct{ err error }

func (s erroringScorer) ScoreFile(absPath string, fc diff.FileChange) ([]lang.FunctionComplexity, error) {
	return nil, s.err
}

// TestCollectChurnResults_PropagatesError asserts the scorer error escapes
// the aggregation helper so the caller can react.
func TestCollectChurnResults_PropagatesError(t *testing.T) {
	files := []diff.FileChange{
		{Path: "nope.go", Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 1}}},
	}
	_, err := collectChurnResults(t.TempDir(), files, map[string]int{"nope.go": 0},
		erroringScorer{err: errTest})
	if err == nil {
		t.Fatal("expected scorer error to propagate")
	}
	if !containsAnalyzingPrefix(err.Error()) {
		t.Errorf("error %q should be wrapped with file context", err)
	}
}

// TestAnalyze_PropagatesScorerError pins that the top-level Analyze wraps
// scorer errors rather than swallowing them — covers the `if err != nil`
// branch in Analyze.
func TestAnalyze_PropagatesScorerError(t *testing.T) {
	d := &diff.Result{Files: []diff.FileChange{
		{Path: "x.go", Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 1}}},
	}}
	_, err := Analyze(t.TempDir(), d, 10, erroringScorer{err: errTest})
	if err == nil {
		t.Fatal("expected Analyze to return scorer error")
	}
}

// errTest is a sentinel error value the helpers above return.
var errTest = testErr("scorer boom")

type testErr string

func (e testErr) Error() string { return string(e) }

func containsAnalyzingPrefix(s string) bool {
	prefix := "analyzing "
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
