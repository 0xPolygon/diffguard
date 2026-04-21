package goanalyzer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// Most of these tests are imported verbatim from the pre-split
// internal/complexity package. They exercise the walker directly (rather
// than going through AnalyzeFile + a tempdir file) so failures localize to
// the exact construct that broke.

func TestComputeComplexity(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		expected int
	}{
		{"empty function", `package p; func f() {}`, 0},
		{"single if", `package p; func f(x int) { if x > 0 {} }`, 1},
		{"if-else", `package p; func f(x int) { if x > 0 {} else {} }`, 2},
		{"if-else if-else", `package p; func f(x int) { if x > 0 {} else if x < 0 {} else {} }`, 3},
		{"nested if", `package p; func f(x, y int) { if x > 0 { if y > 0 {} } }`, 3},
		{"for loop", `package p; func f() { for i := 0; i < 10; i++ {} }`, 1},
		{"nested for", `package p; func f() { for i := 0; i < 10; i++ { for j := 0; j < 10; j++ {} } }`, 3},
		{"switch with cases", `package p; func f(x int) { switch x { case 1: case 2: case 3: } }`, 1},
		{"logical operators same type", `package p; func f(a, b, c bool) { if a && b && c {} }`, 2},
		{"logical operators mixed", `package p; func f(a, b, c bool) { if a && b || c {} }`, 3},
		{"range loop", `package p; func f(s []int) { for range s {} }`, 1},
		{"select statement", `package p; func f(c chan int) { select { case <-c: } }`, 1},
		{"deeply nested", `package p
func f(x, y, z int) {
	if x > 0 {
		for y > 0 {
			if z > 0 {
			}
		}
	}
}`, 6},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := parseFuncBody(t, tt.code)
			if got := computeCognitiveComplexity(body); got != tt.expected {
				t.Errorf("complexity = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestComputeComplexity_NilBody(t *testing.T) {
	if got := computeCognitiveComplexity(nil); got != 0 {
		t.Errorf("computeCognitiveComplexity(nil) = %d, want 0", got)
	}
}

func TestWalkStmt_NestingPenalty(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		expected int
	}{
		{"range at nesting 1", `package p; func f(x int) {
			if x > 0 {
				for range []int{} {
					if x > 0 {}
				}
			}
		}`, 6},
		{"switch at nesting 1", `package p; func f(x int) {
			if x > 0 {
				switch x {
				case 1:
					if x > 0 {}
				}
			}
		}`, 6},
		{"select at nesting 1", `package p; func f(x int, c chan int) {
			if x > 0 {
				select {
				case <-c:
					if x > 0 {}
				}
			}
		}`, 6},
		{"type switch at nesting 1", `package p; func f(x int, v any) {
			if x > 0 {
				switch v.(type) {
				case int:
					if x > 0 {}
				}
			}
		}`, 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := parseFuncBody(t, tt.code)
			if got := computeCognitiveComplexity(body); got != tt.expected {
				t.Errorf("complexity = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestWalkForStmt_WithLogicalCondition(t *testing.T) {
	body := parseFuncBody(t, `package p; func f(a, b bool) { for a && b {} }`)
	if got := computeCognitiveComplexity(body); got != 2 {
		t.Errorf("complexity = %d, want 2", got)
	}
}

func TestWalkIfStmt_WithElseChain(t *testing.T) {
	body := parseFuncBody(t, `package p
func f(x int) {
	if x > 0 {
	} else if x < 0 {
	} else {
	}
}`)
	if got := computeCognitiveComplexity(body); got != 3 {
		t.Errorf("complexity = %d, want 3", got)
	}
}

func TestWalkIfStmt_WithInit(t *testing.T) {
	body := parseFuncBody(t, `package p
func f() error {
	if err := g(); err != nil {
	}
	return nil
}
func g() error { return nil }`)
	if got := computeCognitiveComplexity(body); got != 1 {
		t.Errorf("complexity = %d, want 1", got)
	}
}

func TestWalkStmt_LabeledStmt(t *testing.T) {
	body := parseFuncBody(t, `package p
func f(x int) {
outer:
	for x > 0 {
		_ = x
		break outer
	}
}`)
	if got := computeCognitiveComplexity(body); got != 1 {
		t.Errorf("complexity = %d, want 1", got)
	}
}

func TestWalkStmt_GoAndDefer(t *testing.T) {
	body := parseFuncBody(t, `package p
func f() {
	go func() {
		if true {}
	}()
	defer func() {
		if true {}
	}()
}`)
	if got := computeCognitiveComplexity(body); got != 4 {
		t.Errorf("complexity = %d, want 4", got)
	}
}

func TestWalkStmt_FuncLitInAssign(t *testing.T) {
	body := parseFuncBody(t, `package p
func f() {
	x := func() {
		if true {}
	}
	_ = x
}`)
	if got := computeCognitiveComplexity(body); got != 2 {
		t.Errorf("complexity = %d, want 2", got)
	}
}

func TestWalkStmt_FuncLitInReturn(t *testing.T) {
	body := parseFuncBody(t, `package p
func f() func() {
	return func() {
		if true {}
	}
}`)
	if got := computeCognitiveComplexity(body); got != 2 {
		t.Errorf("complexity = %d, want 2", got)
	}
}

func TestCountLogicalOps(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		expected int
	}{
		{"no logical ops", `package p; var x = 1 + 2`, 0},
		{"single and", `package p; var x = true && false`, 1},
		{"chain same op", `package p; var x = true && false && true`, 1},
		{"mixed ops", `package p; var x = true && false || true`, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "test.go", tt.code, 0)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			var expr ast.Expr
			ast.Inspect(f, func(n ast.Node) bool {
				if vs, ok := n.(*ast.ValueSpec); ok && len(vs.Values) > 0 {
					expr = vs.Values[0]
					return false
				}
				return true
			})
			if got := countLogicalOps(expr); got != tt.expected {
				t.Errorf("countLogicalOps = %d, want %d", got, tt.expected)
			}
		})
	}
}

// parseFuncBody parses code and returns the body of the first FuncDecl.
// All the walker tests use this rather than open-coding the parse loop.
func parseFuncBody(t *testing.T, code string) *ast.BlockStmt {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", code, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok {
			return fd.Body
		}
	}
	t.Fatal("no function found")
	return nil
}
