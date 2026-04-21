package rustanalyzer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// depsImpl implements lang.ImportResolver for Rust via tree-sitter. The
// Cargo.toml manifest gives us the crate (package) name; in-source
// `use crate::` / `use self::` / `use super::` declarations and `mod`
// declarations provide the internal dependency edges.
//
// The returned graph uses directory-level node keys (paths relative to the
// repo root) so it matches the Go analyzer's shape: every edge says "this
// package directory depends on that package directory".
type depsImpl struct{}

// DetectModulePath returns the crate name read from Cargo.toml's
// `[package] name = "..."` entry. We parse the TOML with a lightweight
// line scanner rather than pulling in a full TOML dependency — the two
// tokens we need are easy to find and the result is cached by the caller.
func (depsImpl) DetectModulePath(repoPath string) (string, error) {
	cargoPath := filepath.Join(repoPath, "Cargo.toml")
	content, err := os.ReadFile(cargoPath)
	if err != nil {
		return "", fmt.Errorf("reading Cargo.toml: %w", err)
	}
	name := parseCargoPackageName(string(content))
	if name == "" {
		return "", fmt.Errorf("no [package] name found in Cargo.toml")
	}
	return name, nil
}

// parseCargoPackageName extracts the `name = "..."` value from the
// [package] table of a Cargo.toml. We accept either quote style and ignore
// table nesting beyond the top-level [package] header; that's sufficient
// because `name` is never redeclared under nested tables.
func parseCargoPackageName(content string) string {
	inPackage := false
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inPackage = strings.EqualFold(line, "[package]")
			continue
		}
		if !inPackage {
			continue
		}
		if !strings.HasPrefix(line, "name") {
			continue
		}
		// line looks like: name = "foo" or name="foo"
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		val := strings.TrimSpace(line[eq+1:])
		val = strings.Trim(val, "\"'")
		if val != "" {
			return val
		}
	}
	return ""
}

// ScanPackageImports returns a single-entry adjacency map:
//
//	{ <pkgDir>: { <internalDep1>: true, <internalDep2>: true, ... } }
//
// where keys are directories relative to repoPath. A use declaration is
// "internal" when it begins with `crate::`, `self::`, or `super::`.
// External crates (anything else) are filtered out. `mod foo;` adds an
// edge from the current package to the child module subdir.
func (depsImpl) ScanPackageImports(repoPath, pkgDir, _ string) map[string]map[string]bool {
	absDir := filepath.Join(repoPath, pkgDir)
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil
	}

	deps := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".rs") {
			continue
		}
		absFile := filepath.Join(absDir, e.Name())
		if isRustTestFile(absFile) {
			continue
		}
		collectImports(absFile, repoPath, pkgDir, deps)
	}
	if len(deps) == 0 {
		return nil
	}
	return map[string]map[string]bool{pkgDir: deps}
}

// collectImports parses one .rs file and adds each internal import / mod
// declaration to `deps`. Parse errors are silently ignored to match the Go
// analyzer's "skip broken files" behavior.
func collectImports(absFile, repoPath, pkgDir string, deps map[string]bool) {
	tree, src, err := parseFile(absFile)
	if err != nil {
		return
	}
	defer tree.Close()

	walk(tree.RootNode(), func(n *sitter.Node) bool {
		switch n.Type() {
		case "use_declaration":
			addUseEdge(n, src, pkgDir, deps)
		case "mod_item":
			addModEdge(n, src, repoPath, pkgDir, deps)
		}
		return true
	})
}

// addUseEdge examines a `use` declaration and, if it starts with
// `crate::` / `self::` / `super::`, records an edge to the directory that
// corresponds to the path's module prefix. We stop at the penultimate
// segment because the final segment is the imported item (function/type/
// trait), not a package directory.
func addUseEdge(n *sitter.Node, src []byte, pkgDir string, deps map[string]bool) {
	// The `argument` field holds the import path tree.
	arg := n.ChildByFieldName("argument")
	if arg == nil {
		return
	}
	// Walk the arg, skipping the final item to produce a package path.
	segs := collectUseSegments(arg, src)
	if len(segs) == 0 {
		return
	}
	target := resolveInternalPath(segs, pkgDir)
	if target == "" {
		return
	}
	deps[target] = true
}

