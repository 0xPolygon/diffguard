package tsanalyzer

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// annotationScannerImpl implements lang.AnnotationScanner for TypeScript.
// Disable annotations use the JS/TS comment prefix:
//
//	// mutator-disable-next-line
//	// mutator-disable-func
//
// Block comments (`/* ... */`) are accepted for parity with the other
// analyzers; tree-sitter models them as `comment` or `block_comment`
// depending on grammar version, so we check both.
type annotationScannerImpl struct{}

// ScanAnnotations returns the set of 1-based source lines on which
// mutation generation should be suppressed.
func (annotationScannerImpl) ScanAnnotations(absPath string) (map[int]bool, error) {
	tree, src, err := parseFile(absPath)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	disabled := map[int]bool{}
	funcRanges := collectFuncRanges(tree.RootNode())

	walk(tree.RootNode(), func(n *sitter.Node) bool {
		switch n.Type() {
		case "comment", "line_comment", "block_comment":
			applyAnnotation(n, src, funcRanges, disabled)
		}
		return true
	})
	return disabled, nil
}

// applyAnnotation consumes a single comment node and, if it carries a
// known annotation, disables the appropriate line(s) in `disabled`.
func applyAnnotation(comment *sitter.Node, src []byte, funcs []funcRange, disabled map[int]bool) {
	text := stripCommentMarkers(nodeText(comment, src))
	line := nodeLine(comment)
	switch {
	case strings.HasPrefix(text, "mutator-disable-next-line"):
		disabled[line+1] = true
	case strings.HasPrefix(text, "mutator-disable-func"):
		disableEnclosingFunc(line, funcs, disabled)
	}
}

// stripCommentMarkers strips `//`, `/*`, `*/` and surrounding whitespace.
// Matches the Rust/Go analyzer helpers.
func stripCommentMarkers(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "//")
	s = strings.TrimPrefix(s, "/*")
	s = strings.TrimSuffix(s, "*/")
	return strings.TrimSpace(s)
}

// disableEnclosingFunc marks every line of the function the comment
// belongs to as disabled. A comment belongs to a function when it sits
// inside the function's range, or when it directly precedes the function
// (at most one blank line in between).
func disableEnclosingFunc(commentLine int, funcs []funcRange, disabled map[int]bool) {
	for _, r := range funcs {
		if isCommentForFunc(commentLine, r) {
			for i := r.start; i <= r.end; i++ {
				disabled[i] = true
			}
			return
		}
	}
}

func isCommentForFunc(commentLine int, r funcRange) bool {
	if commentLine >= r.start && commentLine <= r.end {
		return true
	}
	return r.start > commentLine && r.start-commentLine <= 2
}

// funcRange is the 1-based inclusive line span of a function declaration.
// Same shape used by the annotation scanner and the mutant generator.
type funcRange struct{ start, end int }

// collectFuncRanges returns one funcRange per function declaration in the
// file — all the forms collectFunctions picks up (function_declaration,
// method_definition, arrow functions/function expressions assigned to a
// variable_declarator, generator functions).
func collectFuncRanges(root *sitter.Node) []funcRange {
	var ranges []funcRange
	walk(root, func(n *sitter.Node) bool {
		switch n.Type() {
		case "function_declaration", "method_definition",
			"generator_function_declaration",
			"arrow_function", "function_expression", "generator_function":
			ranges = append(ranges, funcRange{
				start: nodeLine(n),
				end:   nodeEndLine(n),
			})
		}
		return true
	})
	return ranges
}
