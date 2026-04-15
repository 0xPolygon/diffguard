package complexity

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

func TestWalkStmt_NestingPenalty(t *testing.T) {
	// Nesting penalty must be additive, not subtractive.
	// If `1 + nesting` were mutated to `1 - nesting`, nested constructs
	// would produce wrong (lower) values.
	tests := []struct {
		name     string
		code     string
		expected int
	}{
		{
			"range at nesting 1 with body",
			`package p; func f(x int) {
				if x > 0 {
					for range []int{} {
						if x > 0 {}
					}
				}
			}`,
			// if(1+0) + range(1+1) + inner_if(1+2) = 1 + 2 + 3 = 6
			6,
		},
		{
			"switch at nesting 1 with body",
			`package p; func f(x int) {
				if x > 0 {
					switch x {
					case 1:
						if x > 0 {}
					}
				}
			}`,
			// if(1+0) + switch(1+1) + case_if(1+2) = 1 + 2 + 3 = 6
			6,
		},
		{
			"select at nesting 1 with body",
			`package p; func f(x int, c chan int) {
				if x > 0 {
					select {
					case <-c:
						if x > 0 {}
					}
				}
			}`,
			// if(1+0) + select(1+1) + case_if(1+2) = 1 + 2 + 3 = 6
			6,
		},
		{
			"type switch at nesting 1 with body",
			`package p; func f(x int, v any) {
				if x > 0 {
					switch v.(type) {
					case int:
						if x > 0 {}
					}
				}
			}`,
			// if(1+0) + typeswitch(1+1) + case_if(1+2) = 1 + 2 + 3 = 6
			6,
		},
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
				t.Errorf("complexity = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestWalkForStmt_WithLogicalCondition(t *testing.T) {
	// Tests that for-loop conditions with logical ops are counted.
	// If `s.Cond != nil` were mutated to `s.Cond == nil`, the logical
	// ops in the condition would be missed.
	code := `package p; func f(a, b bool) { for a && b {} }`
	// for(1) + &&(1) = 2
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "test.go", code, 0)
	var fn *ast.FuncDecl
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok {
			fn = fd
			break
		}
	}
	got := computeComplexity(fn.Body)
	if got != 2 {
		t.Errorf("complexity = %d, want 2 (for + logical op)", got)
	}
}

func TestWalkIfStmt_WithElseChain(t *testing.T) {
	code := `package p
func f(x int) {
	if x > 0 {
	} else if x < 0 {
	} else {
	}
}`
	// if(1) + else if(1) + else(1) = 3
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "test.go", code, 0)
	var fn *ast.FuncDecl
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok {
			fn = fd
			break
		}
	}
	got := computeComplexity(fn.Body)
	if got != 3 {
		t.Errorf("complexity = %d, want 3", got)
	}
}

