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
)

// --- Annotation tests ---

func TestScanAnnotations_DisableNextLine(t *testing.T) {
	code := `package p

func f() {
	// mutator-disable-next-line
	if true {
	}
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", code, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	disabled := scanAnnotations(fset, f)
	// Comment is on line 4, so line 5 should be disabled
	if !disabled[5] {
		t.Errorf("expected line 5 disabled, got disabled=%v", disabled)
	}
	if disabled[4] {
		t.Error("comment line should not be disabled")
	}
	if disabled[6] {
		t.Error("line 6 should not be disabled")
	}
}

func TestScanAnnotations_DisableFunc(t *testing.T) {
	code := `package p

// mutator-disable-func
func f() {
	if true {
	}
	x := 1
	_ = x
}

func g() {
	if true {
	}
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", code, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	disabled := scanAnnotations(fset, f)

	// All lines of f() (4-9) should be disabled
	for i := 4; i <= 9; i++ {
		if !disabled[i] {
			t.Errorf("expected line %d disabled (inside f)", i)
		}
	}
	// g() should not be disabled
	if disabled[12] {
		t.Error("g()'s line 12 should not be disabled")
	}
}

func TestScanAnnotations_NoAnnotations(t *testing.T) {
	code := `package p

func f() {
	if true {}
}
`
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "test.go", code, parser.ParseComments)
	disabled := scanAnnotations(fset, f)
	if len(disabled) != 0 {
		t.Errorf("expected empty disabled map, got %v", disabled)
	}
}

func TestScanAnnotations_IrrelevantComment(t *testing.T) {
	code := `package p

// this is just a regular comment
func f() {
	if true {}
}
`
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "test.go", code, parser.ParseComments)
	disabled := scanAnnotations(fset, f)
	if len(disabled) != 0 {
		t.Errorf("regular comments should not disable mutations, got %v", disabled)
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

	mutants, err := generateMutants(fp, fc)
	if err != nil {
		t.Fatal(err)
	}

	// The `x > 0` line is annotated — no mutants for line 5
	for _, m := range mutants {
		if m.Line == 5 {
			t.Errorf("expected no mutants on annotated line 5, got: %+v", m)
		}
	}

	// The `x < 0` line should still have mutants
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

// --- New operator tests ---

func TestIncDecMutants(t *testing.T) {
	// x++ -> x--
	incStmt := &ast.IncDecStmt{Tok: token.INC}
	m := incdecMutants("a.go", 5, incStmt)
	if len(m) != 1 {
		t.Fatalf("expected 1 mutant for ++, got %d", len(m))
	}
	if m[0].Operator != "incdec" {
		t.Errorf("operator = %q, want incdec", m[0].Operator)
	}
	if !strings.Contains(m[0].Description, "--") {
		t.Errorf("description = %q, expected it to mention --", m[0].Description)
	}

	// x-- -> x++
	decStmt := &ast.IncDecStmt{Tok: token.DEC}
	m = incdecMutants("a.go", 5, decStmt)
	if len(m) != 1 {
		t.Fatalf("expected 1 mutant for --, got %d", len(m))
	}

	// Other tokens produce nothing
	other := &ast.IncDecStmt{Tok: token.ADD}
	if ms := incdecMutants("a.go", 5, other); len(ms) != 0 {
		t.Errorf("unexpected mutants for non-incdec tok: %+v", ms)
	}
}

func TestIfBodyMutants(t *testing.T) {
	// If with body
	body := &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.Ident{Name: "x"}}}}
	ifStmt := &ast.IfStmt{Cond: &ast.Ident{Name: "cond"}, Body: body}
	m := ifBodyMutants("a.go", 5, ifStmt)
	if len(m) != 1 {
		t.Fatalf("expected 1 mutant for non-empty if body, got %d", len(m))
	}
	if m[0].Operator != "branch_removal" {
		t.Errorf("operator = %q, want branch_removal", m[0].Operator)
	}

	// If with empty body — no mutant
	empty := &ast.IfStmt{Cond: &ast.Ident{Name: "cond"}, Body: &ast.BlockStmt{}}
	if ms := ifBodyMutants("a.go", 5, empty); len(ms) != 0 {
		t.Errorf("expected no mutants for empty if body, got %d", len(ms))
	}
}

