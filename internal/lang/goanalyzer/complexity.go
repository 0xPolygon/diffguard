package goanalyzer

import (
	"go/ast"
	"go/token"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
)

// complexityImpl is the Go implementation of both lang.ComplexityCalculator
// and lang.ComplexityScorer. The scorer interface is defined separately so
// a language can ship a faster approximation; for Go the full cognitive
// score is cheap enough that one struct serves both.
type complexityImpl struct{}

// AnalyzeFile returns per-function cognitive complexity for functions whose
// line range overlaps the diff's changed regions. Parse errors return
// (nil, nil) — the old analyzer treated parse failure as "skip the file"
// and we preserve that behavior.
func (complexityImpl) AnalyzeFile(absPath string, fc diff.FileChange) ([]lang.FunctionComplexity, error) {
	fset, f, err := parseFile(absPath, 0)
	if err != nil {
		return nil, nil
	}

	var results []lang.FunctionComplexity
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}
		startLine := fset.Position(fn.Pos()).Line
		endLine := fset.Position(fn.End()).Line
		if !fc.OverlapsRange(startLine, endLine) {
			return false
		}
		results = append(results, lang.FunctionComplexity{
			FunctionInfo: lang.FunctionInfo{
				File:    fc.Path,
				Line:    startLine,
				EndLine: endLine,
				Name:    funcName(fn),
			},
			Complexity: computeCognitiveComplexity(fn.Body),
		})
		return false
	})
	return results, nil
}

// ScoreFile is the ComplexityScorer entry point used by the churn analyzer.
// It deliberately uses a simplified counter (bump by 1 for each if/for/
// switch/select/logical-op node) rather than the full cognitive complexity
// walker, matching the pre-split churn.computeComplexity. The churn score
// only needs a relative ordering of "hotter" functions; a coarse counter is
// faster to compute and keeps the churn output byte-identical to the
// pre-refactor numbers.
func (complexityImpl) ScoreFile(absPath string, fc diff.FileChange) ([]lang.FunctionComplexity, error) {
	fset, f, err := parseFile(absPath, 0)
	if err != nil {
		return nil, nil
	}

	var results []lang.FunctionComplexity
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}
		startLine := fset.Position(fn.Pos()).Line
		endLine := fset.Position(fn.End()).Line
		if !fc.OverlapsRange(startLine, endLine) {
			return false
		}
		results = append(results, lang.FunctionComplexity{
			FunctionInfo: lang.FunctionInfo{
				File:    fc.Path,
				Line:    startLine,
				EndLine: endLine,
				Name:    funcName(fn),
			},
			Complexity: computeSimpleComplexity(fn.Body),
		})
		return false
	})
	return results, nil
}

// computeSimpleComplexity is the simplified counter used by the churn
// analyzer: +1 per branching construct, +1 per && / || operator. No
// nesting penalty and no operator-change accounting. Matches the
// pre-split internal/churn.computeComplexity so churn scores stay
// byte-identical.
func computeSimpleComplexity(body *ast.BlockStmt) int {
	if body == nil {
		return 0
	}
	count := 0
	ast.Inspect(body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.IfStmt:
			count++
		case *ast.ForStmt, *ast.RangeStmt:
			count++
		case *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
			count++
		case *ast.BinaryExpr:
			if v.Op == token.LAND || v.Op == token.LOR {
				count++
			}
		}
		return true
	})
	return count
}

// computeCognitiveComplexity is the exact algorithm that lived in
// internal/complexity/complexity.go before the language split. It's moved
// here verbatim (only the receiver type changed) so byte-identical scores
// are guaranteed.
func computeCognitiveComplexity(body *ast.BlockStmt) int {
	if body == nil {
		return 0
	}
	return walkBlock(body.List, 0)
}

func walkBlock(stmts []ast.Stmt, nesting int) int {
	total := 0
	for _, stmt := range stmts {
		total += walkStmt(stmt, nesting)
	}
	return total
}

