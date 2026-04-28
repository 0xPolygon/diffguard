package deadcode

import (
	"errors"
	"testing"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
	"github.com/0xPolygon/diffguard/internal/report"
)

// stubDetector is a minimal in-memory DeadCodeDetector that replays a fixed
// list of unused symbols (per file path) without touching the filesystem.
// Lets the orchestrator tests run without spinning up a real Go toolchain.
type stubDetector struct {
	byPath map[string][]lang.UnusedSymbol
	err    error
}

func (s *stubDetector) FindDeadCode(_ string, fc diff.FileChange) ([]lang.UnusedSymbol, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.byPath[fc.Path], nil
}

func TestAnalyze_NilDetectorPasses(t *testing.T) {
	d := &diff.Result{Files: []diff.FileChange{{Path: "a.go"}}}
	s, err := Analyze("/repo", d, nil)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if s.Severity != report.SeverityPass {
		t.Errorf("severity = %v, want PASS", s.Severity)
	}
	if s.Name != "Dead Code" {
		t.Errorf("name = %q, want Dead Code", s.Name)
	}
}

func TestAnalyze_NoDeadCodePasses(t *testing.T) {
	d := &diff.Result{Files: []diff.FileChange{{Path: "a.go"}}}
	det := &stubDetector{byPath: map[string][]lang.UnusedSymbol{}}
	s, err := Analyze("/repo", d, det)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if s.Severity != report.SeverityPass {
		t.Errorf("severity = %v, want PASS", s.Severity)
	}
}

func TestAnalyze_DeadCodeWarns(t *testing.T) {
	d := &diff.Result{Files: []diff.FileChange{{Path: "a.go"}, {Path: "b.go"}}}
	det := &stubDetector{
		byPath: map[string][]lang.UnusedSymbol{
			"a.go": {{File: "a.go", Line: 5, Name: "foo", Kind: "func"}},
			"b.go": {{File: "b.go", Line: 10, Name: "bar", Kind: "var"}},
		},
	}
	s, err := Analyze("/repo", d, det)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if s.Severity != report.SeverityWarn {
		t.Errorf("severity = %v, want WARN", s.Severity)
	}
	if len(s.Findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(s.Findings))
	}
	// Findings should be sorted by file then line.
	if s.Findings[0].File != "a.go" || s.Findings[1].File != "b.go" {
		t.Errorf("findings not sorted by file: %+v", s.Findings)
	}
	for _, f := range s.Findings {
		if f.Severity != report.SeverityWarn {
			t.Errorf("finding severity = %v, want WARN", f.Severity)
		}
	}
}

func TestAnalyze_DetectorErrorPropagates(t *testing.T) {
	d := &diff.Result{Files: []diff.FileChange{{Path: "a.go"}}}
	det := &stubDetector{err: errors.New("boom")}
	_, err := Analyze("/repo", d, det)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestBuildSection_StatsByKind(t *testing.T) {
	results := []lang.UnusedSymbol{
		{File: "a.go", Line: 1, Name: "f1", Kind: "func"},
		{File: "a.go", Line: 2, Name: "f2", Kind: "func"},
		{File: "b.go", Line: 3, Name: "v1", Kind: "var"},
	}
	s := buildSection(results)
	stats, ok := s.Stats.(map[string]any)
	if !ok {
		t.Fatalf("stats wrong type: %T", s.Stats)
	}
	if stats["unused_symbols"] != 3 {
		t.Errorf("unused_symbols = %v, want 3", stats["unused_symbols"])
	}
	byKind, ok := stats["by_kind"].(map[string]int)
	if !ok {
		t.Fatalf("by_kind wrong type: %T", stats["by_kind"])
	}
	if byKind["func"] != 2 || byKind["var"] != 1 {
		t.Errorf("by_kind = %+v, want func:2 var:1", byKind)
	}
}

func TestBuildSection_FindingMessage(t *testing.T) {
	s := buildSection([]lang.UnusedSymbol{
		{File: "a.go", Line: 1, Name: "foo", Kind: "func"},
	})
	if len(s.Findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(s.Findings))
	}
	want := `unused func "foo"`
	if s.Findings[0].Message != want {
		t.Errorf("message = %q, want %q", s.Findings[0].Message, want)
	}
	if s.Findings[0].Function != "foo" {
		t.Errorf("function = %q, want foo", s.Findings[0].Function)
	}
}

func TestPluralize(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "symbols"},
		{1, "symbol"},
		{2, "symbols"},
	}
	for _, tt := range tests {
		if got := pluralize("symbol", tt.n); got != tt.want {
			t.Errorf("pluralize(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}
