package goanalyzer

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ast/astutil"

	"github.com/0xPolygon/diffguard/internal/lang"
)

// Mutant schemata: instead of compiling one program per mutant, every mutant
// of a package is compiled into a single program behind a runtime switch.
// The test binary is built once; a mutant is selected per run through an
// environment variable.
const (
	// mutantEnv selects the active mutant id; unset or 0 runs unmutated code.
	mutantEnv = "DIFFGUARD_MUTANT"
	// traceEnv names a file that receives the id of every mutation point
	// the run reaches, one per line.
	traceEnv = "DIFFGUARD_TRACE"
	// schemataFileName is the helper file added to an instrumented package.
	schemataFileName = "zz_diffguard_schemata.go"
	schemataPrefix   = "__dg"
	switchFunc       = schemataPrefix + "On"
)

// binaryHelpers maps an operator to the helper that evaluates it, or its
// mutation when the helper's mutant is active. Operands are evaluated once,
// in source order, exactly as the original expression would.
var binaryHelpers = map[token.Token]string{
	token.GTR: schemataPrefix + "Gt",
	token.GEQ: schemataPrefix + "Ge",
	token.LSS: schemataPrefix + "Lt",
	token.LEQ: schemataPrefix + "Le",
	token.ADD: schemataPrefix + "Add",
	token.SUB: schemataPrefix + "Sub",
	token.MUL: schemataPrefix + "Mul",
	token.QUO: schemataPrefix + "Quo",
}

// instrumentFile returns the source of absPath with each site guarded by
// the switch for its id, plus which sites could be embedded. Sites that
// cannot be expressed as a guarded mutation are left for the overlay path.
func instrumentFile(absPath string, sites []lang.MutantSite, ids []int) (src []byte, embedded []bool, pkgName string, err error) {
	fset, f, err := parseFile(absPath, parser.ParseComments)
	if err != nil {
		return nil, nil, "", err
	}
	// Synthesized nodes carry no positions, which lets the printer misplace
	// ordinary comments into invalid spots. Only directives affect the build.
	f.Comments = directiveComments(f.Comments)
	embedded = locateSites(fset, f, sites, func(c *astutil.Cursor, i int, fn *ast.FuncType) bool {
		return embedMutant(c, sites[i].Operator, ids[i], fn)
	})
	return renderFile(fset, f), embedded, f.Name.Name, nil
}

func directiveComments(groups []*ast.CommentGroup) []*ast.CommentGroup {
	var kept []*ast.CommentGroup
	for _, g := range groups {
		if hasDirective(g) {
			kept = append(kept, g)
		}
	}
	return kept
}

func hasDirective(g *ast.CommentGroup) bool {
	for _, c := range g.List {
		for _, prefix := range []string{"//go:", "// +build", "//export ", "//line "} {
			if strings.HasPrefix(c.Text, prefix) {
				return true
			}
		}
	}
	return false
}

func embedMutant(c *astutil.Cursor, operator string, id int, fn *ast.FuncType) bool {
	switch operator {
	case "negate_conditional":
		c.Replace(toggled(c.Node().(ast.Expr), id))
		return true
	case "conditional_boundary", "math_operator":
		return embedBinary(c, id)
	case "boolean_substitution":
		return embedBool(c, id)
	case "return_value":
		return embedReturn(c, id, fn)
	case "incdec":
		return embedIncDec(c, id)
	case "branch_removal":
		stmt := c.Node().(*ast.IfStmt)
		stmt.Body = block(&ast.IfStmt{Cond: not(switchCall(id)), Body: stmt.Body})
		return true
	case "statement_deletion":
		return embedDeletion(c, id)
	}
	return false
}

// toggled returns `((e) != __dgOn(id))`: e, negated while the mutant is
// active. The result stays an untyped boolean, so it fits wherever e did.
func toggled(e ast.Expr, id int) ast.Expr {
	return paren(&ast.BinaryExpr{X: paren(e), Op: token.NEQ, Y: switchCall(id)})
}

func embedBinary(c *astutil.Cursor, id int) bool {
	expr := c.Node().(*ast.BinaryExpr)
	if isLiteral(expr.X) && isLiteral(expr.Y) {
		// Constant arithmetic is exact; a runtime helper would not be.
		return false
	}
	c.Replace(call(binaryHelpers[expr.Op], intLit(id), expr.X, expr.Y))
	return true
}

func isLiteral(e ast.Expr) bool {
	_, ok := e.(*ast.BasicLit)
	return ok
}

func embedBool(c *astutil.Cursor, id int) bool {
	switch c.Name() {
	case "Sel", "Name", "Names", "Label":
		// An identifier slot, not an expression: `true` here is a name.
		return false
	}
	c.Replace(toggled(c.Node().(ast.Expr), id))
	return true
}

func embedReturn(c *astutil.Cursor, id int, fn *ast.FuncType) bool {
	zeros := zeroResults(fn)
	if c.Index() < 0 || len(zeros) == 0 {
		return false
	}
	c.Replace(&ast.IfStmt{
		Cond: switchCall(id),
		Body: block(&ast.ReturnStmt{Results: zeros}),
		Else: block(c.Node().(ast.Stmt)),
	})
	return true
}

func embedIncDec(c *astutil.Cursor, id int) bool {
	stmt := c.Node().(*ast.IncDecStmt)
	helper := schemataPrefix + "Inc"
	if stmt.Tok == token.DEC {
		helper = schemataPrefix + "Dec"
	}
	c.Replace(&ast.ExprStmt{X: call(helper, intLit(id), &ast.UnaryExpr{Op: token.AND, X: stmt.X})})
	return true
}

