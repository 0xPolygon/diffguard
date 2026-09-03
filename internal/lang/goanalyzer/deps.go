package goanalyzer

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// depsImpl implements lang.ImportResolver for Go. It reads the module path
// from go.mod and uses the standard Go parser to scan each package for
// internal imports.
type depsImpl struct{}

// DetectModulePath reads `module <path>` from repoPath/go.mod.
func (depsImpl) DetectModulePath(repoPath string) (string, error) {
	goModPath := filepath.Join(repoPath, "go.mod")
	content, err := os.ReadFile(goModPath)
	if err != nil {
		return "", fmt.Errorf("reading go.mod: %w", err)
	}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
		}
	}
	return "", fmt.Errorf("no module directive found in go.mod")
}

// ScanPackageImports returns a map with a single entry:
//
//	{ <pkgImportPath>: { <internalDep1>: true, <internalDep2>: true, ... } }
//
// where pkgImportPath = modulePath + "/" + pkgDir. Only files the Go build
// would compile for the host platform are scanned: generator scripts kept
// next to library code under `//go:build ignore` (or `none`) are package
// main and routinely import the very package they sit in, which the parser
// alone would report as a self-import cycle. External imports and `_test`
// packages are ignored so the graph only contains internal edges, matching
// the pre-split deps.go behavior.
func (depsImpl) ScanPackageImports(repoPath, pkgDir, modulePath string) map[string]map[string]bool {
	absDir := filepath.Join(repoPath, pkgDir)
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, absDir, buildableFile(absDir), parser.ImportsOnly)
	if err != nil {
		return nil
	}

	edges := make(map[string]map[string]bool)
	pkgImportPath := modulePath + "/" + pkgDir
	for _, p := range pkgs {
		collectPackageEdges(p, modulePath, pkgImportPath, edges)
	}
	return edges
}

// buildableFile reports whether the Go build would compile the file for the
// host platform, honoring //go:build constraints and GOOS/GOARCH suffixes.
func buildableFile(dir string) func(fs.FileInfo) bool {
	return func(fi fs.FileInfo) bool {
		ok, err := build.Default.MatchFile(dir, fi.Name())
		return err == nil && ok
	}
}

// collectPackageEdges walks the files of a parsed package and adds internal
// import edges into `edges`. `_test` packages are skipped so the graph
// stays focused on non-test source dependencies.
func collectPackageEdges(p *ast.Package, modulePath, pkgImportPath string, edges map[string]map[string]bool) {
	if strings.HasSuffix(p.Name, "_test") {
		return
	}
	for _, f := range p.Files {
		for _, imp := range f.Imports {
			addInternalImport(imp, modulePath, pkgImportPath, edges)
		}
	}
}

// addInternalImport records an edge from pkgImportPath to the target of imp
// when the import is internal to modulePath. External imports are dropped.
func addInternalImport(imp *ast.ImportSpec, modulePath, pkgImportPath string, edges map[string]map[string]bool) {
	importPath := strings.Trim(imp.Path.Value, `"`)
	if !strings.HasPrefix(importPath, modulePath) {
		return
	}
	if edges[pkgImportPath] == nil {
		edges[pkgImportPath] = make(map[string]bool)
	}
	edges[pkgImportPath][importPath] = true
}
