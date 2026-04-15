package mutation

import (
	"go/ast"
	"go/token"
	"os"
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
				t.Errorf("binaryMutants(%v) produced %d mutants, want %d", tt.op, len(mutants), tt.expected)
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
				t.Errorf("boolMutants(%q) produced %d mutants, want %d", tt.ident, len(mutants), tt.expected)
			}
		})
	}
}

func TestReturnMutants(t *testing.T) {
	// Return with values
	ret := &ast.ReturnStmt{
		Results: []ast.Expr{&ast.Ident{Name: "x"}},
	}
	mutants := returnMutants("test.go", 1, ret)
	if len(mutants) != 1 {
		t.Errorf("returnMutants with values: got %d, want 1", len(mutants))
	}

	// Bare return
	bareRet := &ast.ReturnStmt{}
	mutants = returnMutants("test.go", 1, bareRet)
	if len(mutants) != 0 {
		t.Errorf("returnMutants bare: got %d, want 0", len(mutants))
	}
}

func TestGenerateMutants(t *testing.T) {
	code := `package test

func add(a, b int) int {
	if a > b {
		return a + b
	}
	return a - b
}
`
	dir := t.TempDir()
	filePath := dir + "/test.go"
	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		t.Fatalf("writeTestFile: %v", err)
	}

	fc := diff.FileChange{
		Path: "test.go",
		Regions: []diff.ChangedRegion{
			{StartLine: 1, EndLine: 8},
		},
	}

	mutants, err := generateMutants(filePath, fc)
	if err != nil {
		t.Fatalf("generateMutants error: %v", err)
	}

	if len(mutants) == 0 {
		t.Error("expected mutants, got none")
	}

	// Should have mutations for: > (boundary), + (math), - (math)
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

func TestSampleMutants(t *testing.T) {
	mutants := make([]Mutant, 100)
	for i := range mutants {
		mutants[i] = Mutant{File: "test.go", Line: i}
	}

	sampled := sampleMutants(mutants, 50)
	if len(sampled) != 50 {
		t.Errorf("sampleMutants(100, 50%%) = %d, want 50", len(sampled))
	}

	full := sampleMutants(mutants, 100)
	if len(full) != 100 {
		t.Errorf("sampleMutants(100, 100%%) = %d, want 100", len(full))
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

func TestParseMutationOp(t *testing.T) {
	tests := []struct {
		desc     string
		expected token.Token
	}{
		{"> -> >=", token.GEQ},
		{"== -> !=", token.NEQ},
		{"+ -> -", token.SUB},
		{"invalid", token.ILLEGAL},
	}

	for _, tt := range tests {
		got := parseMutationOp(tt.desc)
		if got != tt.expected {
			t.Errorf("parseMutationOp(%q) = %v, want %v", tt.desc, got, tt.expected)
		}
	}
}
