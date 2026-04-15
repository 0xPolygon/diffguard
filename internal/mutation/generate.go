package mutation

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"

	"github.com/0xPolygon/diffguard/internal/diff"
)

// generateMutants parses a file and creates mutants for changed regions.
// Lines disabled via mutator-disable-* annotations are skipped.
func generateMutants(absPath string, fc diff.FileChange) ([]Mutant, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, absPath, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	disabled := scanAnnotations(fset, f)
	var mutants []Mutant

	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			return true
		}
		line := fset.Position(n.Pos()).Line
		if !fc.ContainsLine(line) || disabled[line] {
			return true
		}
		mutants = append(mutants, mutantsFor(fc.Path, line, n)...)
		return true
	})

	return mutants, nil
}

func mutantsFor(file string, line int, n ast.Node) []Mutant {
	switch node := n.(type) {
	case *ast.BinaryExpr:
		return binaryMutants(file, line, node)
	case *ast.Ident:
		return boolMutants(file, line, node)
	case *ast.ReturnStmt:
		return returnMutants(file, line, node)
	case *ast.IncDecStmt:
		return incdecMutants(file, line, node)
	case *ast.IfStmt:
		return ifBodyMutants(file, line, node)
	case *ast.ExprStmt:
		return exprStmtMutants(file, line, node)
	}
	return nil
}

// binaryMutants generates mutations for binary expressions.
func binaryMutants(file string, line int, expr *ast.BinaryExpr) []Mutant {
	replacements := map[token.Token][]token.Token{
		token.GTR: {token.GEQ},
		token.LSS: {token.LEQ},
		token.GEQ: {token.GTR},
		token.LEQ: {token.LSS},
		token.EQL: {token.NEQ},
		token.NEQ: {token.EQL},
		token.ADD: {token.SUB},
		token.SUB: {token.ADD},
		token.MUL: {token.QUO},
		token.QUO: {token.MUL},
	}

	targets, ok := replacements[expr.Op]
	if !ok {
		return nil
	}

	var mutants []Mutant
	for _, newOp := range targets {
		mutants = append(mutants, Mutant{
			File:        file,
			Line:        line,
			Description: fmt.Sprintf("%s -> %s", expr.Op, newOp),
			Operator:    operatorName(expr.Op, newOp),
		})
	}

	return mutants
}

// boolMutants generates true <-> false mutations.
func boolMutants(file string, line int, ident *ast.Ident) []Mutant {
	if ident.Name != "true" && ident.Name != "false" {
		return nil
	}

	newVal := "true"
	if ident.Name == "true" {
		newVal = "false"
	}

	return []Mutant{{
		File:        file,
		Line:        line,
		Description: fmt.Sprintf("%s -> %s", ident.Name, newVal),
		Operator:    "boolean_substitution",
	}}
}

// returnMutants generates zero-value return mutations.
func returnMutants(file string, line int, ret *ast.ReturnStmt) []Mutant {
	if len(ret.Results) == 0 {
		return nil
	}

	return []Mutant{{
		File:        file,
		Line:        line,
		Description: "replace return values with zero values",
		Operator:    "return_value",
	}}
}

// incdecMutants swaps ++ with -- and vice versa.
func incdecMutants(file string, line int, stmt *ast.IncDecStmt) []Mutant {
	var newTok token.Token
	switch stmt.Tok {
	case token.INC:
		newTok = token.DEC
	case token.DEC:
		newTok = token.INC
	default:
		return nil
	}
	return []Mutant{{
		File:        file,
		Line:        line,
		Description: fmt.Sprintf("%s -> %s", stmt.Tok, newTok),
		Operator:    "incdec",
	}}
}

// ifBodyMutants empties the body of an if statement.
func ifBodyMutants(file string, line int, stmt *ast.IfStmt) []Mutant {
	if stmt.Body == nil || len(stmt.Body.List) == 0 {
		return nil
	}
	return []Mutant{{
		File:        file,
		Line:        line,
		Description: "remove if body",
		Operator:    "branch_removal",
	}}
}

// exprStmtMutants deletes a bare function-call statement (discards side effects).
func exprStmtMutants(file string, line int, stmt *ast.ExprStmt) []Mutant {
	if _, ok := stmt.X.(*ast.CallExpr); !ok {
		return nil
	}
	return []Mutant{{
		File:        file,
		Line:        line,
		Description: "remove call statement",
		Operator:    "statement_deletion",
	}}
}

func operatorName(from, to token.Token) string {
	switch {
	case isBoundary(from) || isBoundary(to):
		return "conditional_boundary"
	case isComparison(from) || isComparison(to):
		return "negate_conditional"
	case isMath(from) || isMath(to):
		return "math_operator"
	default:
		return "unknown"
	}
}

func isBoundary(t token.Token) bool {
	return t == token.GTR || t == token.GEQ || t == token.LSS || t == token.LEQ
}

func isComparison(t token.Token) bool {
	return t == token.EQL || t == token.NEQ
}

func isMath(t token.Token) bool {
	return t == token.ADD || t == token.SUB || t == token.MUL || t == token.QUO
}
