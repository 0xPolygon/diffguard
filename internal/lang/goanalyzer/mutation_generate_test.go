package goanalyzer

import (
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xPolygon/diffguard/internal/diff"
)

func TestBinaryMutants(t *testing.T) {
	tests := []struct {
		name     string
		op       token.Token
		expected int
	}{
		{"greater than", token.GTR, 1},
		{"less than", token.LSS, 1},
		{"equal", token.EQL, 1},
		{"not equal", token.NEQ, 1},
		{"add", token.ADD, 1},
		{"subtract", token.SUB, 1},
		{"multiply", token.MUL, 1},
		{"divide", token.QUO, 1},
		{"and (no mutation)", token.LAND, 0},
		{"or (no mutation)", token.LOR, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr := &ast.BinaryExpr{Op: tt.op}
			mutants := binaryMutants("test.go", 1, expr)
			if len(mutants) != tt.expected {
				t.Errorf("binaryMutants(%v) = %d mutants, want %d", tt.op, len(mutants), tt.expected)
			}
		})
	}
}

func TestBoolMutants(t *testing.T) {
	tests := []struct {
		name     string
		ident    string
		expected int
	}{
		{"true", "true", 1},
		{"false", "false", 1},
		{"other", "x", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ident := &ast.Ident{Name: tt.ident}
			mutants := boolMutants("test.go", 1, ident)
			if len(mutants) != tt.expected {
				t.Errorf("boolMutants(%q) = %d, want %d", tt.ident, len(mutants), tt.expected)
			}
		})
	}
}

func TestReturnMutants(t *testing.T) {
	ret := &ast.ReturnStmt{Results: []ast.Expr{&ast.Ident{Name: "x"}}}
	mutants := returnMutants("test.go", 1, ret)
	if len(mutants) != 1 {
		t.Errorf("returnMutants with values: got %d, want 1", len(mutants))
	}

	bareRet := &ast.ReturnStmt{}
	mutants = returnMutants("test.go", 1, bareRet)
	if len(mutants) != 0 {
		t.Errorf("returnMutants bare: got %d, want 0", len(mutants))
	}
}

func TestIncDecMutants(t *testing.T) {
	incStmt := &ast.IncDecStmt{Tok: token.INC}
	m := incdecMutants("a.go", 5, incStmt)
	if len(m) != 1 {
		t.Fatalf("expected 1 mutant for ++, got %d", len(m))
	}
	if m[0].Operator != "incdec" {
		t.Errorf("operator = %q, want incdec", m[0].Operator)
	}
	if !strings.Contains(m[0].Description, "--") {
		t.Errorf("description = %q", m[0].Description)
	}

	decStmt := &ast.IncDecStmt{Tok: token.DEC}
	m = incdecMutants("a.go", 5, decStmt)
	if len(m) != 1 {
		t.Fatalf("expected 1 mutant for --, got %d", len(m))
	}

	other := &ast.IncDecStmt{Tok: token.ADD}
	if ms := incdecMutants("a.go", 5, other); len(ms) != 0 {
		t.Errorf("unexpected mutants for non-incdec tok: %+v", ms)
	}
}

func TestIfBodyMutants(t *testing.T) {
	body := &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.Ident{Name: "x"}}}}
	ifStmt := &ast.IfStmt{Cond: &ast.Ident{Name: "cond"}, Body: body}
	m := ifBodyMutants("a.go", 5, ifStmt)
	if len(m) != 1 {
		t.Fatalf("expected 1 mutant, got %d", len(m))
	}
	if m[0].Operator != "branch_removal" {
		t.Errorf("operator = %q, want branch_removal", m[0].Operator)
	}

	empty := &ast.IfStmt{Cond: &ast.Ident{Name: "cond"}, Body: &ast.BlockStmt{}}
	if ms := ifBodyMutants("a.go", 5, empty); len(ms) != 0 {
		t.Errorf("expected no mutants for empty if body, got %d", len(ms))
	}
}

func TestExprStmtMutants_CallExpr(t *testing.T) {
	call := &ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.Ident{Name: "foo"}}}
	m := exprStmtMutants("a.go", 5, call)
	if len(m) != 1 {
		t.Fatalf("expected 1 mutant, got %d", len(m))
	}
	if m[0].Operator != "statement_deletion" {
		t.Errorf("operator = %q", m[0].Operator)
	}
}