func TestExprStmtMutants_CallExpr(t *testing.T) {
	call := &ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.Ident{Name: "foo"}}}
	m := exprStmtMutants("a.go", 5, call)
	if len(m) != 1 {
		t.Fatalf("expected 1 mutant for call expr, got %d", len(m))
	}
	if m[0].Operator != "statement_deletion" {
		t.Errorf("operator = %q, want statement_deletion", m[0].Operator)
	}
}

func TestExprStmtMutants_NonCall(t *testing.T) {
	// ExprStmt wrapping a non-call (e.g., an ident) — skip
	stmt := &ast.ExprStmt{X: &ast.Ident{Name: "x"}}
	if ms := exprStmtMutants("a.go", 5, stmt); len(ms) != 0 {
		t.Errorf("expected no mutants for non-call expr, got %d", len(ms))
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

	m := &Mutant{Line: 4, Operator: "statement_deletion"}
	result := applyMutation(fp, m)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// doThing() should be removed (replaced with empty stmt)
	if strings.Contains(string(result), "doThing()") {
		t.Errorf("expected doThing() removed, got:\n%s", string(result))
	}
}

// --- Parallel execution tests ---

func TestGroupByPackage(t *testing.T) {
	mutants := []Mutant{
		{File: "a/foo.go", Line: 1},
		{File: "a/bar.go", Line: 1},
		{File: "b/baz.go", Line: 1},
	}
	groups := groupByPackage(mutants)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	total := 0
	for _, g := range groups {
		total += len(g)
	}
	if total != 3 {
		t.Errorf("expected 3 total mutants across groups, got %d", total)
	}
}

func TestGroupByPackage_SamePackage(t *testing.T) {
	mutants := []Mutant{
		{File: "pkg/a.go", Line: 1},
		{File: "pkg/a.go", Line: 5},
		{File: "pkg/b.go", Line: 1},
	}
	groups := groupByPackage(mutants)
	if len(groups) != 1 {
		t.Errorf("expected 1 group for same package, got %d", len(groups))
	}
	if len(groups[0]) != 3 {
		t.Errorf("expected 3 mutants in the single group, got %d", len(groups[0]))
	}
}

func TestGroupByPackage_PointersIntoOriginal(t *testing.T) {
	// Verify groupByPackage returns pointers into the original slice,
	// so goroutines writing m.Killed will be visible to the caller.
	mutants := []Mutant{
		{File: "pkg/a.go", Line: 1},
		{File: "pkg/a.go", Line: 2},
	}
	groups := groupByPackage(mutants)
	// Mutate via the group pointer
	groups[0][0].Killed = true
	// Original slice should see the change
	if !mutants[0].Killed {
		t.Error("expected original mutant to be updated via group pointer")
	}
}

// --- Options tests ---

func TestOptionsTimeout_Default(t *testing.T) {
	opts := Options{}
	if opts.timeout() != 30*1000*1000*1000 { // 30 seconds in ns
		t.Errorf("default timeout = %v, want 30s", opts.timeout())
	}
}

func TestBuildTestArgs_Default(t *testing.T) {
	args := buildTestArgs(Options{})
	// Should be: test -count=1 -timeout 30s ./...
	if args[0] != "test" {
		t.Errorf("args[0] = %q, want test", args[0])
	}
	// No -run flag in default case
	for _, a := range args {
		if a == "-run" {
			t.Error("did not expect -run in default args")
		}
	}
}

func TestBuildTestArgs_WithPattern(t *testing.T) {
	args := buildTestArgs(Options{TestPattern: "TestFoo"})
	found := false
	for i, a := range args {
		if a == "-run" && i+1 < len(args) && args[i+1] == "TestFoo" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected -run TestFoo in args, got %v", args)
	}
}