func walkStmt(stmt ast.Stmt, nesting int) int {
	switch s := stmt.(type) {
	case *ast.IfStmt:
		return walkIfStmt(s, nesting)
	case *ast.ForStmt:
		return walkForStmt(s, nesting)
	case *ast.RangeStmt:
		return 1 + nesting + walkBlock(s.Body.List, nesting+1)
	case *ast.SwitchStmt:
		return 1 + nesting + walkBlock(s.Body.List, nesting+1)
	case *ast.TypeSwitchStmt:
		return 1 + nesting + walkBlock(s.Body.List, nesting+1)
	case *ast.SelectStmt:
		return 1 + nesting + walkBlock(s.Body.List, nesting+1)
	case *ast.CaseClause:
		return walkBlock(s.Body, nesting)
	case *ast.CommClause:
		return walkBlock(s.Body, nesting)
	case *ast.BlockStmt:
		return walkBlock(s.List, nesting)
	case *ast.LabeledStmt:
		return walkStmt(s.Stmt, nesting)
	case *ast.AssignStmt:
		return walkExprsForFuncLit(s.Rhs, nesting)
	case *ast.ExprStmt:
		return walkExprForFuncLit(s.X, nesting)
	case *ast.ReturnStmt:
		return walkExprsForFuncLit(s.Results, nesting)
	case *ast.GoStmt:
		return walkExprForFuncLit(s.Call.Fun, nesting)
	case *ast.DeferStmt:
		return walkExprForFuncLit(s.Call.Fun, nesting)
	}
	return 0
}

func walkIfStmt(s *ast.IfStmt, nesting int) int {
	total := 1 + nesting
	total += countLogicalOps(s.Cond)
	if s.Init != nil {
		total += walkStmt(s.Init, nesting)
	}
	total += walkBlock(s.Body.List, nesting+1)
	if s.Else != nil {
		total += walkElseChain(s.Else, nesting)
	}
	return total
}

func walkForStmt(s *ast.ForStmt, nesting int) int {
	total := 1 + nesting
	if s.Cond != nil {
		total += countLogicalOps(s.Cond)
	}
	total += walkBlock(s.Body.List, nesting+1)
	return total
}

func walkElseChain(node ast.Node, nesting int) int {
	switch e := node.(type) {
	case *ast.IfStmt:
		total := 1
		total += countLogicalOps(e.Cond)
		if e.Init != nil {
			total += walkStmt(e.Init, nesting)
		}
		total += walkBlock(e.Body.List, nesting+1)
		if e.Else != nil {
			total += walkElseChain(e.Else, nesting)
		}
		return total
	case *ast.BlockStmt:
		return 1 + walkBlock(e.List, nesting+1)
	}
	return 0
}

func walkExprsForFuncLit(exprs []ast.Expr, nesting int) int {
	total := 0
	for _, expr := range exprs {
		total += walkExprForFuncLit(expr, nesting)
	}
	return total
}

func walkExprForFuncLit(expr ast.Expr, nesting int) int {
	total := 0
	ast.Inspect(expr, func(n ast.Node) bool {
		if fl, ok := n.(*ast.FuncLit); ok {
			total += walkBlock(fl.Body.List, nesting+1)
			return false
		}
		return true
	})
	return total
}

// countLogicalOps counts operator-type changes in a chain of && / ||.
// A run of the same operator counts as 1; each switch to the other
// operator adds 1. No logical ops at all → 0.
func countLogicalOps(expr ast.Expr) int {
	if expr == nil {
		return 0
	}
	ops := flattenLogicalOps(expr)
	if len(ops) == 0 {
		return 0
	}
	count := 1
	for i := 1; i < len(ops); i++ {
		if ops[i] != ops[i-1] {
			count++
		}
	}
	return count
}

func flattenLogicalOps(expr ast.Expr) []token.Token {
	bin, ok := expr.(*ast.BinaryExpr)
	if !ok {
		return nil
	}
	if bin.Op != token.LAND && bin.Op != token.LOR {
		return nil
	}
	var ops []token.Token
	ops = append(ops, flattenLogicalOps(bin.X)...)
	ops = append(ops, bin.Op)
	ops = append(ops, flattenLogicalOps(bin.Y)...)
	return ops
}
