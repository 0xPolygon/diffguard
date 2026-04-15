package complexity

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"path/filepath"
	"sort"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/report"
)

// FunctionComplexity holds the complexity result for a single function.
type FunctionComplexity struct {
	File       string
	Line       int
	Name       string
	Complexity int
}

// Analyze computes cognitive complexity for all functions in changed regions of the diff.
func Analyze(repoPath string, d *diff.Result, threshold int) (report.Section, error) {
	var results []FunctionComplexity

	for _, fc := range d.Files {
		results = append(results, analyzeFile(repoPath, fc)...)
	}

	return buildSection(results, threshold), nil
}

func analyzeFile(repoPath string, fc diff.FileChange) []FunctionComplexity {
	absPath := filepath.Join(repoPath, fc.Path)
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, absPath, nil, 0)
	if err != nil {
		return nil
	}

	var results []FunctionComplexity
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

		results = append(results, FunctionComplexity{
			File:       fc.Path,
			Line:       startLine,
			Name:       funcName(fn),
			Complexity: computeComplexity(fn.Body),
		})

		return false
	})
	return results
}

// computeComplexity calculates cognitive complexity of a function body.
func computeComplexity(body *ast.BlockStmt) int {
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

// countLogicalOps counts sequences of && and || in an expression.
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

func funcName(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		recv := fn.Recv.List[0]
		var typeName string
		switch t := recv.Type.(type) {
		case *ast.StarExpr:
			if ident, ok := t.X.(*ast.Ident); ok {
				typeName = ident.Name
			}
		case *ast.Ident:
			typeName = t.Name
		}
		return fmt.Sprintf("(%s).%s", typeName, fn.Name.Name)
	}
	return fn.Name.Name
}

func collectComplexityFindings(results []FunctionComplexity, threshold int) ([]report.Finding, []float64, int) {
	var findings []report.Finding
	var values []float64
	failCount := 0

	for _, r := range results {
		values = append(values, float64(r.Complexity))
		if r.Complexity <= threshold {
			continue
		}
		failCount++
		findings = append(findings, report.Finding{
			File:     r.File,
			Line:     r.Line,
			Function: r.Name,
			Message:  fmt.Sprintf("complexity=%d", r.Complexity),
			Value:    float64(r.Complexity),
			Limit:    float64(threshold),
			Severity: report.SeverityFail,
		})
	}

	sort.Slice(findings, func(i, j int) bool {
		return findings[i].Value > findings[j].Value
	})

	return findings, values, failCount
}

func buildSection(results []FunctionComplexity, threshold int) report.Section {
	if len(results) == 0 {
		return report.Section{
			Name:     "Cognitive Complexity",
			Summary:  "No changed functions to analyze",
			Severity: report.SeverityPass,
		}
	}

	findings, values, failCount := collectComplexityFindings(results, threshold)

	sev := report.SeverityPass
	if failCount > 0 {
		sev = report.SeverityFail
	}

	m, med := mean(values), median(values)
	summary := fmt.Sprintf("%d functions analyzed | Mean: %.1f | Median: %.0f | Max: %.0f | %d over threshold (%d)",
		len(results), m, med, max(values), failCount, threshold)

	return report.Section{
		Name:     "Cognitive Complexity",
		Summary:  summary,
		Severity: sev,
		Findings: findings,
		Stats: map[string]any{
			"total_functions": len(results),
			"mean":            math.Round(m*10) / 10,
			"median":          med,
			"max":             max(values),
			"violations":      failCount,
			"threshold":       threshold,
			"histogram":       report.Histogram(values, []float64{5, 10, 15, 20}),
		},
	}
}

func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func median(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := make([]float64, len(vals))
	copy(sorted, vals)
	sort.Float64s(sorted)
	n := len(sorted)
	if n%2 == 0 {
		return (sorted[n/2-1] + sorted[n/2]) / 2
	}
	return sorted[n/2]
}

func max(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	m := vals[0]
	for _, v := range vals[1:] {
		if v > m {
			m = v
		}
	}
	return m
}
