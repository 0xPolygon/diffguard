package tsanalyzer

import (
	"path/filepath"
	"sort"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
)

// deadcodeImpl is the TypeScript implementation of lang.DeadCodeDetector.
//
// Strategy: walk the program's direct children, collect every non-exported
// top-level declaration (function, lexical, class) whose name sits in a
// changed region of the diff, then count `identifier` references in the
// rest of the file. A non-exported TypeScript symbol is file-local — it
// cannot be imported from another module — so a "no references in this
// file" result is conclusive.
//
// Skipped on purpose:
//   - Exported declarations (`export function ...`, `export const ...`, …)
//     because they may be consumed by external modules.
//   - Type-only declarations (interface_declaration, type_alias_declaration,
//     enum). Tracking their uses through type positions, generics, and
//     casts is more involved and likely noisy at the file granularity used
//     here.
//   - Class methods. The class itself is the candidate; flagging an
//     individual method requires understanding which methods satisfy
//     interfaces or are overridden, which a CST walk can't do reliably.
//   - Local variables inside functions. tsc with `noUnusedLocals` and most
//     linters already catch these.
type deadcodeImpl struct{}

// FindDeadCode reports unused symbols declared in fc's changed regions for
// a TypeScript file. Parse failures return (nil, nil) to match the rest of
// tsanalyzer's "skip silently" convention.
func (deadcodeImpl) FindDeadCode(repoPath string, fc diff.FileChange) ([]lang.UnusedSymbol, error) {
	absPath := filepath.Join(repoPath, fc.Path)
	tree, src, err := parseFile(absPath)
	if err != nil {
		return nil, nil
	}
	defer tree.Close()

	candidates, declSites := collectTSDeadCodeCandidates(tree.RootNode(), src, fc)
	if len(candidates) == 0 {
		return nil, nil
	}

	refs := countTSReferences(tree.RootNode(), src, declSites)

	var results []lang.UnusedSymbol
	for _, c := range candidates {
		if refs[c.Name] > 0 {
			continue
		}
		results = append(results, c)
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Line != results[j].Line {
			return results[i].Line < results[j].Line
		}
		return results[i].Name < results[j].Name
	})
	return results, nil
}

// collectTSDeadCodeCandidates returns:
//   - candidates: non-exported top-level declarations whose name token sits
//     in the diff's changed regions
//   - declSites: byte-position set of EVERY top-level declaration name
//     token (exported and not). This second set is what the reference
//     counter uses to avoid double-counting the declaration itself as a
//     reference to itself.
func collectTSDeadCodeCandidates(root *sitter.Node, src []byte, fc diff.FileChange) ([]lang.UnusedSymbol, map[uint32]bool) {
	declSites := map[uint32]bool{}
	var candidates []lang.UnusedSymbol

	for i := 0; i < int(root.NamedChildCount()); i++ {
		child := root.NamedChild(i)
		if child == nil {
			continue
		}
		processTopLevelNode(child, src, fc, &candidates, declSites, false)
	}
	return candidates, declSites
}

// processTopLevelNode handles one direct child of the program node. The
// `exported` flag controls whether discovered names should be added to the
// candidates list — when true (we're inside an export_statement), names go
// only into declSites.
func processTopLevelNode(n *sitter.Node, src []byte, fc diff.FileChange, candidates *[]lang.UnusedSymbol, declSites map[uint32]bool, exported bool) {
	switch n.Type() {
	case "export_statement":
		recurseExportStatement(n, src, fc, candidates, declSites)
	case "function_declaration", "generator_function_declaration":
		recordNamed(n, src, fc, "func", candidates, declSites, exported)
	case "class_declaration", "abstract_class_declaration":
		recordNamed(n, src, fc, "class", candidates, declSites, exported)
	case "lexical_declaration", "variable_declaration":
		recordDeclaratorList(n, src, fc, candidates, declSites, exported)
	}
}

