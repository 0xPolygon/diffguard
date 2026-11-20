package goanalyzer

import (
	"go/ast"
	"go/token"
	"strings"

	"golang.org/x/tools/go/ast/astutil"

	"github.com/0xPolygon/diffguard/internal/lang"
)

// anchorPos is the position that identifies a node as a mutation site. A
// binary expression is anchored at its operator: `a - b - c` holds two
// subtractions that start at the same position but have distinct operators.
func anchorPos(n ast.Node) token.Pos {
	if b, ok := n.(*ast.BinaryExpr); ok {
		return b.OpPos
	}
	return n.Pos()
}

// siteVisitor mutates the node under the cursor for sites[site] and reports
// whether it did. fn is the innermost enclosing function's type, or nil.
type siteVisitor func(c *astutil.Cursor, site int, fn *ast.FuncType) bool

// siteLocator walks a file and hands each mutation site's node to a visitor.
type siteLocator struct {
	fset  *token.FileSet
	sites []lang.MutantSite
	// byOffset and byLine index the sites by where their node is anchored:
	// sites carrying an offset match exactly, sites without one match the
	// first fitting node on their line.
	byOffset map[int][]int
	byLine   map[int][]int
	applied  []bool
	funcs    []*ast.FuncType
	visit    siteVisitor
}

// locateSites visits the node of every site in f, innermost nodes first, and
// reports which sites the visitor applied. Each site is applied at most once.
func locateSites(fset *token.FileSet, f *ast.File, sites []lang.MutantSite, visit siteVisitor) []bool {
	l := &siteLocator{
		fset:     fset,
		sites:    sites,
		byOffset: map[int][]int{},
		byLine:   map[int][]int{},
		applied:  make([]bool, len(sites)),
		visit:    visit,
	}
	for i, site := range sites {
		if site.Offset != 0 {
			l.byOffset[site.Offset] = append(l.byOffset[site.Offset], i)
		} else {
			l.byLine[site.Line] = append(l.byLine[site.Line], i)
		}
	}
	astutil.Apply(f, l.pre, l.post)
	return l.applied
}

func (l *siteLocator) pre(c *astutil.Cursor) bool {
	if fn := funcTypeOf(c.Node()); fn != nil {
		l.funcs = append(l.funcs, fn)
	}
	return true
}

func (l *siteLocator) post(c *astutil.Cursor) bool {
	n := c.Node()
	if n == nil {
		return true
	}
	l.visitSites(c, n, l.byOffset[l.fset.Position(anchorPos(n)).Offset])
	l.visitSites(c, n, l.byLine[l.fset.Position(n.Pos()).Line])
	if funcTypeOf(n) != nil {
		l.funcs = l.funcs[:len(l.funcs)-1]
	}
	return true
}

// visitSites offers n to each candidate site that has not been applied yet
// and whose operator mutates this kind of node.
func (l *siteLocator) visitSites(c *astutil.Cursor, n ast.Node, candidates []int) {
	for _, i := range candidates {
		if !l.applied[i] && operatorFits(n, l.sites[i]) {
			l.applied[i] = l.visit(c, i, l.enclosingFunc())
		}
	}
}

func (l *siteLocator) enclosingFunc() *ast.FuncType {
	if len(l.funcs) == 0 {
		return nil
	}
	return l.funcs[len(l.funcs)-1]
}

func funcTypeOf(n ast.Node) *ast.FuncType {
	switch fn := n.(type) {
	case *ast.FuncDecl:
		return fn.Type
	case *ast.FuncLit:
		return fn.Type
	}
	return nil
}

// operatorFits reports whether n is the kind of node site's operator
// mutates. For binary operators the source operator must match too.
func operatorFits(n ast.Node, site lang.MutantSite) bool {
	switch node := n.(type) {
	case *ast.BinaryExpr:
		from, to := parseMutationOp(site.Description)
		return to != token.ILLEGAL && node.Op == from && isBinaryOperator(site.Operator)
	case *ast.Ident:
		return site.Operator == "boolean_substitution" && strings.HasPrefix(site.Description, node.Name+" ->")
	case *ast.ReturnStmt:
		return site.Operator == "return_value"
	case *ast.IncDecStmt:
		return site.Operator == "incdec"
	case *ast.IfStmt:
		return site.Operator == "branch_removal" && node.Body != nil
	case *ast.ExprStmt:
		_, isCall := node.X.(*ast.CallExpr)
		return site.Operator == "statement_deletion" && isCall
	}
	return false
}

func isBinaryOperator(op string) bool {
	return op == "conditional_boundary" || op == "negate_conditional" || op == "math_operator"
}

// resultTypes returns one type expression per result of fn, expanding
// grouped names (`(a, b int)` yields int twice).
func resultTypes(fn *ast.FuncType) []ast.Expr {
	if fn == nil || fn.Results == nil {
		return nil
	}
	var types []ast.Expr
	for _, field := range fn.Results.List {
		n := len(field.Names)
		if n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			types = append(types, field.Type)
		}
	}
	return types
}

// zeroValueExpr returns `*new(T)`, an expression that is the zero value of
// any type T without needing type information.
func zeroValueExpr(typ ast.Expr) ast.Expr {
	return &ast.StarExpr{X: &ast.CallExpr{Fun: ast.NewIdent("new"), Args: []ast.Expr{typ}}}
}

// zeroResults returns the zero-value results for a return in fn, or nil if
// fn has no results to zero.
func zeroResults(fn *ast.FuncType) []ast.Expr {
	types := resultTypes(fn)
	zeros := make([]ast.Expr, len(types))
	for i, t := range types {
		zeros[i] = zeroValueExpr(t)
	}
	return zeros
}
