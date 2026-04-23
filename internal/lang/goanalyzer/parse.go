// Package goanalyzer implements the lang.Language interface for Go. It is
// blank-imported from cmd/diffguard/main.go so that Go gets registered at
// process start.
//
// One file per concern per the top-level design doc:
//   - goanalyzer.go       -- Language + init()/Register
//   - parse.go            -- shared AST helpers (funcName, parseFile)
//   - complexity.go       -- ComplexityCalculator + ComplexityScorer
//   - sizes.go            -- FunctionExtractor
//   - deps.go             -- ImportResolver
//   - mutation_generate.go-- MutantGenerator
//   - mutation_apply.go   -- MutantApplier
//   - mutation_annotate.go-- AnnotationScanner
//   - testrunner.go       -- TestRunner (wraps go test -overlay)
package goanalyzer

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
)

// funcName returns the canonical identifier for a function or method:
//
//	func Foo()          -> "Foo"
//	func (t T) Bar()    -> "(T).Bar"
//	func (t *T) Baz()   -> "(T).Baz"
//
// This was duplicated in complexity.go, sizes.go, and churn.go before the
// language split; it now lives here as the single shared implementation.
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

// parseFile parses absPath with the given mode. Returning (nil, nil, err) on
// parse failure keeps callers uniform: the existing Go analyzers treated a
// parse error as "skip this file" rather than propagating it up, and we
// preserve that behavior behind the interface.
func parseFile(absPath string, mode parser.Mode) (*token.FileSet, *ast.File, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, absPath, nil, mode)
	if err != nil {
		return nil, nil, err
	}
	return fset, f, nil
}
