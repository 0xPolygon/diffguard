package mutation

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/report"
)

func TestApplyBinaryMutation_Success(t *testing.T) {
	expr := &ast.BinaryExpr{Op: token.GTR}
	m := &Mutant{Description: "> -> >=", Operator: "conditional_boundary"}
	if !applyBinaryMutation(expr, m) {
		t.Error("expected successful apply")
	}
	if expr.Op != token.GEQ {
		t.Errorf("op = %v, want GEQ", expr.Op)
	}
}

func TestApplyBinaryMutation_WrongNodeType(t *testing.T) {
	ident := &ast.Ident{Name: "x"}
	m := &Mutant{Description: "> -> >=", Operator: "conditional_boundary"}
	if applyBinaryMutation(ident, m) {
		t.Error("expected false for non-BinaryExpr")
	}
}

func TestApplyBinaryMutation_IllegalOp(t *testing.T) {
	expr := &ast.BinaryExpr{Op: token.GTR}
	m := &Mutant{Description: "invalid", Operator: "conditional_boundary"}
	if applyBinaryMutation(expr, m) {
		t.Error("expected false for invalid description")
	}
}

// TestApplyBinaryMutation_OperatorMismatch locks in the fix for a bug where
// applyBinaryMutation rewrote the first BinaryExpr found on a line even
// when its operator differed from the mutant's intended `from` op. E.g.
// given mutant "!= -> ==", applying it to the outer `&&` of `a != nil && b`
// must NOT succeed — otherwise `&&` gets replaced and the inner `!=` stays
// untouched, producing a false-surviving mutant.
func TestApplyBinaryMutation_OperatorMismatch(t *testing.T) {
	expr := &ast.BinaryExpr{Op: token.LAND}
	m := &Mutant{Description: "!= -> ==", Operator: "negate_conditional"}
	if applyBinaryMutation(expr, m) {
		t.Error("expected false when expr.Op (&&) does not match mutant's from-op (!=)")
	}
	if expr.Op != token.LAND {
		t.Errorf("expr.Op = %v, want LAND (unchanged)", expr.Op)
	}
}

// TestApplyBinaryMutation_MathOperatorMismatch: same fix for math operators
// — `start + count - 1` parses with an outer SUB, and mutant "+ -> -" must
// not no-op on that outer SUB.
func TestApplyBinaryMutation_MathOperatorMismatch(t *testing.T) {
	expr := &ast.BinaryExpr{Op: token.SUB}
	m := &Mutant{Description: "+ -> -", Operator: "math_operator"}
	if applyBinaryMutation(expr, m) {
		t.Error("expected false when expr.Op (-) does not match mutant's from-op (+)")
	}
	if expr.Op != token.SUB {
		t.Errorf("expr.Op = %v, want SUB (unchanged)", expr.Op)
	}
}

func TestApplyBoolMutation_TrueToFalse(t *testing.T) {
	ident := &ast.Ident{Name: "true"}
	m := &Mutant{Description: "true -> false", Operator: "boolean_substitution"}
	if !applyBoolMutation(ident, m) {
		t.Error("expected successful apply")
	}
	if ident.Name != "false" {
		t.Errorf("name = %q, want false", ident.Name)
	}
}

func TestApplyBoolMutation_FalseToTrue(t *testing.T) {
	ident := &ast.Ident{Name: "false"}
	m := &Mutant{Description: "false -> true", Operator: "boolean_substitution"}
	if !applyBoolMutation(ident, m) {
		t.Error("expected successful apply")
	}
	if ident.Name != "true" {
		t.Errorf("name = %q, want true", ident.Name)
	}
}

func TestApplyBoolMutation_WrongNodeType(t *testing.T) {
	expr := &ast.BinaryExpr{Op: token.ADD}
	m := &Mutant{Description: "true -> false", Operator: "boolean_substitution"}
	if applyBoolMutation(expr, m) {
		t.Error("expected false for non-Ident")
	}
}

func TestApplyBoolMutation_NonBoolIdent(t *testing.T) {
	ident := &ast.Ident{Name: "x"}
	m := &Mutant{Description: "true -> false", Operator: "boolean_substitution"}
	if applyBoolMutation(ident, m) {
		t.Error("expected false for non-bool ident")
	}
}

