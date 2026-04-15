package mutation

import (
	"go/ast"
	"go/token"
	"strings"
)

// scanAnnotations returns the set of source lines where mutation generation
// should be suppressed based on mutator-disable-* comment annotations.
//
// Supported annotations:
//   - // mutator-disable-next-line : skips mutations on the following line
//   - // mutator-disable-func      : skips mutations in the enclosing function
func scanAnnotations(fset *token.FileSet, f *ast.File) map[int]bool {
	disabled := make(map[int]bool)
	funcs := funcRanges(fset, f)

	for _, cg := range f.Comments {
		for _, c := range cg.List {
			applyAnnotation(stripCommentMarkers(c.Text), fset.Position(c.Pos()).Line, funcs, disabled)
		}
	}
	return disabled
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

type funcRange struct {
	start, end int
}

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
