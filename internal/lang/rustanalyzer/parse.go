// Package rustanalyzer implements the lang.Language interface for Rust. It
// is blank-imported from cmd/diffguard/main.go so Rust gets registered at
// process start.
//
// One file per concern, mirroring the Go analyzer layout:
//   - rustanalyzer.go     -- Language + init()/Register
//   - parse.go            -- tree-sitter setup, CST helpers
//   - sizes.go            -- FunctionExtractor
//   - complexity.go       -- ComplexityCalculator + ComplexityScorer
//   - deps.go             -- ImportResolver
//   - mutation_generate.go-- MutantGenerator
//   - mutation_apply.go   -- MutantApplier
//   - mutation_annotate.go-- AnnotationScanner
//   - testrunner.go       -- TestRunner (wraps cargo test)
package rustanalyzer

import (
	"context"
	"os"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/rust"
)

// rustLang is the cached tree-sitter Rust grammar handle. Because building
// the grammar involves cgo bridging, we do it once and reuse the pointer
// rather than paying for it on every parse. Lazy-init keeps process start
// fast — diffguard binaries that never touch a .rs file pay nothing.
var (
	rustLangOnce sync.Once
	rustLang     *sitter.Language
)

// rustLanguage returns the tree-sitter Rust grammar, building it on first
// use. The sitter.Language struct is safe to share across goroutines.
func rustLanguage() *sitter.Language {
	rustLangOnce.Do(func() {
		rustLang = rust.GetLanguage()
	})
	return rustLang
}

// parseFile reads absPath from disk and returns the parsed tree plus the
// source bytes. Callers get back (nil, nil, err) on read error.
func parseFile(absPath string) (*sitter.Tree, []byte, error) {
	src, err := os.ReadFile(absPath)
	if err != nil {
		return nil, nil, err
	}
	tree, err := parseBytes(src)
	if err != nil {
		return nil, nil, err
	}
	return tree, src, nil
}

// parseBytes returns a *sitter.Tree for src. Unlike sitter.Parse which
// returns only the root node, we return the Tree so callers can hold onto
// it and Close it when done to release the underlying C allocation.
func parseBytes(src []byte) (*sitter.Tree, error) {
	parser := sitter.NewParser()
	parser.SetLanguage(rustLanguage())
	return parser.ParseCtx(context.Background(), nil, src)
}

// walk invokes fn on every node in the subtree rooted at n. The walk is a
// plain depth-first pre-order traversal using NamedChildCount/NamedChild —
// matches the style used by the sitter example code and avoids the trickier
// TreeCursor API. Returning false from fn prunes the subtree.
func walk(n *sitter.Node, fn func(*sitter.Node) bool) {
	if n == nil {
		return
	}
	if !fn(n) {
		return
	}
	for i := range int(n.ChildCount()) {
		walk(n.Child(i), fn)
	}
}

// nodeLine returns the 1-based start line of n. tree-sitter uses 0-based
// coordinates internally; every diffguard interface (FunctionInfo, MutantSite)
// is 1-based, so we convert here once.
func nodeLine(n *sitter.Node) int {
	return int(n.StartPoint().Row) + 1
}

// nodeEndLine returns the 1-based end line of n (inclusive of the last line
// any part of n occupies). We subtract one when EndPoint is exactly at a
// line boundary (column 0) because tree-sitter reports the position one past
// the last byte — e.g. a function whose closing brace is the last char on
// line 10 has EndPoint at (11, 0). Without the adjustment we'd report end
// lines that disagree with the Go analyzer's behavior.
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