func TestApplyReturnMutation_Success(t *testing.T) {
	ret := &ast.ReturnStmt{
		Results: []ast.Expr{
			&ast.Ident{Name: "x", NamePos: 1},
		},
	}
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

func TestTryApplyMutation_Binary(t *testing.T) {
	expr := &ast.BinaryExpr{Op: token.ADD}
	m := &Mutant{Description: "+ -> -", Operator: "math_operator"}
	if !tryApplyMutation(expr, m) {
		t.Error("expected successful apply for math_operator")
	}
	if expr.Op != token.SUB {
		t.Errorf("op = %v, want SUB", expr.Op)
	}
}

func TestTryApplyMutation_Bool(t *testing.T) {
	ident := &ast.Ident{Name: "true"}
	m := &Mutant{Description: "true -> false", Operator: "boolean_substitution"}
	if !tryApplyMutation(ident, m) {
		t.Error("expected successful apply for boolean_substitution")
	}
}

func TestTryApplyMutation_Return(t *testing.T) {
	ret := &ast.ReturnStmt{Results: []ast.Expr{&ast.Ident{Name: "x", NamePos: 1}}}
	m := &Mutant{Operator: "return_value"}
	if !tryApplyMutation(ret, m) {
		t.Error("expected successful apply for return_value")
	}
}

func TestTryApplyMutation_Unknown(t *testing.T) {
	ident := &ast.Ident{Name: "x"}
	m := &Mutant{Operator: "unknown_operator"}
	if tryApplyMutation(ident, m) {
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

	m := &Mutant{Line: 4, Description: "true -> false", Operator: "boolean_substitution"}
	if !applyMutationToAST(fset, f, m) {
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

	m := &Mutant{Line: 999, Description: "true -> false", Operator: "boolean_substitution"}
	if applyMutationToAST(fset, f, m) {
		t.Error("expected no mutation applied for wrong line")
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

	m := &Mutant{Line: 4, Description: "> -> >=", Operator: "conditional_boundary"}
	result := applyMutation(fp, m)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !strings.Contains(string(result), ">=") {
		t.Error("expected mutated code to contain >=")
	}
}

func TestApplyMutation_ParseError(t *testing.T) {
	m := &Mutant{Line: 1, Operator: "boolean_substitution"}
	result := applyMutation("/nonexistent/file.go", m)
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

	m := &Mutant{Line: 999, Operator: "boolean_substitution", Description: "true -> false"}
	result := applyMutation(fp, m)
	if result != nil {
		t.Error("expected nil when mutation can't be applied")
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

func TestBuildSection_HighScore(t *testing.T) {
	mutants := []Mutant{
		{File: "a.go", Line: 1, Killed: true, Operator: "negate_conditional"},
		{File: "a.go", Line: 2, Killed: true, Operator: "negate_conditional"},
		{File: "a.go", Line: 3, Killed: true, Operator: "negate_conditional"},
		{File: "a.go", Line: 4, Killed: true, Operator: "negate_conditional"},
		{File: "a.go", Line: 5, Killed: true, Operator: "negate_conditional"},
	}
	s := buildSection(mutants, 5, Options{})
	if s.Severity != report.SeverityPass {
		t.Errorf("severity = %v, want PASS (100%% kill rate)", s.Severity)
	}
}

// Low Tier-1 score fails the section because logic mutations surviving
// almost always indicate a real test gap.
func TestBuildSection_LowScore(t *testing.T) {
	mutants := []Mutant{
		{File: "a.go", Line: 1, Killed: true, Operator: "negate_conditional"},
		{File: "a.go", Line: 2, Killed: false, Description: "mut", Operator: "negate_conditional"},
		{File: "a.go", Line: 3, Killed: false, Description: "mut", Operator: "negate_conditional"},
		{File: "a.go", Line: 4, Killed: false, Description: "mut", Operator: "negate_conditional"},
		{File: "a.go", Line: 5, Killed: false, Description: "mut", Operator: "negate_conditional"},
	}
	s := buildSection(mutants, 1, Options{})
	if s.Severity != report.SeverityFail {
		t.Errorf("severity = %v, want FAIL (Tier 1 at 20%% < default 90%%)", s.Severity)
	}
	if len(s.Findings) != 4 {
		t.Errorf("findings = %d, want 4 (survived mutants)", len(s.Findings))
	}
}

// Medium Tier-2 score produces a WARN but not FAIL.
func TestBuildSection_MediumScore(t *testing.T) {
	mutants := make([]Mutant, 10)
	killed := 6 // 60% — below default Tier-2 threshold of 70.
	for i := 0; i < killed; i++ {
		mutants[i] = Mutant{File: "a.go", Line: i, Killed: true, Operator: "boolean_substitution"}
	}
	for i := killed; i < 10; i++ {
		mutants[i] = Mutant{File: "a.go", Line: i, Killed: false, Description: "mut", Operator: "boolean_substitution"}
	}
	s := buildSection(mutants, killed, Options{})
	if s.Severity != report.SeverityWarn {
		t.Errorf("severity = %v, want WARN (Tier 2 at 60%% < default 70%%)", s.Severity)
	}
}

func TestBuildSection_ZeroMutants(t *testing.T) {
	s := buildSection(nil, 0, Options{})
	// No mutants means nothing to gate on — severity should be PASS and
	// stats should still be populated.
	if s.Severity != report.SeverityPass {
		t.Errorf("severity = %v, want PASS (no mutants to gate on)", s.Severity)
	}
	if s.Stats == nil {
		t.Error("expected non-nil stats")
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

	mutants, err := generateMutants(fp, fc)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	operators := make(map[string]int)
	for _, m := range mutants {
		operators[m.Operator]++
	}

	if operators["conditional_boundary"] == 0 {
		t.Error("missing conditional_boundary mutants")
	}
	if operators["boolean_substitution"] == 0 {
		t.Error("missing boolean_substitution mutants")
	}
	if operators["math_operator"] == 0 {
		t.Error("missing math_operator mutants")
	}
	if operators["return_value"] == 0 {
		t.Error("missing return_value mutants")
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
	if !isComparison(token.NEQ) {
		t.Error("NEQ should be comparison")
	}
	if isComparison(token.GTR) {
		t.Error("GTR should not be comparison")
	}
}

func TestIsMath(t *testing.T) {
	if !isMath(token.ADD) {
		t.Error("ADD should be math")
	}
	if !isMath(token.MUL) {
		t.Error("MUL should be math")
	}
	if isMath(token.EQL) {
		t.Error("EQL should not be math")
	}
}
