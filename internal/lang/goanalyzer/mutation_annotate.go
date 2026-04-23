package goanalyzer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// annotationScannerImpl implements lang.AnnotationScanner for Go.
// The disable annotations are `// mutator-disable-next-line` (skips the
// following source line) and `// mutator-disable-func` (skips every line of
// the enclosing function, including its signature). Both forms are stripped
// of their comment markers before matching so either `//` or `/* ... */` is
// accepted.
type annotationScannerImpl struct{}

// ScanAnnotations returns the set of source lines on which mutation
// generation should be suppressed for absPath. The returned map is keyed by
// 1-based line number; a `true` value means disabled.
func (annotationScannerImpl) ScanAnnotations(absPath string) (map[int]bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, absPath, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	disabled := make(map[int]bool)
	funcs := funcRanges(fset, f)
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			applyAnnotation(stripCommentMarkers(c.Text), fset.Position(c.Pos()).Line, funcs, disabled)
		}
	}
	return disabled, nil
}

func stripCommentMarkers(raw string) string {
	s := strings.TrimSpace(strings.TrimPrefix(raw, "//"))
	s = strings.TrimSpace(strings.TrimPrefix(s, "/*"))
	s = strings.TrimSpace(strings.TrimSuffix(s, "*/"))
	return s
}

func applyAnnotation(text string, commentLine int, funcs []funcRange, disabled map[int]bool) {
	switch {
	case strings.HasPrefix(text, "mutator-disable-next-line"):
		disabled[commentLine+1] = true
	case strings.HasPrefix(text, "mutator-disable-func"):
		disableEnclosingFunc(commentLine, funcs, disabled)
	}
}

func disableEnclosingFunc(commentLine int, funcs []funcRange, disabled map[int]bool) {
	for _, r := range funcs {
		if isCommentForFunc(commentLine, r) {
			markFuncDisabled(r, disabled)
			return
		}
	}
}

// isCommentForFunc reports whether a comment on commentLine applies to the
// given function, either because it's inside the function or directly
// precedes it (godoc-style, allowing one blank line).
func isCommentForFunc(commentLine int, r funcRange) bool {
	if commentLine >= r.start && commentLine <= r.end {
		return true
	}
	return r.start > commentLine && r.start-commentLine <= 2
}

func markFuncDisabled(r funcRange, disabled map[int]bool) {
	for i := r.start; i <= r.end; i++ {
		disabled[i] = true
	}
}

type funcRange struct{ start, end int }

func funcRanges(fset *token.FileSet, f *ast.File) []funcRange {
	var ranges []funcRange
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}
		ranges = append(ranges, funcRange{
			start: fset.Position(fn.Pos()).Line,
			end:   fset.Position(fn.End()).Line,
		})
		return true
	})
	return ranges
}