func TestExprStmtMutants_NonCall(t *testing.T) {
	stmt := &ast.ExprStmt{X: &ast.Ident{Name: "x"}}
	if ms := exprStmtMutants("a.go", 5, stmt); len(ms) != 0 {
		t.Errorf("expected no mutants for non-call, got %d", len(ms))
	}
}

func TestOperatorName(t *testing.T) {
	tests := []struct {
		from, to token.Token
		expected string
	}{
		{token.GTR, token.GEQ, "conditional_boundary"},
		{token.EQL, token.NEQ, "negate_conditional"},
		{token.ADD, token.SUB, "math_operator"},
	}
	for _, tt := range tests {
		got := operatorName(tt.from, tt.to)
		if got != tt.expected {
			t.Errorf("operatorName(%v, %v) = %q, want %q", tt.from, tt.to, got, tt.expected)
		}
	}
}

func TestIsBoundary(t *testing.T) {
	if !isBoundary(token.GTR) {
		t.Error("GTR should be boundary")
	}
	if !isBoundary(token.GEQ) {
		t.Error("GEQ should be boundary")
	}
	if isBoundary(token.EQL) {
		t.Error("EQL should not be boundary")
	}
}

func TestIsComparison(t *testing.T) {
	if !isComparison(token.EQL) {
		t.Error("EQL should be comparison")
	}
	if isComparison(token.GTR) {
		t.Error("GTR should not be comparison")
	}
}

func TestIsMath(t *testing.T) {
	if !isMath(token.ADD) {
		t.Error("ADD should be math")
	}
	if isMath(token.EQL) {
		t.Error("EQL should not be math")
	}
}

func TestGenerateMutants_EndToEnd(t *testing.T) {
	code := `package test

func add(a, b int) int {
	if a > b {
		return a + b
	}
	return a - b
}
`
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.go")
	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		t.Fatal(err)
	}

	fc := diff.FileChange{
		Path:    "test.go",
		Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 8}},
	}

	mutants, err := mutantGeneratorImpl{}.GenerateMutants(filePath, fc, nil)
	if err != nil {
		t.Fatalf("GenerateMutants: %v", err)
	}
	if len(mutants) == 0 {
		t.Error("expected mutants, got none")
	}

	operators := make(map[string]int)
	for _, m := range mutants {
		operators[m.Operator]++
	}

	if operators["conditional_boundary"] == 0 {
		t.Error("expected conditional_boundary mutants")
	}
	if operators["math_operator"] == 0 {
		t.Error("expected math_operator mutants")
	}
}

func TestGenerateMutants_WithAllTypes(t *testing.T) {
	code := `package test

func f(a, b int) bool {
	if a > b {
		return true
	}
	x := a + b
	_ = x
	return false
}
`
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	os.WriteFile(fp, []byte(code), 0644)

	fc := diff.FileChange{
		Path:    "test.go",
		Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 20}},
	}

	mutants, err := mutantGeneratorImpl{}.GenerateMutants(fp, fc, nil)
	if err != nil {
		t.Fatalf("GenerateMutants: %v", err)
	}

	operators := make(map[string]int)
	for _, m := range mutants {
		operators[m.Operator]++
	}

	for _, want := range []string{"conditional_boundary", "boolean_substitution", "math_operator", "return_value"} {
		if operators[want] == 0 {
			t.Errorf("missing %s mutants", want)
		}
	}
}

func TestGenerateMutants_HonorsDisableNextLine(t *testing.T) {
	code := `package test

func f(x int) bool {
	// mutator-disable-next-line
	if x > 0 {
		return true
	}
	if x < 0 {
		return false
	}
	return false
}
`
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	os.WriteFile(fp, []byte(code), 0644)

	fc := diff.FileChange{
		Path:    "test.go",
		Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 100}},
	}

	disabled, err := annotationScannerImpl{}.ScanAnnotations(fp)
	if err != nil {
		t.Fatal(err)
	}
	mutants, err := mutantGeneratorImpl{}.GenerateMutants(fp, fc, disabled)
	if err != nil {
		t.Fatal(err)
	}

	for _, m := range mutants {
		if m.Line == 5 {
			t.Errorf("expected no mutants on annotated line 5, got: %+v", m)
		}
	}

	foundAt8 := false
	for _, m := range mutants {
		if m.Line == 8 {
			foundAt8 = true
		}
	}
	if !foundAt8 {
		t.Error("expected mutants on un-annotated line 8")
	}
}
