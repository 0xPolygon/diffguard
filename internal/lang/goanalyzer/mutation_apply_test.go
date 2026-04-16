package goanalyzer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xPolygon/diffguard/internal/lang"
)

func TestApplyBinaryMutation_Success(t *testing.T) {
	expr := &ast.BinaryExpr{Op: token.GTR}
	site := lang.MutantSite{Description: "> -> >=", Operator: "conditional_boundary"}
	if !applyBinaryMutation(expr, site) {
		t.Error("expected successful apply")
	}
	if expr.Op != token.GEQ {
		t.Errorf("op = %v, want GEQ", expr.Op)
	}
}

func TestApplyBinaryMutation_WrongNodeType(t *testing.T) {
	ident := &ast.Ident{Name: "x"}
	site := lang.MutantSite{Description: "> -> >=", Operator: "conditional_boundary"}
	if applyBinaryMutation(ident, site) {
		t.Error("expected false for non-BinaryExpr")
	}
}

func TestApplyBinaryMutation_IllegalOp(t *testing.T) {
	expr := &ast.BinaryExpr{Op: token.GTR}
	site := lang.MutantSite{Description: "invalid", Operator: "conditional_boundary"}
	if applyBinaryMutation(expr, site) {
		t.Error("expected false for invalid description")
	}
}

// TestApplyBinaryMutation_OperatorMismatch locks in the fix for a bug where
// applyBinaryMutation rewrote the first BinaryExpr found on a line even
// when its operator differed from the mutant's intended `from` op.
func TestApplyBinaryMutation_OperatorMismatch(t *testing.T) {
	expr := &ast.BinaryExpr{Op: token.LAND}
	site := lang.MutantSite{Description: "!= -> ==", Operator: "negate_conditional"}
	if applyBinaryMutation(expr, site) {
		t.Error("expected false when expr.Op (&&) does not match mutant's from-op (!=)")
	}
	if expr.Op != token.LAND {
		t.Errorf("expr.Op = %v, want LAND", expr.Op)
	}
}

func TestApplyBinaryMutation_MathOperatorMismatch(t *testing.T) {
	expr := &ast.BinaryExpr{Op: token.SUB}
	site := lang.MutantSite{Description: "+ -> -", Operator: "math_operator"}
	if applyBinaryMutation(expr, site) {
		t.Error("expected false when expr.Op (-) does not match from-op (+)")
	}
}

func TestApplyBoolMutation_TrueToFalse(t *testing.T) {
	ident := &ast.Ident{Name: "true"}
	site := lang.MutantSite{Description: "true -> false", Operator: "boolean_substitution"}
	if !applyBoolMutation(ident, site) {
		t.Error("expected successful apply")
	}
	if ident.Name != "false" {
		t.Errorf("name = %q, want false", ident.Name)
	}
}

func TestApplyBoolMutation_FalseToTrue(t *testing.T) {
	ident := &ast.Ident{Name: "false"}
	site := lang.MutantSite{Description: "false -> true", Operator: "boolean_substitution"}
	if !applyBoolMutation(ident, site) {
		t.Error("expected successful apply")
	}
	if ident.Name != "true" {
		t.Errorf("name = %q, want true", ident.Name)
	}
}

func TestApplyBoolMutation_WrongNodeType(t *testing.T) {
	expr := &ast.BinaryExpr{Op: token.ADD}
	site := lang.MutantSite{Description: "true -> false", Operator: "boolean_substitution"}
	if applyBoolMutation(expr, site) {
		t.Error("expected false for non-Ident")
	}
}

func TestApplyReturnMutation_Success(t *testing.T) {
	ret := &ast.ReturnStmt{Results: []ast.Expr{&ast.Ident{Name: "x", NamePos: 1}}}
	if !applyReturnMutation(ret) {
		t.Error("expected successful apply")
	}
	if ident, ok := ret.Results[0].(*ast.Ident); !ok || ident.Name != "nil" {
		t.Error("expected result replaced with nil")
	}
}

func TestApplyReturnMutation_WrongNodeType(t *testing.T) {
	ident := &ast.Ident{Name: "x"}
	if applyReturnMutation(ident) {
		t.Error("expected false for non-ReturnStmt")
	}
}

func TestApplyIncDecMutation_Inc(t *testing.T) {
	stmt := &ast.IncDecStmt{Tok: token.INC}
	if !applyIncDecMutation(stmt) {
		t.Error("expected successful apply")
	}
	if stmt.Tok != token.DEC {
		t.Errorf("tok = %v, want DEC", stmt.Tok)
	}
}

func TestApplyIncDecMutation_Dec(t *testing.T) {
	stmt := &ast.IncDecStmt{Tok: token.DEC}
	if !applyIncDecMutation(stmt) {
		t.Error("expected successful apply")
	}
	if stmt.Tok != token.INC {
		t.Errorf("tok = %v, want INC", stmt.Tok)
	}
}

func TestApplyIncDecMutation_WrongNodeType(t *testing.T) {
	if applyIncDecMutation(&ast.Ident{Name: "x"}) {
		t.Error("expected false for non-IncDecStmt")
	}
}

func TestApplyBranchRemoval(t *testing.T) {
	body := &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.Ident{Name: "x"}}}}
	ifStmt := &ast.IfStmt{Cond: &ast.Ident{Name: "cond"}, Body: body}
	if !applyBranchRemoval(ifStmt) {
		t.Error("expected successful apply")
	}
	if len(ifStmt.Body.List) != 0 {
		t.Errorf("expected body emptied, got %d stmts", len(ifStmt.Body.List))
	}
}