func TestWalkIfStmt_WithInit(t *testing.T) {
	// Tests that if-init is processed for complexity.
	code := `package p
func f() error {
	if err := g(); err != nil {
	}
	return nil
}
func g() error { return nil }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", code, 0)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	var fn *ast.FuncDecl
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name.Name == "f" {
			fn = fd
			break
		}
	}
	got := computeComplexity(fn.Body)
	// if(1+0) = 1 (init is an assignment with no control flow)
	if got != 1 {
		t.Errorf("complexity = %d, want 1", got)
	}
}

func TestWalkElseChain_NestedInit(t *testing.T) {
	code := `package p
func f(x int) error {
	if x > 0 {
	} else if err := g(); err != nil {
	}
	return nil
}
func g() error { return nil }
`
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "test.go", code, 0)
	var fn *ast.FuncDecl
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name.Name == "f" {
			fn = fd
			break
		}
	}
	got := computeComplexity(fn.Body)
	// if(1) + else-if(1) = 2
	if got != 2 {
		t.Errorf("complexity = %d, want 2", got)
	}
}

func TestWalkElseChain_WithNestedBody(t *testing.T) {
	// Tests that nesting+1 is correctly applied in walkElseChain's body.
	code := `package p
func f(x int) {
	if x > 0 {
	} else if x < 0 {
		if x < -10 {
		}
	}
}`
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "test.go", code, 0)
	var fn *ast.FuncDecl
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok {
			fn = fd
			break
		}
	}
	got := computeComplexity(fn.Body)
	// if(1+0) + else-if(1) + nested-if(1+1nesting) = 1 + 1 + 2 = 4
	if got != 4 {
		t.Errorf("complexity = %d, want 4", got)
	}
}

func TestBuildSection_StatsValues(t *testing.T) {
	results := []FunctionComplexity{
		{File: "a.go", Line: 1, Name: "f1", Complexity: 4},
		{File: "b.go", Line: 1, Name: "f2", Complexity: 8},
		{File: "c.go", Line: 1, Name: "f3", Complexity: 12},
	}

	s := buildSection(results, 10)

	stats := s.Stats.(map[string]any)
	if stats["total_functions"] != 3 {
		t.Errorf("total_functions = %v, want 3", stats["total_functions"])
	}
	if stats["violations"] != 1 {
		t.Errorf("violations = %v, want 1", stats["violations"])
	}
	// mean = (4+8+12)/3 = 8.0
	if stats["mean"] != 8.0 {
		t.Errorf("mean = %v, want 8.0", stats["mean"])
	}
	// median of [4,8,12] = 8
	if stats["median"] != 8.0 {
		t.Errorf("median = %v, want 8.0", stats["median"])
	}
	// max = 12
	if stats["max"] != 12.0 {
		t.Errorf("max = %v, want 12.0", stats["max"])
	}
}

func TestComputeComplexity_NilBody(t *testing.T) {
	if got := computeComplexity(nil); got != 0 {
		t.Errorf("computeComplexity(nil) = %d, want 0", got)
	}
}

func TestAnalyzeFile(t *testing.T) {
	code := `package test

func simple() {
	x := 1
	_ = x
}

func withIf(a int) {
	if a > 0 {
	}
}
`
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	os.WriteFile(fp, []byte(code), 0644)

	fc := diff.FileChange{
		Path:    "test.go",
		Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 100}},
	}

	results := analyzeFile(dir, fc)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// simple should have complexity 0
	if results[0].Complexity != 0 {
		t.Errorf("simple complexity = %d, want 0", results[0].Complexity)
	}
	// withIf should have complexity 1
	if results[1].Complexity != 1 {
		t.Errorf("withIf complexity = %d, want 1", results[1].Complexity)
	}
}

func TestAnalyzeFile_ParseError(t *testing.T) {
	dir := t.TempDir()
	fc := diff.FileChange{
		Path:    "nonexistent.go",
		Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 10}},
	}

	results := analyzeFile(dir, fc)
	if results != nil {
		t.Error("expected nil for parse error")
	}
}

func TestAnalyzeFile_MultipleFunctions(t *testing.T) {
	// If the ast.Inspect callback's `return true` (for non-FuncDecl nodes)
	// were mutated to `return false`, only the first function would be found.
	code := `package test

type S struct{}

func (s S) Method1() {
	if true {}
}

func (s *S) Method2() {
	if true {}
}

func TopLevel() {
	if true {}
}
`
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	os.WriteFile(fp, []byte(code), 0644)

	fc := diff.FileChange{
		Path:    "test.go",
		Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 100}},
	}

	results := analyzeFile(dir, fc)
	if len(results) != 3 {
		t.Errorf("expected 3 functions, got %d", len(results))
	}
}

func TestAnalyzeFile_OutOfRange(t *testing.T) {
	code := `package test

func f() {
	x := 1
	_ = x
}
`
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	os.WriteFile(fp, []byte(code), 0644)

	fc := diff.FileChange{
		Path:    "test.go",
		Regions: []diff.ChangedRegion{{StartLine: 100, EndLine: 200}},
	}

	results := analyzeFile(dir, fc)
	if len(results) != 0 {
		t.Errorf("expected 0 results for out-of-range, got %d", len(results))
	}
}

func TestCollectComplexityFindings(t *testing.T) {
	results := []FunctionComplexity{
		{File: "a.go", Line: 1, Name: "low", Complexity: 5},
		{File: "b.go", Line: 1, Name: "high", Complexity: 15},
		{File: "c.go", Line: 1, Name: "medium", Complexity: 10},
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
	results := []FunctionComplexity{
		{File: "a.go", Line: 1, Name: "exact", Complexity: 10},
		{File: "b.go", Line: 1, Name: "over", Complexity: 11},
	}

	_, _, failCount := collectComplexityFindings(results, 10)
	if failCount != 1 {
		t.Errorf("failCount = %d, want 1 (11 > 10, 10 is not > 10)", failCount)
	}
}

func TestBuildSection_Empty(t *testing.T) {
	s := buildSection(nil, 10)
	if s.Severity != report.SeverityPass {
		t.Errorf("severity = %v, want PASS", s.Severity)
	}
}

func TestBuildSection_WithViolations(t *testing.T) {
	results := []FunctionComplexity{
		{File: "a.go", Line: 1, Name: "complex", Complexity: 20},
		{File: "b.go", Line: 1, Name: "simple", Complexity: 3},
	}

	s := buildSection(results, 10)
	if s.Severity != report.SeverityFail {
		t.Errorf("severity = %v, want FAIL", s.Severity)
	}
	if len(s.Findings) != 1 {
		t.Errorf("findings = %d, want 1", len(s.Findings))
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
	// Odd count
	if got := median([]float64{3, 1, 2}); got != 2 {
		t.Errorf("median([3,1,2]) = %f, want 2", got)
	}
	// Even count
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

func TestWalkStmt_LabeledStmt(t *testing.T) {
	code := `package p
func f(x int) {
outer:
	for x > 0 {
		_ = x
		break outer
	}
}`
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "test.go", code, 0)
	var fn *ast.FuncDecl
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok {
			fn = fd
			break
		}
	}
	got := computeComplexity(fn.Body)
	// labeled for(1) = 1
	if got != 1 {
		t.Errorf("complexity = %d, want 1", got)
	}
}

func TestWalkStmt_GoAndDefer(t *testing.T) {
	code := `package p
func f() {
	go func() {
		if true {}
	}()
	defer func() {
		if true {}
	}()
}`
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "test.go", code, 0)
	var fn *ast.FuncDecl
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok {
			fn = fd
			break
		}
	}
	got := computeComplexity(fn.Body)
	// go func: if(1+1nesting) = 2
	// defer func: if(1+1nesting) = 2
	// total = 4
	if got != 4 {
		t.Errorf("complexity = %d, want 4", got)
	}
}

func TestWalkStmt_FuncLitInAssign(t *testing.T) {
	code := `package p
func f() {
	x := func() {
		if true {}
	}
	_ = x
}`
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "test.go", code, 0)
	var fn *ast.FuncDecl
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok {
			fn = fd
			break
		}
	}
	got := computeComplexity(fn.Body)
	// func lit with if at nesting 1: if(1+1) = 2
	if got != 2 {
		t.Errorf("complexity = %d, want 2", got)
	}
}

func TestWalkStmt_FuncLitInReturn(t *testing.T) {
	code := `package p
func f() func() {
	return func() {
		if true {}
	}
}`
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "test.go", code, 0)
	var fn *ast.FuncDecl
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok {
			fn = fd
			break
		}
	}
	got := computeComplexity(fn.Body)
	// return func lit with if at nesting 1: if(1+1) = 2
	if got != 2 {
		t.Errorf("complexity = %d, want 2", got)
	}
}