func embedDeletion(c *astutil.Cursor, id int) bool {
	if c.Index() < 0 {
		return false
	}
	c.Replace(&ast.IfStmt{Cond: not(switchCall(id)), Body: block(c.Node().(ast.Stmt))})
	return true
}

func switchCall(id int) ast.Expr { return call(switchFunc, intLit(id)) }

func call(fn string, args ...ast.Expr) *ast.CallExpr {
	return &ast.CallExpr{Fun: ast.NewIdent(fn), Args: args}
}

func intLit(n int) ast.Expr {
	return &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(n)}
}

func paren(e ast.Expr) ast.Expr { return &ast.ParenExpr{X: e} }

func not(e ast.Expr) ast.Expr { return &ast.UnaryExpr{Op: token.NOT, X: e} }

func block(stmts ...ast.Stmt) *ast.BlockStmt { return &ast.BlockStmt{List: stmts} }

// blamedMutants returns the ids of the mutants responsible for a compile
// error at line:col of instrumented source, or nil if none can be blamed.
//
// An error inside a helper call or a zeroed return belongs to that mutant.
// Any other error (typically "missing return") comes from a statement that
// was wrapped in a guard, so every guard in the enclosing function is blamed.
func blamedMutants(src []byte, line, col int) []int {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		return nil
	}
	tf := fset.File(f.Pos())
	if line < 1 || line > tf.LineCount() {
		return nil
	}
	pos := tf.LineStart(line) + token.Pos(col-1)
	path, _ := astutil.PathEnclosingInterval(f, pos, pos)
	for _, n := range path {
		if id, ok := owningMutant(n); ok {
			return []int{id}
		}
	}
	for _, n := range path {
		if fn, ok := n.(*ast.FuncDecl); ok {
			return guardIDs(fn)
		}
	}
	return nil
}

// owningMutant reports the mutant a node was synthesized for: a helper
// call, or the if statement that selects a zeroed return.
func owningMutant(n ast.Node) (int, bool) {
	if stmt, ok := n.(*ast.IfStmt); ok {
		n = stmt.Cond
	}
	c, ok := n.(*ast.CallExpr)
	if !ok || len(c.Args) == 0 {
		return 0, false
	}
	fn, ok := c.Fun.(*ast.Ident)
	lit, isLit := c.Args[0].(*ast.BasicLit)
	if !ok || !isLit || !strings.HasPrefix(fn.Name, schemataPrefix) {
		return 0, false
	}
	id, err := strconv.Atoi(lit.Value)
	return id, err == nil
}

// guardIDs returns the ids of every `!__dgOn(id)` guard in fn.
func guardIDs(fn *ast.FuncDecl) []int {
	var ids []int
	ast.Inspect(fn, func(n ast.Node) bool {
		u, ok := n.(*ast.UnaryExpr)
		if !ok || u.Op != token.NOT {
			return true
		}
		if id, ok := owningMutant(u.X); ok {
			ids = append(ids, id)
		}
		return true
	})
	return ids
}

// schemataHelperSource returns the helper file for an instrumented package.
// Imports are aliased so they cannot collide with the package's own names.
func schemataHelperSource(pkgName string) []byte {
	return []byte(fmt.Sprintf(schemataHelperTemplate, pkgName, mutantEnv, traceEnv))
}

const schemataHelperTemplate = `// Code generated by diffguard for mutation testing. DO NOT EDIT.

package %s

import (
	__dgos "os"
	__dgstrconv "strconv"
	__dgsync "sync"
)

type __dgNum interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr |
		~float32 | ~float64
}

type __dgOrd interface {
	__dgNum | ~string
}

var (
	__dgActive = func() int {
		n, _ := __dgstrconv.Atoi(__dgos.Getenv(%q))
		return n
	}()
	__dgTrace = __dgos.Getenv(%q)
	__dgMu    __dgsync.Mutex
	__dgSeen  __dgsync.Map
)

func __dgOn(k int) bool {
	if __dgTrace != "" {
		__dgRecord(k)
	}
	return __dgActive == k
}

func __dgRecord(k int) {
	if _, seen := __dgSeen.LoadOrStore(k, true); seen {
		return
	}
	__dgMu.Lock()
	defer __dgMu.Unlock()
	f, err := __dgos.OpenFile(__dgTrace, __dgos.O_APPEND|__dgos.O_CREATE|__dgos.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	f.WriteString(__dgstrconv.Itoa(k) + "\n")
	f.Close()
}

func __dgGt[T __dgOrd](k int, a, b T) bool {
	if __dgOn(k) {
		return a >= b
	}
	return a > b
}

func __dgGe[T __dgOrd](k int, a, b T) bool {
	if __dgOn(k) {
		return a > b
	}
	return a >= b
}

func __dgLt[T __dgOrd](k int, a, b T) bool {
	if __dgOn(k) {
		return a <= b
	}
	return a < b
}

func __dgLe[T __dgOrd](k int, a, b T) bool {
	if __dgOn(k) {
		return a < b
	}
	return a <= b
}

func __dgAdd[T __dgNum](k int, a, b T) T {
	if __dgOn(k) {
		return a - b
	}
	return a + b
}

func __dgSub[T __dgNum](k int, a, b T) T {
	if __dgOn(k) {
		return a + b
	}
	return a - b
}

func __dgMul[T __dgNum](k int, a, b T) T {
	if __dgOn(k) {
		return a / b
	}
	return a * b
}

func __dgQuo[T __dgNum](k int, a, b T) T {
	if __dgOn(k) {
		return a * b
	}
	return a / b
}

func __dgInc[T __dgNum](k int, p *T) {
	if __dgOn(k) {
		*p--
		return
	}
	*p++
}

func __dgDec[T __dgNum](k int, p *T) {
	if __dgOn(k) {
		*p++
		return
	}
	*p--
}
`
