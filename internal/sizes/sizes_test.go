package sizes

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/report"
)

func TestAnalyzeFile(t *testing.T) {
	code := `package test

func short() {
	x := 1
	_ = x
}

func longer() {
	a := 1
	b := 2
	c := 3
	d := 4
	e := 5
	_ = a + b + c + d + e
}
`
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.go")
	os.WriteFile(filePath, []byte(code), 0644)

	fc := diff.FileChange{
		Path:    "test.go",
		Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 100}},
	}

	funcs, fileSize := analyzeFile(dir, fc)

	if fileSize == nil {
		t.Fatal("expected non-nil fileSize")
	}
	if fileSize.Lines == 0 {
		t.Error("file should have non-zero lines")
	}
	if fileSize.Path != "test.go" {
		t.Errorf("fileSize.Path = %q, want test.go", fileSize.Path)
	}

	if len(funcs) != 2 {
		t.Fatalf("expected 2 functions, got %d", len(funcs))
	}
	if funcs[0].Name != "short" {
		t.Errorf("funcs[0].Name = %q, want short", funcs[0].Name)
	}
	if funcs[0].Lines <= 0 {
		t.Error("function lines should be > 0")
	}
}

func TestAnalyzeFile_ParseError(t *testing.T) {
	dir := t.TempDir()
	fc := diff.FileChange{
		Path:    "nonexistent.go",
		Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 10}},
	}

	funcs, fileSize := analyzeFile(dir, fc)
	if funcs != nil {
		t.Error("expected nil funcs for parse error")
	}
	if fileSize != nil {
		t.Error("expected nil fileSize for parse error")
	}
}

func TestCollectFunctionSizes_OnlyInRange(t *testing.T) {
	code := `package test

func inRange() {
	x := 1
	_ = x
}

func outOfRange() {
	y := 2
	_ = y
}
`
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.go")
	os.WriteFile(filePath, []byte(code), 0644)

	fc := diff.FileChange{
		Path:    "test.go",
		Regions: []diff.ChangedRegion{{StartLine: 3, EndLine: 6}},
	}

	funcs, _ := analyzeFile(dir, fc)
	if len(funcs) != 1 {
		t.Fatalf("expected 1 function in range, got %d", len(funcs))
	}
	if funcs[0].Name != "inRange" {
		t.Errorf("expected inRange, got %s", funcs[0].Name)
	}
}

func TestCollectFunctionSizes_LineCalc(t *testing.T) {
	code := `package test

func f() {
	a := 1
	b := 2
	c := 3
	_ = a + b + c
}
`
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.go")
	os.WriteFile(filePath, []byte(code), 0644)

	fc := diff.FileChange{
		Path:    "test.go",
		Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 100}},
	}

	funcs, _ := analyzeFile(dir, fc)
	if len(funcs) != 1 {
		t.Fatalf("expected 1 function, got %d", len(funcs))
	}
	// func f() { starts at line 3, } at line 8 = 6 lines
	if funcs[0].Lines != 6 {
		t.Errorf("function lines = %d, want 6", funcs[0].Lines)
	}
}

func TestCheckFuncSizes(t *testing.T) {
	funcs := []FunctionSize{
		{File: "a.go", Line: 1, Name: "small", Lines: 10},
		{File: "b.go", Line: 1, Name: "big", Lines: 60},
		{File: "c.go", Line: 1, Name: "huge", Lines: 100},
	}

	findings := checkFuncSizes(funcs, 50)
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
	funcs := []FunctionSize{
		{File: "a.go", Line: 1, Name: "exact", Lines: 50},
		{File: "b.go", Line: 1, Name: "over", Lines: 51},
	}

	findings := checkFuncSizes(funcs, 50)
	if len(findings) != 1 {
		t.Errorf("expected 1 violation (51 > 50), got %d", len(findings))
	}
}

func TestCheckFileSizes(t *testing.T) {
	files := []FileSize{
		{Path: "small.go", Lines: 100},
		{Path: "big.go", Lines: 600},
	}

	findings := checkFileSizes(files, 500)
	if len(findings) != 1 {
		t.Errorf("expected 1 violation, got %d", len(findings))
	}
}

func TestCheckFileSizes_AtBoundary(t *testing.T) {
	files := []FileSize{
		{Path: "exact.go", Lines: 500},
		{Path: "over.go", Lines: 501},
	}

	findings := checkFileSizes(files, 500)
	if len(findings) != 1 {
		t.Errorf("expected 1 violation (501 > 500), got %d", len(findings))
	}
}

func TestBuildSection_Empty(t *testing.T) {
	s := buildSection(nil, nil, 50, 500)
	if s.Severity != report.SeverityPass {
		t.Errorf("empty section severity = %v, want PASS", s.Severity)
	}
	if s.Name != "Code Sizes" {
		t.Errorf("name = %q, want Code Sizes", s.Name)
	}
}

func TestBuildSection_WithViolations(t *testing.T) {
	funcs := []FunctionSize{{File: "a.go", Line: 1, Name: "big", Lines: 100}}
	s := buildSection(funcs, nil, 50, 500)
	if s.Severity != report.SeverityFail {
		t.Errorf("section severity = %v, want FAIL", s.Severity)
	}
	if len(s.Findings) != 1 {
		t.Errorf("expected 1 finding, got %d", len(s.Findings))
	}
}

func TestBuildSection_NoViolations(t *testing.T) {
	funcs := []FunctionSize{{File: "a.go", Line: 1, Name: "small", Lines: 10}}
	files := []FileSize{{Path: "a.go", Lines: 100}}
	s := buildSection(funcs, files, 50, 500)
	if s.Severity != report.SeverityPass {
		t.Errorf("severity = %v, want PASS", s.Severity)
	}
}

func TestBuildSection_SortedByValue(t *testing.T) {
	funcs := []FunctionSize{
		{File: "a.go", Line: 1, Name: "medium", Lines: 60},
		{File: "b.go", Line: 1, Name: "huge", Lines: 200},
		{File: "c.go", Line: 1, Name: "big", Lines: 80},
	}
	s := buildSection(funcs, nil, 50, 500)
	if len(s.Findings) != 3 {
		t.Fatalf("expected 3 findings, got %d", len(s.Findings))
	}
	if s.Findings[0].Value != 200 {
		t.Errorf("first finding value = %v, want 200", s.Findings[0].Value)
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
		t.Run(tt.expected, func(t *testing.T) {
			dir := t.TempDir()
			fp := filepath.Join(dir, "test.go")
			os.WriteFile(fp, []byte(tt.code), 0644)

			fc := diff.FileChange{
				Path:    "test.go",
				Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 100}},
			}
			funcs, _ := analyzeFile(dir, fc)
			found := false
			for _, f := range funcs {
				if f.Name == tt.expected {
					found = true
				}
			}
			if !found {
				t.Errorf("funcName not found: want %q, got %v", tt.expected, funcs)
			}
		})
	}
}

func TestAnalyze(t *testing.T) {
	code := `package test

func small() {
	x := 1
	_ = x
}
`
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	os.WriteFile(fp, []byte(code), 0644)

	d := &diff.Result{
		Files: []diff.FileChange{
			{Path: "test.go", Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 100}}},
		},
	}

	section, err := Analyze(dir, d, 50, 500)
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	if section.Severity != report.SeverityPass {
		t.Errorf("severity = %v, want PASS", section.Severity)
	}
}