func TestApplyBranchRemoval_WrongType(t *testing.T) {
	if applyBranchRemoval(&ast.Ident{Name: "x"}) {
		t.Error("expected false for non-IfStmt")
	}
}

func TestTryApplyMutation_Binary(t *testing.T) {
	expr := &ast.BinaryExpr{Op: token.ADD}
	site := lang.MutantSite{Description: "+ -> -", Operator: "math_operator"}
	if !tryApplyMutation(expr, site) {
		t.Error("expected successful apply")
	}
	if expr.Op != token.SUB {
		t.Errorf("op = %v, want SUB", expr.Op)
	}
}

func TestTryApplyMutation_Bool(t *testing.T) {
	ident := &ast.Ident{Name: "true"}
	site := lang.MutantSite{Description: "true -> false", Operator: "boolean_substitution"}
	if !tryApplyMutation(ident, site) {
		t.Error("expected successful apply")
	}
}

func TestTryApplyMutation_Return(t *testing.T) {
	ret := &ast.ReturnStmt{Results: []ast.Expr{&ast.Ident{Name: "x", NamePos: 1}}}
	site := lang.MutantSite{Operator: "return_value"}
	if !tryApplyMutation(ret, site) {
		t.Error("expected successful apply")
	}
}

func TestTryApplyMutation_Unknown(t *testing.T) {
	ident := &ast.Ident{Name: "x"}
	site := lang.MutantSite{Operator: "unknown_operator"}
	if tryApplyMutation(ident, site) {
		t.Error("expected false for unknown operator")
	}
}

func TestApplyMutationToAST(t *testing.T) {
	code := `package test

func f() bool {
	return true
}
`
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	os.WriteFile(fp, []byte(code), 0644)

	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, fp, nil, parser.ParseComments)

	site := lang.MutantSite{Line: 4, Description: "true -> false", Operator: "boolean_substitution"}
	if !applyMutationToAST(fset, f, site) {
		t.Error("expected mutation to be applied")
	}
}

func TestApplyMutationToAST_NoMatch(t *testing.T) {
	code := `package test

func f() int {
	return 42
}
`
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	os.WriteFile(fp, []byte(code), 0644)

	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, fp, nil, parser.ParseComments)

	site := lang.MutantSite{Line: 999, Description: "true -> false", Operator: "boolean_substitution"}
	if applyMutationToAST(fset, f, site) {
		t.Error("expected no mutation applied")
	}
}

func TestApplyMutation_Full(t *testing.T) {
	code := `package test

func f(a, b int) bool {
	return a > b
}
`
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	os.WriteFile(fp, []byte(code), 0644)

	site := lang.MutantSite{File: "test.go", Line: 4, Description: "> -> >=", Operator: "conditional_boundary"}
	result, _ := mutantApplierImpl{}.ApplyMutation(fp, site)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !strings.Contains(string(result), ">=") {
		t.Error("expected mutated code to contain >=")
	}
}

func TestApplyMutation_ParseError(t *testing.T) {
	site := lang.MutantSite{Line: 1, Operator: "boolean_substitution"}
	result, _ := mutantApplierImpl{}.ApplyMutation("/nonexistent/file.go", site)
	if result != nil {
		t.Error("expected nil for parse error")
	}
}

func TestApplyMutation_NoMatch(t *testing.T) {
	code := `package test

func f() {}
`
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	os.WriteFile(fp, []byte(code), 0644)

	site := lang.MutantSite{Line: 999, Operator: "boolean_substitution", Description: "true -> false"}
	result, _ := mutantApplierImpl{}.ApplyMutation(fp, site)
	if result != nil {
		t.Error("expected nil when mutation can't be applied")
	}
}

func TestApplyStatementDeletion(t *testing.T) {
	code := `package test

func f() {
	doThing()
	x := 1
	_ = x
}
`
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	os.WriteFile(fp, []byte(code), 0644)

	site := lang.MutantSite{Line: 4, Operator: "statement_deletion"}
	result, _ := mutantApplierImpl{}.ApplyMutation(fp, site)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if strings.Contains(string(result), "doThing()") {
		t.Errorf("expected doThing() removed, got:\n%s", string(result))
	}
}

func TestRenderFile(t *testing.T) {
	code := `package test

func f() {}
`
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "test.go", code, parser.ParseComments)

	result := renderFile(fset, f)
	if result == nil {
		t.Fatal("expected non-nil render result")
	}
	if !strings.Contains(string(result), "package test") {
		t.Error("rendered file should contain package declaration")
	}
}

func TestZeroValueExpr(t *testing.T) {
	original := &ast.Ident{Name: "x", NamePos: 42}
	result := zeroValueExpr(original)
	ident, ok := result.(*ast.Ident)
	if !ok {
		t.Fatal("expected *ast.Ident")
	}
	if ident.Name != "nil" {
		t.Errorf("name = %q, want nil", ident.Name)
	}
}

func TestParseMutationOp(t *testing.T) {
	tests := []struct {
		desc     string
		wantFrom token.Token
		wantTo   token.Token
	}{
		{"> -> >=", token.GTR, token.GEQ},
		{"== -> !=", token.EQL, token.NEQ},
		{"+ -> -", token.ADD, token.SUB},
		{"invalid", token.ILLEGAL, token.ILLEGAL},
		{"+ -> unknown", token.ILLEGAL, token.ILLEGAL},
	}
	for _, tt := range tests {
		gotFrom, gotTo := parseMutationOp(tt.desc)
		if gotFrom != tt.wantFrom || gotTo != tt.wantTo {
			t.Errorf("parseMutationOp(%q) = (%v, %v), want (%v, %v)",
				tt.desc, gotFrom, gotTo, tt.wantFrom, tt.wantTo)
		}
	}
}
