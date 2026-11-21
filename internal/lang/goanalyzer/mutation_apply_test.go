package goanalyzer

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xPolygon/diffguard/internal/diff"
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

func intResults(n int) *ast.FuncType {
	fields := make([]*ast.Field, n)
	for i := range fields {
		fields[i] = &ast.Field{Type: ast.NewIdent("int")}
	}
	return &ast.FuncType{Results: &ast.FieldList{List: fields}}
}

func TestApplyReturnMutation_Success(t *testing.T) {
	ret := &ast.ReturnStmt{Results: []ast.Expr{&ast.Ident{Name: "x", NamePos: 1}}}
	if !applyReturnMutation(ret, intResults(2)) {
		t.Error("expected successful apply")
	}
	if len(ret.Results) != 2 {
		t.Fatalf("results = %d, want one per declared result (2)", len(ret.Results))
	}
	if _, ok := ret.Results[0].(*ast.StarExpr); !ok {
		t.Error("expected result replaced with *new(T)")
	}
}

func TestApplyReturnMutation_WrongNodeType(t *testing.T) {
	ident := &ast.Ident{Name: "x"}
	if applyReturnMutation(ident, intResults(1)) {
		t.Error("expected false for non-ReturnStmt")
	}
}

func TestApplyReturnMutation_NoResults(t *testing.T) {
	ret := &ast.ReturnStmt{Results: []ast.Expr{ast.NewIdent("x")}}
	if applyReturnMutation(ret, nil) || applyReturnMutation(ret, &ast.FuncType{}) {
		t.Error("expected false when the enclosing function declares no results")
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
	var buf strings.Builder
	if err := printer.Fprint(&buf, token.NewFileSet(), zeroValueExpr(ast.NewIdent("error"))); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "*new(error)" {
		t.Errorf("zero value = %q, want *new(error)", buf.String())
	}
}

func TestResultTypes_ExpandsGroupedNames(t *testing.T) {
	fn := &ast.FuncType{Results: &ast.FieldList{List: []*ast.Field{
		{Names: []*ast.Ident{ast.NewIdent("a"), ast.NewIdent("b")}, Type: ast.NewIdent("int")},
		{Type: ast.NewIdent("error")},
	}}}
	if got := len(resultTypes(fn)); got != 3 {
		t.Errorf("resultTypes = %d, want 3", got)
	}
	if resultTypes(nil) != nil || resultTypes(&ast.FuncType{}) != nil {
		t.Error("expected nil for a function without results")
	}
}

// applyAt generates the mutants of code, applies the one matching operator
// whose line is `line` and whose index among those is nth, and returns the
// mutated source.
func applyAt(t *testing.T, code, operator string, line, nth int) string {
	t.Helper()
	fp := filepath.Join(t.TempDir(), "test.go")
	if err := os.WriteFile(fp, []byte(code), 0644); err != nil {
		t.Fatal(err)
	}
	fc := diff.FileChange{Path: "test.go", Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 1000}}}
	sites, err := mutantGeneratorImpl{}.GenerateMutants(fp, fc, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sites {
		if s.Operator != operator || s.Line != line {
			continue
		}
		if nth > 0 {
			nth--
			continue
		}
		out, _ := mutantApplierImpl{}.ApplyMutation(fp, s)
		if out == nil {
			t.Fatalf("mutation %+v was not applied", s)
		}
		return string(out)
	}
	t.Fatalf("no %s mutant on line %d", operator, line)
	return ""
}

// Two sites on one line must each mutate their own node. Matching by line
// alone mutated the first one twice and never tested the second.
func TestApplyMutation_SecondSiteOnLine(t *testing.T) {
	code := "package test\n\nfunc f(a, b int) bool {\n\treturn a == 1 || b == 2\n}\n"
	first := applyAt(t, code, "negate_conditional", 4, 0)
	second := applyAt(t, code, "negate_conditional", 4, 1)
	if !strings.Contains(first, "a != 1 || b == 2") {
		t.Errorf("first site: got\n%s", first)
	}
	if !strings.Contains(second, "a == 1 || b != 2") {
		t.Errorf("second site: got\n%s", second)
	}
}

func TestApplyMutation_ChainedOperatorsAreDistinctSites(t *testing.T) {
	code := "package test\n\nfunc f(a, b, c int) int {\n\treturn a - b - c\n}\n"
	if got := applyAt(t, code, "math_operator", 4, 1); !strings.Contains(got, "a + b - c") {
		t.Errorf("inner subtraction: got\n%s", got)
	}
	if got := applyAt(t, code, "math_operator", 4, 0); !strings.Contains(got, "a - b + c") {
		t.Errorf("outer subtraction: got\n%s", got)
	}
}

// A zeroed return must compile for any result types, including when the
// original returns a multi-value call.
func TestApplyMutation_ReturnZeroValuesAreTyped(t *testing.T) {
	code := "package test\n\nfunc g() (string, error) { return \"x\", nil }\n\nfunc f() (string, error) {\n\treturn g()\n}\n"
	got := applyAt(t, code, "return_value", 6, 0)
	if !strings.Contains(got, "return *new(string), *new(error)") {
		t.Errorf("got\n%s", got)
	}
}

func TestApplyMutation_ReturnInsideFuncLitUsesItsSignature(t *testing.T) {
	code := "package test\n\nfunc f() error {\n\tg := func() int {\n\t\treturn 7\n\t}\n\t_ = g\n\treturn nil\n}\n"
	if got := applyAt(t, code, "return_value", 5, 0); !strings.Contains(got, "return *new(int)") {
		t.Errorf("got\n%s", got)
	}
}

func TestApplyMutation_StatementDeletionInCaseClause(t *testing.T) {
	code := "package test\n\nfunc f(x int) {\n\tswitch x {\n\tcase 1:\n\t\tdoThing()\n\t}\n}\n\nfunc doThing() {}\n"
	if got := applyAt(t, code, "statement_deletion", 6, 0); strings.Contains(got, "\t\tdoThing()") {
		t.Errorf("call not removed:\n%s", got)
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

// Two boolean literals on one line are separate sites; each mutant must
// flip its own literal.
func TestApplyMutation_SecondIdentifierOnLine(t *testing.T) {
	code := "package test\n\nfunc f() (bool, bool) {\n\treturn true, true\n}\n"
	if got := applyAt(t, code, "boolean_substitution", 4, 0); !strings.Contains(got, "return false, true") {
		t.Errorf("first literal: got\n%s", got)
	}
	if got := applyAt(t, code, "boolean_substitution", 4, 1); !strings.Contains(got, "return true, false") {
		t.Errorf("second literal: got\n%s", got)
	}
}
