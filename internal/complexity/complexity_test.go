package complexity

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
	_ "github.com/0xPolygon/diffguard/internal/lang/goanalyzer"
	"github.com/0xPolygon/diffguard/internal/report"
)

// goCalc returns the registered Go ComplexityCalculator. The goanalyzer
// package is blank-imported above so its init() has run by the time this
// helper is called.
func goCalc(t *testing.T) lang.ComplexityCalculator {
	t.Helper()
	l, ok := lang.Get("go")
	if !ok {
		t.Fatal("go language not registered")
	}
	return l.ComplexityCalculator()
}

// TestAnalyze_WithGoCalc is the integration-shape replacement for the old
// tree of "exercise the AST walker directly" tests that lived here before
// the complexity AST logic moved into goanalyzer. The walker tests now live
// next to the walker in goanalyzer/complexity_walker_test.go; this test
// locks in the orchestration: calculator is consulted, findings are
// aggregated, summary severity and stats shape are correct.
func TestAnalyze_WithGoCalc(t *testing.T) {
	code := `package test

func simple() {}

func complex_fn(x int) {
	if x > 0 {
		if x > 10 {
			if x > 100 {
				if x > 1000 {
					if x > 10000 {
						if x > 100000 {
							_ = x
						}
					}
				}
			}
		}
	}
}
`
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	if err := os.WriteFile(fp, []byte(code), 0644); err != nil {
		t.Fatal(err)
	}

	d := &diff.Result{
		Files: []diff.FileChange{
			{Path: "test.go", Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 100}}},
		},
	}

	section, err := Analyze(dir, d, 10, goCalc(t))
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	// complex_fn has 6 nested ifs — cognitive score > 10 triggers FAIL.
	if section.Severity != report.SeverityFail {
		t.Errorf("severity = %v, want FAIL", section.Severity)
	}
	if len(section.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(section.Findings))
	}
	if section.Findings[0].Function != "complex_fn" {
		t.Errorf("finding function = %q, want complex_fn", section.Findings[0].Function)
	}
}

func TestAnalyze_EmptyResult(t *testing.T) {
	d := &diff.Result{} // no files
	section, err := Analyze(t.TempDir(), d, 10, goCalc(t))
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if section.Severity != report.SeverityPass {
		t.Errorf("severity = %v, want PASS", section.Severity)
	}
	if section.Name != "Cognitive Complexity" {
		t.Errorf("name = %q", section.Name)
	}
}

func TestBuildSection_StatsValues(t *testing.T) {
	results := []lang.FunctionComplexity{
		{FunctionInfo: lang.FunctionInfo{File: "a.go", Line: 1, Name: "f1"}, Complexity: 4},
		{FunctionInfo: lang.FunctionInfo{File: "b.go", Line: 1, Name: "f2"}, Complexity: 8},
		{FunctionInfo: lang.FunctionInfo{File: "c.go", Line: 1, Name: "f3"}, Complexity: 12},
	}

	s := buildSection(results, 10)

	stats := s.Stats.(map[string]any)
	if stats["total_functions"] != 3 {
		t.Errorf("total_functions = %v, want 3", stats["total_functions"])
	}
	if stats["violations"] != 1 {
		t.Errorf("violations = %v, want 1", stats["violations"])
	}
	if stats["mean"] != 8.0 {
		t.Errorf("mean = %v, want 8.0", stats["mean"])
	}
	if stats["median"] != 8.0 {
		t.Errorf("median = %v, want 8.0", stats["median"])
	}
	if stats["max"] != 12.0 {
		t.Errorf("max = %v, want 12.0", stats["max"])
	}
}

func TestBuildSection_Empty(t *testing.T) {
	s := buildSection(nil, 10)
	if s.Severity != report.SeverityPass {
		t.Errorf("severity = %v, want PASS", s.Severity)
	}
}

func TestBuildSection_WithViolations(t *testing.T) {
	results := []lang.FunctionComplexity{
		{FunctionInfo: lang.FunctionInfo{File: "a.go", Line: 1, Name: "complex"}, Complexity: 20},
		{FunctionInfo: lang.FunctionInfo{File: "b.go", Line: 1, Name: "simple"}, Complexity: 3},
	}

	s := buildSection(results, 10)
	if s.Severity != report.SeverityFail {
		t.Errorf("severity = %v, want FAIL", s.Severity)
	}
	if len(s.Findings) != 1 {
		t.Errorf("findings = %d, want 1", len(s.Findings))
	}
}

func TestCollectComplexityFindings(t *testing.T) {
	results := []lang.FunctionComplexity{
		{FunctionInfo: lang.FunctionInfo{File: "a.go", Line: 1, Name: "low"}, Complexity: 5},
		{FunctionInfo: lang.FunctionInfo{File: "b.go", Line: 1, Name: "high"}, Complexity: 15},
		{FunctionInfo: lang.FunctionInfo{File: "c.go", Line: 1, Name: "medium"}, Complexity: 10},
	}

	findings, values, failCount := collectComplexityFindings(results, 10)

	if failCount != 1 {
		t.Errorf("failCount = %d, want 1", failCount)
	}
	if len(findings) != 1 {
		t.Errorf("findings = %d, want 1", len(findings))
	}
	if len(values) != 3 {
		t.Errorf("values = %d, want 3", len(values))
	}
}

func TestCollectComplexityFindings_AtBoundary(t *testing.T) {
	results := []lang.FunctionComplexity{
		{FunctionInfo: lang.FunctionInfo{File: "a.go", Line: 1, Name: "exact"}, Complexity: 10},
		{FunctionInfo: lang.FunctionInfo{File: "b.go", Line: 1, Name: "over"}, Complexity: 11},
	}

	_, _, failCount := collectComplexityFindings(results, 10)
	if failCount != 1 {
		t.Errorf("failCount = %d, want 1 (11 > 10, 10 is not > 10)", failCount)
	}
}

func TestMean(t *testing.T) {
	if got := mean(nil); got != 0 {
		t.Errorf("mean(nil) = %f, want 0", got)
	}
	if got := mean([]float64{2, 4, 6}); got != 4 {
		t.Errorf("mean([2,4,6]) = %f, want 4", got)
	}
}

func TestMedian(t *testing.T) {
	if got := median(nil); got != 0 {
		t.Errorf("median(nil) = %f, want 0", got)
	}
	if got := median([]float64{3, 1, 2}); got != 2 {
		t.Errorf("median([3,1,2]) = %f, want 2", got)
	}
	if got := median([]float64{4, 1, 3, 2}); got != 2.5 {
		t.Errorf("median([4,1,3,2]) = %f, want 2.5", got)
	}
}

func TestMax(t *testing.T) {
	if got := max(nil); got != 0 {
		t.Errorf("max(nil) = %f, want 0", got)
	}
	if got := max([]float64{3, 7, 1, 5}); got != 7 {
		t.Errorf("max([3,7,1,5]) = %f, want 7", got)
	}
}
