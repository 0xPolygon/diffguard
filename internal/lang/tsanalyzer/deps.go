package tsanalyzer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// depsImpl implements lang.ImportResolver for TypeScript via tree-sitter.
// package.json gives us the project name; import and require() statements
// in source files provide the internal dependency edges.
//
// The returned graph uses directory-level node keys (paths relative to the
// repo root) so it matches the Go and Rust analyzers' shape: every edge
// says "this package directory depends on that package directory".
type depsImpl struct{}

// DetectModulePath returns the `name` from package.json. Missing / unnamed
// package.json returns an error — same contract as the Rust analyzer's
// Cargo.toml handler.
func (depsImpl) DetectModulePath(repoPath string) (string, error) {
	manifestPath := filepath.Join(repoPath, "package.json")
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", fmt.Errorf("reading package.json: %w", err)
	}
	var pkg struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(content, &pkg); err != nil {
		return "", fmt.Errorf("parsing package.json: %w", err)
	}
	if pkg.Name == "" {
		return "", fmt.Errorf("no name field in package.json")
	}
	return pkg.Name, nil
}

// ScanPackageImports returns a single-entry adjacency map:
//
//	{ <pkgDir>: { <internalDep1>: true, <internalDep2>: true, ... } }
//
// where keys are directories relative to repoPath. An import specifier is
// "internal" when it begins with `.` (relative import) or a registered
// project alias (`@/`, `~/`). External packages (bare specifiers) are
// filtered out.
func (depsImpl) ScanPackageImports(repoPath, pkgDir, _ string) map[string]map[string]bool {
	absDir := filepath.Join(repoPath, pkgDir)
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil
	}

	deps := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".ts") && !strings.HasSuffix(name, ".tsx") {
			continue
		}
		absFile := filepath.Join(absDir, name)
		if isTSTestFile(absFile) {
			continue
		}
		collectImports(absFile, repoPath, pkgDir, deps)
	}
	if len(deps) == 0 {
		return nil
	}
	return map[string]map[string]bool{pkgDir: deps}
}

// collectImports parses one .ts/.tsx file and adds each internal import /
// require to `deps`. Parse errors are silently ignored to match the other
// analyzers' "skip broken files" behavior.
func collectImports(absFile, repoPath, pkgDir string, deps map[string]bool) {
	tree, src, err := parseFile(absFile)
	if err != nil {
		return
	}
	defer tree.Close()

	walk(tree.RootNode(), func(n *sitter.Node) bool {
		switch n.Type() {
		case "import_statement":
			addImportEdge(n, src, repoPath, pkgDir, deps)
		case "call_expression":
			// require('./foo') style.
			addRequireEdge(n, src, repoPath, pkgDir, deps)
		}
		return true
	})
}

// addImportEdge reads the source specifier of an import_statement and, if
// it resolves to an internal module, records an edge.
func addImportEdge(n *sitter.Node, src []byte, repoPath, pkgDir string, deps map[string]bool) {
	// The `source` field is a string node.
	source := n.ChildByFieldName("source")
	if source == nil {
		return
	}
	spec := unquote(nodeText(source, src))
	target := resolveInternal(spec, repoPath, pkgDir)
	if target == "" {
		return
	}
	deps[target] = true
}

// addRequireEdge matches `require('...')` calls and records an edge when
// the specifier is internal.
func addRequireEdge(n *sitter.Node, src []byte, repoPath, pkgDir string, deps map[string]bool) {
	fn := n.ChildByFieldName("function")
	if fn == nil {
		return
	}
	if nodeText(fn, src) != "require" {
		return
	}
	args := n.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return
	}
	arg := args.NamedChild(0)
	if arg == nil {
		return
	}
	if arg.Type() != "string" {
		return
	}
	spec := unquote(nodeText(arg, src))
	target := resolveInternal(spec, repoPath, pkgDir)
	if target == "" {
		return
	}
	deps[target] = true
}

// unquote strips a single pair of surrounding single, double, or backtick
// quotes from a string literal's source text. Nothing fancier — TypeScript
// string literals in import specifiers are always simple.
func unquote(s string) string {
	if len(s) < 2 {
		return s
	}
	first, last := s[0], s[len(s)-1]
	if first != last {
		return s
	}
	if first == '"' || first == '\'' || first == '`' {
		return s[1 : len(s)-1]
	}
	return s
}

// resolveInternal maps an import specifier to a repo-relative directory or
// returns "" if the import is external.
//
//	./foo              -> pkgDir/foo
//	../shared/util     -> <parent>/shared/util
//	@/components/Card  -> components/Card (common Next.js alias)
//	~/lib/foo          -> lib/foo (common project alias)
//	lodash             -> ""  (external)
func resolveInternal(spec, repoPath, pkgDir string) string {
	if spec == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") || spec == "." || spec == "..":
		return resolveRelative(spec, pkgDir)
	case strings.HasPrefix(spec, "@/"):
		return filepath.ToSlash(filepath.Dir("@/" + spec[2:]))
	case strings.HasPrefix(spec, "~/"):
		return filepath.ToSlash(filepath.Dir(spec[2:]))
	}
	return ""
}

// resolveRelative resolves a relative specifier against the importing
// file's package directory. We fold the result to directory granularity —
// a specifier ending in `/index` or a bare file basename resolves to the
// directory containing it, matching the Go analyzer's package-level edge
// shape.
func resolveRelative(spec, pkgDir string) string {
	combined := filepath.Join(pkgDir, spec)
	// If the specifier points at a file basename (no extension, no trailing
	// slash), we still want the containing directory as the graph node.
	// filepath.Dir gives us that for both `./foo` -> pkgDir/foo (we treat
	// as directory) and `./foo/bar` -> pkgDir/foo/bar.
	cleaned := filepath.ToSlash(filepath.Clean(combined))
	// A trailing `/index` or `/index.ts` etc. folds to the parent directory.
	base := filepath.Base(cleaned)
	if base == "index" {
		cleaned = filepath.ToSlash(filepath.Dir(cleaned))
	}
	return cleaned
}
