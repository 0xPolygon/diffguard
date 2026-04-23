package goanalyzer

import (
	"go/ast"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
)

// sizesImpl implements lang.FunctionExtractor for Go by parsing the file
// and reporting function line ranges plus the overall file line count.
type sizesImpl struct{}

// ExtractFunctions parses absPath and returns (functions-in-changed-regions,
// file size, error). Parse errors return (nil, nil, nil) to match the
// pre-refactor behavior where parse failure silently skipped the file.
func (sizesImpl) ExtractFunctions(absPath string, fc diff.FileChange) ([]lang.FunctionSize, *lang.FileSize, error) {
	fset, f, err := parseFile(absPath, 0)
	if err != nil {
		return nil, nil, nil
	}

	var fileSize *lang.FileSize
	if file := fset.File(f.Pos()); file != nil {
		fileSize = &lang.FileSize{Path: fc.Path, Lines: file.LineCount()}
	}

	var results []lang.FunctionSize
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
		results = append(results, lang.FunctionSize{
			FunctionInfo: lang.FunctionInfo{
				File:    fc.Path,
				Line:    startLine,
				EndLine: endLine,
				Name:    funcName(fn),
			},
			Lines: endLine - startLine + 1,
		})
		return false
	})
	return results, fileSize, nil
}
