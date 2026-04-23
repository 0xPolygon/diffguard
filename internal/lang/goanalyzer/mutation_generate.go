package goanalyzer

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
)

// mutantGeneratorImpl implements lang.MutantGenerator for Go. The generation
// strategy is unchanged from the pre-split internal/mutation/generate.go —
// the only difference is that mutants are now returned as []lang.MutantSite
// so the mutation orchestrator can stay language-agnostic.
type mutantGeneratorImpl struct{}

// GenerateMutants re-parses the file (with comments so annotation scanning
// can share the same AST) and emits a MutantSite for each operator that
// applies on a changed, non-disabled line.
func (mutantGeneratorImpl) GenerateMutants(absPath string, fc diff.FileChange, disabled map[int]bool) ([]lang.MutantSite, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, absPath, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	var mutants []lang.MutantSite
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

func mutantsFor(file string, line int, n ast.Node) []lang.MutantSite {
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

// binaryMutants covers the conditional_boundary / negate_conditional /
// math_operator operators. Each source operator maps to a single canonical
// replacement; a surviving mutant should never be ambiguous about what
// "the mutation" was.
func binaryMutants(file string, line int, expr *ast.BinaryExpr) []lang.MutantSite {
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

	var mutants []lang.MutantSite
	for _, newOp := range targets {
		mutants = append(mutants, lang.MutantSite{
			File:        file,
			Line:        line,
			Description: fmt.Sprintf("%s -> %s", expr.Op, newOp),
			Operator:    operatorName(expr.Op, newOp),
		})
	}
	return mutants
}

// boolMutants generates true <-> false mutations.
func boolMutants(file string, line int, ident *ast.Ident) []lang.MutantSite {
	if ident.Name != "true" && ident.Name != "false" {
		return nil
	}
	newVal := "true"
	if ident.Name == "true" {
		newVal = "false"
	}
	return []lang.MutantSite{{
		File:        file,
		Line:        line,
		Description: fmt.Sprintf("%s -> %s", ident.Name, newVal),
		Operator:    "boolean_substitution",
	}}
}

// returnMutants generates zero-value return mutations.
//
// Returns whose every result is already the literal identifier `nil` are
// skipped: the zero-value mutation rewrites each result to `nil`, producing
// an identical AST and therefore an equivalent mutant that can never be
// killed.
func returnMutants(file string, line int, ret *ast.ReturnStmt) []lang.MutantSite {
	if len(ret.Results) == 0 {
		return nil
	}
	if allLiteralNil(ret.Results) {
		return nil
	}
	return []lang.MutantSite{{
		File:        file,
		Line:        line,
		Description: "replace return values with zero values",
		Operator:    "return_value",
	}}
}

func allLiteralNil(exprs []ast.Expr) bool {
	for _, e := range exprs {
		ident, ok := e.(*ast.Ident)
		if !ok || ident.Name != "nil" {
			return false
		}
	}
	return true
}

// incdecMutants swaps ++ with -- and vice versa.
func incdecMutants(file string, line int, stmt *ast.IncDecStmt) []lang.MutantSite {
	var newTok token.Token
	switch stmt.Tok {
	case token.INC:
		newTok = token.DEC
	case token.DEC:
		newTok = token.INC
	default:
		return nil
	}
	return []lang.MutantSite{{
		File:        file,
		Line:        line,
		Description: fmt.Sprintf("%s -> %s", stmt.Tok, newTok),
		Operator:    "incdec",
	}}
}

// ifBodyMutants empties the body of an if statement.
func ifBodyMutants(file string, line int, stmt *ast.IfStmt) []lang.MutantSite {
	if stmt.Body == nil || len(stmt.Body.List) == 0 {
		return nil
	}
	return []lang.MutantSite{{
		File:        file,
		Line:        line,
		Description: "remove if body",
		Operator:    "branch_removal",
	}}
}

// exprStmtMutants deletes a bare function-call statement (discards side effects).
func exprStmtMutants(file string, line int, stmt *ast.ExprStmt) []lang.MutantSite {
	if _, ok := stmt.X.(*ast.CallExpr); !ok {
		return nil
	}
	return []lang.MutantSite{{
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
