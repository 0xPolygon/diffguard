package tsanalyzer

import (
	"sort"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
)

// sizesImpl implements lang.FunctionExtractor for TypeScript via
// tree-sitter. Both the `.ts` and `.tsx` grammars share the function node
// kinds we care about (function_declaration, method_definition,
// arrow_function, generator_function), so the walk is grammar-agnostic —
// we only switch grammars at parse time based on the file extension.
type sizesImpl struct{}

// ExtractFunctions parses absPath and returns functions overlapping the
// diff's changed regions plus the overall file size. A parse failure is
// treated as "skip this file" to match the Go and Rust analyzers'
// (nil, nil, nil) convention.
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

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Line != results[j].Line {
			return results[i].Line < results[j].Line
		}
		return results[i].Name < results[j].Name
	})
	return results, fileSize, nil
}

// tsFunction is the internal record produced by the extractor. Wider than
// FunctionSize so the complexity analyzer can walk the body without
// re-parsing.
type tsFunction struct {
	name      string
	startLine int
	endLine   int
	body      *sitter.Node // the body/statement_block
	node      *sitter.Node // the outer function-ish node
}

// collectFunctions walks the CST and returns every declared function form
// the spec cares about:
//
//   - function_declaration: classic `function foo() {}` or `function* gen() {}`
//   - generator_function_declaration: `function* gen() {}` (some grammars)
//   - method_definition: `class X { foo() {} }` — named after its class
//   - variable_declarator with an arrow_function or function expression
//     initializer: `const foo = () => ...` or `const foo = function() {}`
//
// Nested functions are separate entries (matching Rust/Go).
func collectFunctions(root *sitter.Node, src []byte) []tsFunction {
	var fns []tsFunction

	walk(root, func(n *sitter.Node) bool {
		switch n.Type() {
		case "function_declaration", "generator_function_declaration":
			fns = appendFunction(fns, n, src, standaloneName(n, src))
		case "method_definition":
			fns = appendFunction(fns, n, src, methodName(n, src))
		case "variable_declarator":
			// const/let/var NAME = (arrow|function)
			if fn := variableInitializedFn(n, src); fn != nil {
				fns = append(fns, *fn)
			}
		}
		return true
	})
	return fns
}

// appendFunction pushes a function record with startLine/endLine/body
// resolved. Returns nil if the node has no resolvable body so callers
// don't end up with partial records.
func appendFunction(acc []tsFunction, n *sitter.Node, src []byte, name string) []tsFunction {
	if name == "" {
		return acc
	}
	body := n.ChildByFieldName("body")
	return append(acc, tsFunction{
		name:      name,
		startLine: nodeLine(n),
		endLine:   nodeEndLine(n),
		body:      body,
		node:      n,
	})
}

// standaloneName returns the function's name for a function_declaration or
// generator_function_declaration. tree-sitter exposes the name via a
// "name" field.
func standaloneName(n *sitter.Node, src []byte) string {
	if name := n.ChildByFieldName("name"); name != nil {
		return nodeText(name, src)
	}
	return ""
}

// methodName returns `ClassName.method` for a method_definition. The
// grammar puts the enclosing class_declaration/class a few levels up; we
// walk ancestors until we find one and take its name field. If there's no
// class (rare — e.g. an object literal method), we fall back to the bare
// method name so the function is still tracked.
func methodName(n *sitter.Node, src []byte) string {
	name := n.ChildByFieldName("name")
	if name == nil {
		return ""
	}
	methodBase := nodeText(name, src)

	// Walk up for the enclosing class name. Stop if we hit a function
	// boundary first (e.g. a method defined inside a nested function — we
	// don't prefix it with the outer class then).
	for parent := n.Parent(); parent != nil; parent = parent.Parent() {
		switch parent.Type() {
		case "class_declaration", "class", "abstract_class_declaration":
			if cn := parent.ChildByFieldName("name"); cn != nil {
				return nodeText(cn, src) + "." + methodBase
			}
			return methodBase
		case "function_declaration", "arrow_function", "function_expression",
			"generator_function", "generator_function_declaration",
			"method_definition":
			// Crossed a function boundary with no class — surface just the
			// method base name.
			if parent == n {
				continue
			}
			return methodBase
		}
	}
	return methodBase
}

// variableInitializedFn returns a tsFunction if the variable_declarator's
// value is a function-like initializer (arrow or function expression /
// generator). Name is taken from the declarator's "name" field, which is
// an identifier for the common `const x = () => {}` pattern.
//
// Destructuring patterns (`const {a} = ...`) don't count — the "name" field
// of the declarator is a pattern rather than an identifier. We only emit
// when the name resolves to a plain identifier.
func variableInitializedFn(n *sitter.Node, src []byte) *tsFunction {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil || nameNode.Type() != "identifier" {
		return nil
	}
	value := n.ChildByFieldName("value")
	if value == nil {
		return nil
	}
	// Grammars differ slightly on the node kind for function expressions;
	// we accept the canonical set covering `() => {}`, `function() {}`,
	// `function* () {}`, and async variants.
	switch value.Type() {
	case "arrow_function", "function_expression", "function",
		"generator_function":
		body := value.ChildByFieldName("body")
		return &tsFunction{
			name:      nodeText(nameNode, src),
			startLine: nodeLine(value),
			endLine:   nodeEndLine(value),
			body:      body,
			node:      value,
		}
	}
	return nil
}
