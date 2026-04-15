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
	newOp := parseMutationOp(m.Description)
	if newOp == token.ILLEGAL {
		return false
	}
	expr.Op = newOp
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

func parseMutationOp(desc string) token.Token {
	parts := strings.Split(desc, " -> ")
	if len(parts) != 2 {
		return token.ILLEGAL
	}

	opMap := map[string]token.Token{
		">": token.GTR, ">=": token.GEQ,
		"<": token.LSS, "<=": token.LEQ,
		"==": token.EQL, "!=": token.NEQ,
		"+": token.ADD, "-": token.SUB,
		"*": token.MUL, "/": token.QUO,
	}

	if op, ok := opMap[parts[1]]; ok {
		return op
	}
	return token.ILLEGAL
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
