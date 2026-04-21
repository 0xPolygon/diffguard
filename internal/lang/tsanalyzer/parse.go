// Package tsanalyzer implements the lang.Language interface for TypeScript
// (and .tsx). It is blank-imported from cmd/diffguard/main.go so TypeScript
// gets registered at process start.
//
// One file per concern, mirroring the Go and Rust analyzers:
//   - tsanalyzer.go       -- Language + init()/Register + detector
//   - parse.go            -- tree-sitter setup, CST helpers, grammar pick
//   - sizes.go            -- FunctionExtractor
//   - complexity.go       -- ComplexityCalculator + ComplexityScorer
//   - deps.go             -- ImportResolver
//   - mutation_generate.go-- MutantGenerator
//   - mutation_apply.go   -- MutantApplier
//   - mutation_annotate.go-- AnnotationScanner
//   - testrunner.go       -- TestRunner (wraps vitest/jest/npm test)
//
// Unlike the Go analyzer, TypeScript requires two tree-sitter grammars:
// one for `.ts` (typescript) and one for `.tsx` (tsx). The two grammars
// are nearly identical for our purposes (node kinds like `if_statement`,
// `function_declaration`, etc. are shared) but the parser input has to
// match the extension — the tsx grammar accepts JSX syntax, the plain
// typescript grammar rejects it.
package tsanalyzer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

// tsLang / tsxLang are the cached tree-sitter grammar handles. Building
// them crosses cgo so we only do it once each.
var (
	tsLangOnce  sync.Once
	tsLang      *sitter.Language
	tsxLangOnce sync.Once
	tsxLangLang *sitter.Language
)

// typescriptLanguage returns the tree-sitter grammar for `.ts` source.
func typescriptLanguage() *sitter.Language {
	tsLangOnce.Do(func() {
		tsLang = typescript.GetLanguage()
	})
	return tsLang
}

// tsxLanguage returns the tree-sitter grammar for `.tsx` source.
func tsxLanguage() *sitter.Language {
	tsxLangOnce.Do(func() {
		tsxLangLang = tsx.GetLanguage()
	})
	return tsxLangLang
}

// grammarFor returns the grammar that matches the given file's extension.
// `.tsx` uses the tsx grammar (accepts JSX); everything else (including
// `.ts`, or when the extension isn't obvious) uses the plain typescript
// grammar. Callers are expected to have already filtered to TypeScript
// extensions upstream so the default branch is rare.
func grammarFor(path string) *sitter.Language {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".tsx" {
		return tsxLanguage()
	}
	return typescriptLanguage()
}

// parseFile reads absPath from disk and returns the parsed tree plus the
// source bytes, picking the grammar by file extension. Callers get back
// (nil, nil, err) on read error.
func parseFile(absPath string) (*sitter.Tree, []byte, error) {
	src, err := os.ReadFile(absPath)
	if err != nil {
		return nil, nil, err
	}
	tree, err := parseBytesAs(src, grammarFor(absPath))
	if err != nil {
		return nil, nil, err
	}
	return tree, src, nil
}

// parseBytes parses src with the plain TypeScript grammar. Convenience
// wrapper used by tests that don't care about JSX.
func parseBytes(src []byte) (*sitter.Tree, error) {
	return parseBytesAs(src, typescriptLanguage())
}

// parseBytesAs parses src with the given grammar. The returned *sitter.Tree
// must have its Close() called to release the underlying C allocation.
func parseBytesAs(src []byte, grammar *sitter.Language) (*sitter.Tree, error) {
	parser := sitter.NewParser()
	parser.SetLanguage(grammar)
	return parser.ParseCtx(context.Background(), nil, src)
}

// walk invokes fn on every node in the subtree rooted at n. Plain
// depth-first pre-order, identical to the rust analyzer's walk.
func walk(n *sitter.Node, fn func(*sitter.Node) bool) {
	if n == nil {
		return
	}
	if !fn(n) {
		return
	}
	count := int(n.ChildCount())
	for i := 0; i < count; i++ {
		walk(n.Child(i), fn)
	}
}

// nodeLine returns the 1-based start line of n.
func nodeLine(n *sitter.Node) int {
	return int(n.StartPoint().Row) + 1
}

// nodeEndLine returns the 1-based end line of n. Tree-sitter reports
// EndPoint at the position one past the last byte, so a function whose
// closing brace is the last char on line 10 has EndPoint at (11, 0) and we
// subtract 1 in that case to match diffguard's inclusive convention.
func nodeEndLine(n *sitter.Node) int {
	end := n.EndPoint()
	if end.Column == 0 && end.Row > 0 {
		return int(end.Row)
	}
	return int(end.Row) + 1
}

// nodeText returns the byte slice of src covering n.
func nodeText(n *sitter.Node, src []byte) string {
	return string(src[n.StartByte():n.EndByte()])
}

// countLines returns the number of source lines in src. Same rules as the
// other analyzers: empty file is 0, a file without a trailing newline still
// counts its final line.
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
