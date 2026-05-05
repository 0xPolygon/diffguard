package sizes

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
	_ "github.com/0xPolygon/diffguard/internal/lang/goanalyzer"
	"github.com/0xPolygon/diffguard/internal/report"
)

func goExtractor(t *testing.T) lang.FunctionExtractor {
	t.Helper()
	l, ok := lang.Get("go")
	if !ok {
		t.Fatal("go language not registered")
	}
	return l.FunctionExtractor()
}

func TestCheckFuncSizes(t *testing.T) {
	funcs := []lang.FunctionSize{
		{FunctionInfo: lang.FunctionInfo{File: "a.go", Line: 1, Name: "small"}, Lines: 10},
		{FunctionInfo: lang.FunctionInfo{File: "b.go", Line: 1, Name: "big"}, Lines: 60},
		{FunctionInfo: lang.FunctionInfo{File: "c.go", Line: 1, Name: "huge"}, Lines: 100},
	}

	findings := checkFuncSizes(funcs, 50, nil)
	if len(findings) != 2 {
		t.Errorf("expected 2 violations, got %d", len(findings))
	}
	for _, f := range findings {
		if f.Severity != report.SeverityFail {
			t.Error("expected FAIL severity")
		}
	}
}

func TestCheckFuncSizes_AtBoundary(t *testing.T) {
	funcs := []lang.FunctionSize{
		{FunctionInfo: lang.FunctionInfo{File: "a.go", Line: 1, Name: "exact"}, Lines: 50},
		{FunctionInfo: lang.FunctionInfo{File: "b.go", Line: 1, Name: "over"}, Lines: 51},
	}

	findings := checkFuncSizes(funcs, 50, nil)
	if len(findings) != 1 {
		t.Errorf("expected 1 violation (51 > 50), got %d", len(findings))
	}
}

func TestCheckFileSizes(t *testing.T) {
	files := []lang.FileSize{
		{Path: "small.go", Lines: 100},
		{Path: "big.go", Lines: 600},
	}

	findings := checkFileSizes(files, 500, nil)
	if len(findings) != 1 {
		t.Errorf("expected 1 violation, got %d", len(findings))
	}
}

func TestCheckFileSizes_AtBoundary(t *testing.T) {
	files := []lang.FileSize{
		{Path: "exact.go", Lines: 500},
		{Path: "over.go", Lines: 501},
	}

	findings := checkFileSizes(files, 500, nil)
	if len(findings) != 1 {
		t.Errorf("expected 1 violation (501 > 500), got %d", len(findings))
	}
}

func TestBuildSection_Empty(t *testing.T) {
	s := buildSection(nil, nil, 50, 500, nil, nil)
	if s.Severity != report.SeverityPass {
		t.Errorf("empty section severity = %v, want PASS", s.Severity)
	}
	if s.Name != "Code Sizes" {
		t.Errorf("name = %q, want Code Sizes", s.Name)
	}
}

func TestBuildSection_WithViolations(t *testing.T) {
	funcs := []lang.FunctionSize{{FunctionInfo: lang.FunctionInfo{File: "a.go", Line: 1, Name: "big"}, Lines: 100}}
	s := buildSection(funcs, nil, 50, 500, nil, nil)
	if s.Severity != report.SeverityFail {
		t.Errorf("section severity = %v, want FAIL", s.Severity)
	}
	if len(s.Findings) != 1 {
		t.Errorf("expected 1 finding, got %d", len(s.Findings))
	}
}

func TestBuildSection_NoViolations(t *testing.T) {
	funcs := []lang.FunctionSize{{FunctionInfo: lang.FunctionInfo{File: "a.go", Line: 1, Name: "small"}, Lines: 10}}
	files := []lang.FileSize{{Path: "a.go", Lines: 100}}
	s := buildSection(funcs, files, 50, 500, nil, nil)
	if s.Severity != report.SeverityPass {
		t.Errorf("severity = %v, want PASS", s.Severity)
	}
}

func TestBuildSection_SortedByValue(t *testing.T) {
	funcs := []lang.FunctionSize{
		{FunctionInfo: lang.FunctionInfo{File: "a.go", Line: 1, Name: "medium"}, Lines: 60},
		{FunctionInfo: lang.FunctionInfo{File: "b.go", Line: 1, Name: "huge"}, Lines: 200},
		{FunctionInfo: lang.FunctionInfo{File: "c.go", Line: 1, Name: "big"}, Lines: 80},
	}
	s := buildSection(funcs, nil, 50, 500, nil, nil)
	if len(s.Findings) != 3 {
		t.Fatalf("expected 3 findings, got %d", len(s.Findings))
	}
	if s.Findings[0].Value != 200 {
		t.Errorf("first finding value = %v, want 200", s.Findings[0].Value)
	}
}

// TestAnalyze_WithGoExtractor is the integration replacement for the old
// analyzeFile-based unit tests. The AST walk logic now lives in goanalyzer
// and has its own tests; here we only verify the orchestration wiring.
func TestAnalyze_WithGoExtractor(t *testing.T) {
	code := `package test

func small() {
	x := 1
	_ = x
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

	section, err := Analyze(dir, d, 50, 500, goExtractor(t))
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	if section.Severity != report.SeverityPass {
		t.Errorf("severity = %v, want PASS", section.Severity)
	}
}