// collectUseSegments returns the left-to-right identifier sequence of a
// use path. We skip list forms (`use foo::{bar, baz}`) by only descending
// through scoped_identifier / scoped_use_list / identifier structures and
// taking the first branch — good enough to detect `crate::`/`self::`/
// `super::` roots for edge classification.
//
// Only the prefix is load-bearing; we intentionally don't try to enumerate
// every symbol in a nested use list because the edge granularity is the
// module (directory), not the symbol.
func collectUseSegments(n *sitter.Node, src []byte) []string {
	var segs []string
	var collect func(*sitter.Node)
	collect = func(cur *sitter.Node) {
		if cur == nil {
			return
		}
		switch cur.Type() {
		case "scoped_identifier":
			collect(cur.ChildByFieldName("path"))
			if name := cur.ChildByFieldName("name"); name != nil {
				segs = append(segs, nodeText(name, src))
			}
		case "identifier", "crate", "self", "super":
			segs = append(segs, nodeText(cur, src))
		case "use_list":
			// Take only the first item of a `{a, b}` list — enough to
			// retain the shared prefix that already got emitted.
			if cur.ChildCount() > 0 {
				for i := 0; i < int(cur.ChildCount()); i++ {
					c := cur.Child(i)
					if c != nil && c.IsNamed() {
						collect(c)
						return
					}
				}
			}
		case "scoped_use_list":
			collect(cur.ChildByFieldName("path"))
			if list := cur.ChildByFieldName("list"); list != nil {
				collect(list)
			}
		case "use_as_clause":
			collect(cur.ChildByFieldName("path"))
		}
	}
	collect(n)
	return segs
}

// resolveInternalPath maps a sequence of use segments to a repo-relative
// package directory, or returns "" if the path is not internal.
//
//	crate::foo::bar::Baz  -> src/foo/bar   (relative to crate root 'src')
//	self::foo             -> pkgDir/foo    (sibling module)
//	super::foo            -> <parent>/foo
//
// We assume a standard Cargo layout: crate root lives at `src/` under the
// repo root for library crates and `src/bin/<name>.rs` / similar for
// binaries. For this analyzer, `crate::x::y::Z` resolves to `src/x/y` —
// which is the directory the imported module lives in. The final segment
// (`Z`) is dropped because we want package-level, not symbol-level, edges.
func resolveInternalPath(segs []string, pkgDir string) string {
	if len(segs) == 0 {
		return ""
	}
	// Drop the final segment (imported item) to get the module directory.
	// A single-segment import like `use crate::foo;` still lands at the
	// crate root directory since `foo` is the item, not a directory.
	modSegs := segs[:len(segs)-1]
	if len(modSegs) == 0 {
		return ""
	}

	switch modSegs[0] {
	case "crate":
		// `crate::` roots at `src/`.
		parts := append([]string{"src"}, modSegs[1:]...)
		return filepath.ToSlash(filepath.Join(parts...))
	case "self":
		parts := append([]string{pkgDir}, modSegs[1:]...)
		return filepath.ToSlash(filepath.Join(parts...))
	case "super":
		parent := filepath.Dir(pkgDir)
		if parent == "." || parent == "/" {
			parent = ""
		}
		parts := append([]string{parent}, modSegs[1:]...)
		p := filepath.Join(parts...)
		return filepath.ToSlash(p)
	}
	return ""
}

// addModEdge records an edge for `mod foo;` declarations: the module
// always resolves to a sibling directory (or sibling file) inside pkgDir.
// We emit the directory path so the graph stays at directory granularity.
func addModEdge(n *sitter.Node, src []byte, _, pkgDir string, deps map[string]bool) {
	name := n.ChildByFieldName("name")
	if name == nil {
		return
	}
	modName := nodeText(name, src)
	if modName == "" {
		return
	}
	target := filepath.ToSlash(filepath.Join(pkgDir, modName))
	deps[target] = true
}