// recurseExportStatement walks the children of an export_statement and
// processes each as a top-level node with `exported=true`. Names discovered
// inside go into declSites only — never into candidates.
func recurseExportStatement(n *sitter.Node, src []byte, fc diff.FileChange, candidates *[]lang.UnusedSymbol, declSites map[uint32]bool) {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if c == nil {
			continue
		}
		processTopLevelNode(c, src, fc, candidates, declSites, true)
	}
}

// recordDeclaratorList handles a const/let/var statement, which can hold
// multiple declarators (`const a = 1, b = 2`). Each declarator is processed
// independently so per-name candidacy is correct.
func recordDeclaratorList(n *sitter.Node, src []byte, fc diff.FileChange, candidates *[]lang.UnusedSymbol, declSites map[uint32]bool, exported bool) {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if c == nil || c.Type() != "variable_declarator" {
			continue
		}
		recordVariableDeclarator(c, src, fc, candidates, declSites, exported)
	}
}

// recordNamed handles a node whose declared name lives in its `name` field
// (function_declaration, class_declaration, …). Adds the name's byte
// position to declSites unconditionally; appends to candidates only when
// the node is non-exported AND the name token sits in a changed region.
func recordNamed(n *sitter.Node, src []byte, fc diff.FileChange, kind string, candidates *[]lang.UnusedSymbol, declSites map[uint32]bool, exported bool) {
	name := n.ChildByFieldName("name")
	if name == nil {
		return
	}
	declSites[name.StartByte()] = true
	if exported {
		return
	}
	line := nodeLine(name)
	if !fc.ContainsLine(line) {
		return
	}
	*candidates = append(*candidates, lang.UnusedSymbol{
		File: fc.Path,
		Line: line,
		Name: nodeText(name, src),
		Kind: kind,
	})
}

// recordVariableDeclarator handles one entry inside a const/let/var
// statement. We only honor plain-identifier names — destructuring patterns
// (`const { a } = obj`) are skipped because their used-ness depends on
// per-property analysis we don't do here.
func recordVariableDeclarator(declarator *sitter.Node, src []byte, fc diff.FileChange, candidates *[]lang.UnusedSymbol, declSites map[uint32]bool, exported bool) {
	name := declarator.ChildByFieldName("name")
	if name == nil || name.Type() != "identifier" {
		return
	}
	declSites[name.StartByte()] = true
	if exported {
		return
	}
	line := nodeLine(name)
	if !fc.ContainsLine(line) {
		return
	}
	*candidates = append(*candidates, lang.UnusedSymbol{
		File: fc.Path,
		Line: line,
		Name: nodeText(name, src),
		Kind: kindForDeclarator(declarator),
	})
}

// kindForDeclarator returns "func" when the declarator's value is a
// function expression / arrow, otherwise "var" (or "const" / "let" — but we
// keep the kind set small and uniform with Go). This lets the report make
// the more useful distinction "unused arrow function" vs "unused
// variable".
func kindForDeclarator(declarator *sitter.Node) string {
	value := declarator.ChildByFieldName("value")
	if value == nil {
		return "var"
	}
	switch value.Type() {
	case "arrow_function", "function_expression", "function",
		"generator_function":
		return "func"
	}
	return "var"
}

// countTSReferences walks every node in the tree and returns a map from
// identifier name → reference count. An identifier is a reference if it is
// NOT one of the declaration name tokens recorded by the candidate
// collector. Property accesses (`obj.foo`) use `property_identifier`, not
// `identifier`, and so don't pollute the count.
func countTSReferences(root *sitter.Node, src []byte, declSites map[uint32]bool) map[string]int {
	refs := map[string]int{}
	walk(root, func(n *sitter.Node) bool {
		if n.Type() != "identifier" {
			return true
		}
		if declSites[n.StartByte()] {
			return true
		}
		refs[nodeText(n, src)]++
		return true
	})
	return refs
}
