package rustanalyzer

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// annotationScannerImpl implements lang.AnnotationScanner for Rust. The
// disable annotations are identical to the Go forms:
//
//	// mutator-disable-next-line
//	// mutator-disable-func
//
// `//` and `/* ... */` comments are both accepted — tree-sitter exposes
// them as `line_comment` and `block_comment` respectively.
type annotationScannerImpl struct{}

// ScanAnnotations returns the set of 1-based source lines on which mutation
// generation should be suppressed.
func (annotationScannerImpl) ScanAnnotations(absPath string) (map[int]bool, error) {
	tree, src, err := parseFile(absPath)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	disabled := map[int]bool{}
	funcRanges := collectFuncRanges(tree.RootNode(), src)

	walk(tree.RootNode(), func(n *sitter.Node) bool {
		switch n.Type() {
		case "line_comment", "block_comment":
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
// Matches the Go analyzer's helper so annotation behavior stays uniform
// across languages.
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
// (at most one blank line between them, matching the Go analyzer).
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

// funcRange is the 1-based inclusive line span of a function_item node.
// The same range shape is used by the annotation scanner and by the mutant
// generator (via its filtering of "which lines belong to a function").
type funcRange struct{ start, end int }

// collectFuncRanges returns one funcRange per function_item in the file.
// Methods inside impl blocks are included too — same source-line universe
// the mutant generator cares about.
func collectFuncRanges(root *sitter.Node, _ []byte) []funcRange {
	var ranges []funcRange
	walk(root, func(n *sitter.Node) bool {
		if n.Type() != "function_item" {
			return true
		}
		ranges = append(ranges, funcRange{
			start: nodeLine(n),
			end:   nodeEndLine(n),
		})
		return true
	})
	return ranges
}
