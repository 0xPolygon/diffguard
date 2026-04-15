package complexity

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestComputeComplexity(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		expected int
	}{
		{
			name:     "empty function",
			code:     `package p; func f() {}`,
			expected: 0,
		},
		{
			name:     "single if",
			code:     `package p; func f(x int) { if x > 0 {} }`,
			expected: 1,
		},
		{
			name:     "if-else",
			code:     `package p; func f(x int) { if x > 0 {} else {} }`,
			expected: 2, // +1 if, +1 else
		},
		{
			name:     "if-else if-else",
			code:     `package p; func f(x int) { if x > 0 {} else if x < 0 {} else {} }`,
			expected: 3, // +1 if, +1 else if, +1 else
		},
		{
			name:     "nested if",
			code:     `package p; func f(x, y int) { if x > 0 { if y > 0 {} } }`,
			expected: 3, // +1 outer if (nesting=0), +1 inner if + 1 nesting penalty
		},
		{
			name:     "for loop",
			code:     `package p; func f() { for i := 0; i < 10; i++ {} }`,
			expected: 1,
		},
		{
			name:     "nested for",
			code:     `package p; func f() { for i := 0; i < 10; i++ { for j := 0; j < 10; j++ {} } }`,
			expected: 3, // +1 outer for, +1 inner for + 1 nesting
		},
		{
			name:     "switch with cases",
			code:     `package p; func f(x int) { switch x { case 1: case 2: case 3: } }`,
			expected: 1, // +1 for switch, cases don't add complexity
		},
		{
			name:     "logical operators same type",
			code:     `package p; func f(a, b, c bool) { if a && b && c {} }`,
			expected: 2, // +1 if, +1 for &&-sequence (same operator = 1)
		},
		{
			name:     "logical operators mixed",
			code:     `package p; func f(a, b, c bool) { if a && b || c {} }`,
			expected: 3, // +1 if, +2 for mixed && then ||
		},
		{
			name:     "range loop",
			code:     `package p; func f(s []int) { for range s {} }`,
			expected: 1,
		},
		{
			name:     "select statement",
			code:     `package p; func f(c chan int) { select { case <-c: } }`,
			expected: 1,
		},
		{
			name:     "deeply nested",
			code: `package p
func f(x, y, z int) {
	if x > 0 {          // +1 (nesting=0)
		for y > 0 {      // +1 +1 nesting (nesting=1)
			if z > 0 {   // +1 +2 nesting (nesting=2)
			}
		}
	}
}`,
			expected: 6, // 1 + 2 + 3
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
			if fn == nil {
				t.Fatal("no function found")
			}

			got := computeComplexity(fn.Body)
			if got != tt.expected {
				t.Errorf("complexity = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestFuncName(t *testing.T) {
	tests := []struct {
		code     string
		expected string
	}{
		{
			code:     `package p; func Foo() {}`,
			expected: "Foo",
		},
		{
			code:     `package p; type T struct{}; func (t T) Foo() {}`,
			expected: "(T).Foo",
		},
		{
			code:     `package p; type T struct{}; func (t *T) Foo() {}`,
			expected: "(T).Foo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "test.go", tt.code, 0)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}

			for _, decl := range f.Decls {
				if fd, ok := decl.(*ast.FuncDecl); ok {
					got := funcName(fd)
					if got != tt.expected {
						t.Errorf("funcName = %q, want %q", got, tt.expected)
					}
					return
				}
			}
			t.Fatal("no function found")
		})
	}
}

func TestCountLogicalOps(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		expected int
	}{
		{
			name:     "no logical ops",
			code:     `package p; var x = 1 + 2`,
			expected: 0,
		},
		{
			name:     "single and",
			code:     `package p; var x = true && false`,
			expected: 1,
		},
		{
			name:     "chain same op",
			code:     `package p; var x = true && false && true`,
			expected: 1, // same operator sequence counts as 1
		},
		{
			name:     "mixed ops",
			code:     `package p; var x = true && false || true`,
			expected: 2, // switch from && to ||
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "test.go", tt.code, 0)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}

			// Find the expression in the var declaration
			var expr ast.Expr
			ast.Inspect(f, func(n ast.Node) bool {
				if vs, ok := n.(*ast.ValueSpec); ok && len(vs.Values) > 0 {
					expr = vs.Values[0]
					return false
				}
				return true
			})
			if expr == nil {
				t.Fatal("no expression found")
			}

			got := countLogicalOps(expr)
			if got != tt.expected {
				t.Errorf("countLogicalOps = %d, want %d", got, tt.expected)
			}
		})
	}
}
