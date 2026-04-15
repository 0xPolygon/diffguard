package mutation

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strings"
)

// applyMutation re-parses the file and applies the specific mutation.
func applyMutation(absPath string, m *Mutant) []byte {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, absPath, nil, parser.ParseComments)
	if err != nil {
		return nil
	}

	var applied bool
	if m.Operator == "statement_deletion" {
		applied = applyStatementDeletion(fset, f, m)
	} else {
		applied = applyMutationToAST(fset, f, m)
	}

	if !applied {
		return nil
	}
	return renderFile(fset, f)
}

func applyMutationToAST(fset *token.FileSet, f *ast.File, m *Mutant) bool {
	applied := false
	ast.Inspect(f, func(n ast.Node) bool {
		if applied || n == nil {
			return false
		}
		if fset.Position(n.Pos()).Line != m.Line {
			return true
		}
		applied = tryApplyMutation(n, m)
		return !applied
	})
	return applied
}

// applyStatementDeletion needs the containing block to replace a statement,
// so it walks BlockStmts instead of the flat ast.Inspect used for other ops.
func applyStatementDeletion(fset *token.FileSet, f *ast.File, m *Mutant) bool {
	applied := false
	ast.Inspect(f, func(n ast.Node) bool {
		if applied {
			return false
		}
		block, ok := n.(*ast.BlockStmt)
		if !ok {
			return true
		}
		if tryDeleteInBlock(fset, block, m) {
			applied = true
			return false
		}
		return true
	})
	return applied
}

func tryDeleteInBlock(fset *token.FileSet, block *ast.BlockStmt, m *Mutant) bool {
	for i, stmt := range block.List {
		if fset.Position(stmt.Pos()).Line != m.Line {
			continue
		}
		if _, ok := stmt.(*ast.ExprStmt); !ok {
			continue
		}
		block.List[i] = &ast.EmptyStmt{Semicolon: stmt.Pos()}
		return true
	}
	return false
}

func tryApplyMutation(n ast.Node, m *Mutant) bool {
	switch m.Operator {
	case "conditional_boundary", "negate_conditional", "math_operator":
		return applyBinaryMutation(n, m)
	case "boolean_substitution":
		return applyBoolMutation(n, m)
	case "return_value":
		return applyReturnMutation(n)
	case "incdec":
		return applyIncDecMutation(n)
	case "branch_removal":
		return applyBranchRemoval(n)
	}
	return false
}

func applyBinaryMutation(n ast.Node, m *Mutant) bool {
	expr, ok := n.(*ast.BinaryExpr)
	if !ok {
		return false
	}
	// Verify the operator matches the mutant description. Without this
	// check, the walker would rewrite the first BinaryExpr it finds on
	// the line — e.g. the outer `&&` in `a != nil && b`, or the outer
	// `-` in `a + b - 1` — producing a no-op instead of the intended
	// mutation and leaving a false-surviving mutant.
	from, to := parseMutationOp(m.Description)
	if to == token.ILLEGAL || expr.Op != from {
		return false
	}
	expr.Op = to
	return true
}

func applyBoolMutation(n ast.Node, m *Mutant) bool {
	ident, ok := n.(*ast.Ident)
	if !ok || (ident.Name != "true" && ident.Name != "false") {
		return false
	}
	if strings.Contains(m.Description, "-> true") {
		ident.Name = "true"
	} else {
		ident.Name = "false"
	}
	return true
}

func applyReturnMutation(n ast.Node) bool {
	ret, ok := n.(*ast.ReturnStmt)
	if !ok {
		return false
	}
	for i := range ret.Results {
		ret.Results[i] = zeroValueExpr(ret.Results[i])
	}
	return true
}

func applyIncDecMutation(n ast.Node) bool {
	stmt, ok := n.(*ast.IncDecStmt)
	if !ok {
		return false
	}
	switch stmt.Tok {
	case token.INC:
		stmt.Tok = token.DEC
	case token.DEC:
		stmt.Tok = token.INC
	default:
		return false
	}
	return true
}

func applyBranchRemoval(n ast.Node) bool {
	stmt, ok := n.(*ast.IfStmt)
	if !ok || stmt.Body == nil {
		return false
	}
	stmt.Body.List = nil
	return true
}

// parseMutationOp parses a mutant description of the form "X -> Y" into
// the (from, to) operator pair. Either token is ILLEGAL if parsing fails.
func parseMutationOp(desc string) (from, to token.Token) {
	parts := strings.Split(desc, " -> ")
	if len(parts) != 2 {
		return token.ILLEGAL, token.ILLEGAL
	}

	opMap := map[string]token.Token{
		">": token.GTR, ">=": token.GEQ,
		"<": token.LSS, "<=": token.LEQ,
		"==": token.EQL, "!=": token.NEQ,
		"+": token.ADD, "-": token.SUB,
		"*": token.MUL, "/": token.QUO,
	}

	fromOp, okFrom := opMap[parts[0]]
	toOp, okTo := opMap[parts[1]]
	if !okFrom || !okTo {
		return token.ILLEGAL, token.ILLEGAL
	}
	return fromOp, toOp
}

func zeroValueExpr(expr ast.Expr) ast.Expr {
	return &ast.Ident{Name: "nil", NamePos: expr.Pos()}
}

func renderFile(fset *token.FileSet, f *ast.File) []byte {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, f); err != nil {
		return nil
	}
	return buf.Bytes()
}
