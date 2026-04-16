package rustanalyzer

import (
	"sort"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
)

// sizesImpl implements lang.FunctionExtractor for Rust via tree-sitter. A
// single walk produces both the per-function sizes and the overall file
// size — the file-size row is cheap to compute from the raw byte buffer so
// we don't bother the CST for that number.
type sizesImpl struct{}

// ExtractFunctions parses absPath and returns functions overlapping the
// diff's changed regions plus the overall file size. A parse failure is
// treated as "skip this file" to match the Go analyzer's (nil, nil, nil)
// return convention.
func (sizesImpl) ExtractFunctions(absPath string, fc diff.FileChange) ([]lang.FunctionSize, *lang.FileSize, error) {
	tree, src, err := parseFile(absPath)
	if err != nil {
		return nil, nil, nil
	}
	defer tree.Close()

	fns := collectFunctions(tree.RootNode(), src)
	fileSize := &lang.FileSize{Path: fc.Path, Lines: countLines(src)}

	var results []lang.FunctionSize
	for _, fn := range fns {
		if !fc.OverlapsRange(fn.startLine, fn.endLine) {
			continue
		}
		results = append(results, lang.FunctionSize{
			FunctionInfo: lang.FunctionInfo{
				File:    fc.Path,
				Line:    fn.startLine,
				EndLine: fn.endLine,
				Name:    fn.name,
			},
			Lines: fn.endLine - fn.startLine + 1,
		})
	}

	// Deterministic order matters for report stability: sort by start line,
	// then by name so two functions declared on the same line never flip.
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Line != results[j].Line {
			return results[i].Line < results[j].Line
		}
		return results[i].Name < results[j].Name
	})
	return results, fileSize, nil
}

// rustFunction is the internal record produced by the extractor. It's
// deliberately wider than FunctionSize/FunctionComplexity because the
// complexity analyzer needs the node to walk the body; keeping one record
// shape avoids re-parsing or re-walking.
type rustFunction struct {
	name      string
	startLine int
	endLine   int
	body      *sitter.Node // the body block, or nil for e.g. trait methods with no default impl
	node      *sitter.Node // the entire function_item / declaration node
}

// collectFunctions walks the CST and returns every function_item and every
// method inside an impl_item. Nested functions are reported as separate
// entries to match the spec. Trait default methods are included too —
// their function_item has a body.
//
// Name extraction rules:
//
//	fn foo()                               -> "foo"
//	impl Type { fn bar() }                 -> "Type::bar"
//	impl Trait for Type { fn baz() }       -> "Type::baz"
//	impl<T> Foo<T> { fn qux() }            -> "Foo::qux"
//
// The grammar uses a uniform node kind `function_item` for every function
// definition regardless of context; its parent (`declaration_list` of an
// `impl_item`) tells us the receiver type.
func collectFunctions(root *sitter.Node, src []byte) []rustFunction {
	var fns []rustFunction
	walk(root, func(n *sitter.Node) bool {
		if n.Type() != "function_item" {
			return true
		}
		fn := buildRustFunction(n, src)
		if fn != nil {
			fns = append(fns, *fn)
		}
		// Keep descending: a function may contain nested closures or
		// function items the spec treats as separate entries.
		return true
	})
	return fns
}

// buildRustFunction constructs a rustFunction record from a function_item
// node. Returns nil if the name is unparseable.
func buildRustFunction(n *sitter.Node, src []byte) *rustFunction {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	baseName := nodeText(nameNode, src)

	fullName := baseName
	if typeName := enclosingImplType(n, src); typeName != "" {
		fullName = typeName + "::" + baseName
	}

	body := n.ChildByFieldName("body")
	return &rustFunction{
		name:      fullName,
		startLine: nodeLine(n),
		endLine:   nodeEndLine(n),
		body:      body,
		node:      n,
	}
}

// enclosingImplType walks up parents looking for an impl_item and returns
// its "type" field's text (the `Type` in `impl Type { ... }` or
// `impl Trait for Type { ... }`). Returns "" if the function is not inside
// an impl block.
func enclosingImplType(n *sitter.Node, src []byte) string {
	for parent := n.Parent(); parent != nil; parent = parent.Parent() {
		if parent.Type() == "impl_item" {
			typeNode := parent.ChildByFieldName("type")
			if typeNode == nil {
				return ""
			}
			return simpleTypeName(typeNode, src)
		}
	}
	return ""
}

// simpleTypeName strips generics and pathing from a type node, returning
// just the trailing identifier (`Foo` from `path::to::Foo<T, U>`). The
// impl-type field is usually already simple but the grammar allows any
// type expression here, including `generic_type` with a `type_arguments`
// child and `scoped_type_identifier` with a `path::`/`name` pair.
func simpleTypeName(n *sitter.Node, src []byte) string {
	switch n.Type() {
	case "type_identifier", "primitive_type":
		return nodeText(n, src)
	case "generic_type":
		if inner := n.ChildByFieldName("type"); inner != nil {
			return simpleTypeName(inner, src)
		}
	case "scoped_type_identifier":
		if name := n.ChildByFieldName("name"); name != nil {
			return nodeText(name, src)
		}
	case "reference_type":
		if inner := n.ChildByFieldName("type"); inner != nil {
			return simpleTypeName(inner, src)
		}
	}
	// Fallback: take the last identifier-looking child so unusual shapes
	// don't collapse to an empty name.
	for i := int(n.ChildCount()) - 1; i >= 0; i-- {
		c := n.Child(i)
		if c == nil {
			continue
		}
		if c.Type() == "type_identifier" || c.Type() == "identifier" {
			return nodeText(c, src)
		}
	}
	return nodeText(n, src)
}

// countLines returns the number of source lines in src. An empty file is
// 0, a file without a trailing newline still counts its final line, a file
// with a trailing newline counts exactly that many newline-terminated
// lines.
func countLines(src []byte) int {
	if len(src) == 0 {
		return 0
	}
	count := 0
	for _, b := range src {
		if b == '\n' {
			count++
		}
	}
	if src[len(src)-1] != '\n' {
		count++
	}
	return count
}
