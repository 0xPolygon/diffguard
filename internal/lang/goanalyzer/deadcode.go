package goanalyzer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
)

// deadcodeImpl is the Go implementation of lang.DeadCodeDetector.
//
// Strategy: collect every non-exported, non-method top-level symbol declared
// inside the diff's changed regions, then count references to each name
// across every .go file in the package directory (including _test.go files,
// since test-only usage still counts as "used"). A symbol with zero
// references is reported as dead.
//
// The detector is deliberately scoped to:
//   - Non-exported names — exported symbols may be consumed by external
//     packages or external repos and we can't see those without a whole-
//     codebase scan that would still miss reflective use.
//   - Free functions, package-level vars, and package-level consts — methods
//     are skipped because they may satisfy interfaces, types are skipped
//     because tracking type uses through embedded fields and conversions is
//     too coarse to be useful at this granularity.
//   - Functions other than init / main / TestXxx / BenchmarkXxx / ExampleXxx
//     / FuzzXxx — these are entry points called by the runtime or test
//     framework, not by other Go code, so a "no references" result is a
//     false positive by construction.
type deadcodeImpl struct{}

// FindDeadCode reports unused symbols declared in fc's changed regions.
// Parse errors are swallowed (returning nil) to match the rest of the
// goanalyzer, which treats unparseable files as "skip silently".
func (deadcodeImpl) FindDeadCode(repoPath string, fc diff.FileChange) ([]lang.UnusedSymbol, error) {
	absPath := filepath.Join(repoPath, fc.Path)
	fset, file, err := parseFile(absPath, parser.SkipObjectResolution)
	if err != nil {
		return nil, nil
	}

	candidates := collectDeadCodeCandidates(fset, file, fc)
	if len(candidates) == 0 {
		return nil, nil
	}

	pkgDir := filepath.Dir(absPath)
	refs := scanGoPackageReferences(pkgDir)

	var results []lang.UnusedSymbol
	for _, c := range candidates {
		if refs[c.Name] > 0 {
			continue
		}
		results = append(results, c)
	}
	return results, nil
}

// collectDeadCodeCandidates walks the top-level declarations of file and
// returns the ones that fall inside fc's changed regions, are non-exported,
// and aren't framework-special (init/main/TestXxx/...).
func collectDeadCodeCandidates(fset *token.FileSet, file *ast.File, fc diff.FileChange) []lang.UnusedSymbol {
	pkgName := ""
	if file.Name != nil {
		pkgName = file.Name.Name
	}
	var out []lang.UnusedSymbol
	for _, decl := range file.Decls {
		out = append(out, candidatesFromDecl(fset, decl, fc, pkgName)...)
	}
	return out
}

func candidatesFromDecl(fset *token.FileSet, decl ast.Decl, fc diff.FileChange, pkgName string) []lang.UnusedSymbol {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		return funcCandidate(fset, d, fc, pkgName)
	case *ast.GenDecl:
		return genDeclCandidates(fset, d, fc)
	}
	return nil
}

// funcCandidate returns a single-element slice for non-exported free
// functions whose declaration line is in the diff. Methods (Recv != nil)
// are skipped — they may satisfy an interface that our scan can't see. So
// are init / main / TestXxx / BenchmarkXxx / ExampleXxx / FuzzXxx because
// the runtime or test framework calls them, not user Go code.
func funcCandidate(fset *token.FileSet, fn *ast.FuncDecl, fc diff.FileChange, pkgName string) []lang.UnusedSymbol {
	if fn.Recv != nil {
		return nil
	}
	if fn.Name == nil {
		return nil
	}
	name := fn.Name.Name
	if isExported(name) {
		return nil
	}
	if isFrameworkSpecialFunc(name, pkgName) {
		return nil
	}
	startLine := fset.Position(fn.Name.Pos()).Line
	if !fc.ContainsLine(startLine) {
		return nil
	}
	return []lang.UnusedSymbol{{
		File: fc.Path,
		Line: startLine,
		Name: name,
		Kind: "func",
	}}
}

// genDeclCandidates handles `var`, `const`, and `type` blocks. Each spec
// inside a block is checked independently — `var ( a = 1; b = 2 )` produces
// up to two candidates depending on which lines are in the diff. Types are
// skipped: tracking type uses through embedding, conversions, and assertions
// is more involved than identifier counting and likely to be noisy.
func genDeclCandidates(fset *token.FileSet, decl *ast.GenDecl, fc diff.FileChange) []lang.UnusedSymbol {
	var kind string
	switch decl.Tok {
	case token.VAR:
		kind = "var"
	case token.CONST:
		kind = "const"
	default:
		return nil
	}
	var out []lang.UnusedSymbol
	for _, spec := range decl.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		out = append(out, valueSpecCandidates(fset, vs, fc, kind)...)
	}
	return out
}

func valueSpecCandidates(fset *token.FileSet, vs *ast.ValueSpec, fc diff.FileChange, kind string) []lang.UnusedSymbol {
	var out []lang.UnusedSymbol
	for _, name := range vs.Names {
		if name == nil || name.Name == "_" {
			continue
		}
		if isExported(name.Name) {
			continue
		}
		line := fset.Position(name.Pos()).Line
		if !fc.ContainsLine(line) {
			continue
		}
		out = append(out, lang.UnusedSymbol{
			File: fc.Path,
			Line: line,
			Name: name.Name,
			Kind: kind,
		})
	}
	return out
}

// isExported reports whether the identifier is exported (starts with an
// uppercase letter). Mirrors token.IsExported but doesn't pull the package
// in for one trivial check.
func isExported(name string) bool {
	if name == "" {
		return false
	}
	r := []rune(name)[0]
	return unicode.IsUpper(r)
}

// isFrameworkSpecialFunc reports whether a function name is called by the
// Go runtime or the testing framework rather than by other Go source. Such
// functions have no callers in source code — flagging them would be a
// guaranteed false positive.
//
//   - init / main: package-level entry points.
//   - TestXxx, BenchmarkXxx, ExampleXxx, FuzzXxx: discovered by go test via
//     reflection over the test binary's symbol table.
func isFrameworkSpecialFunc(name, pkgName string) bool {
	switch name {
	case "init":
		return true
	case "main":
		return pkgName == "main"
	}
	for _, prefix := range []string{"Test", "Benchmark", "Example", "Fuzz"} {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		// "TestMain" and bare "Test" both qualify; what disqualifies is e.g.
		// "Testify" where the next rune is lowercase. Per go test conventions,
		// the function name must be `<Prefix>` followed by either nothing or
		// an uppercase rune.
		rest := name[len(prefix):]
		if rest == "" {
			return true
		}
		first := []rune(rest)[0]
		if unicode.IsUpper(first) || unicode.IsDigit(first) {
			return true
		}
	}
	return false
}

// scanGoPackageReferences walks every .go file in pkgDir (top-level only,
// not subdirectories — those are separate packages) and returns a map from
// identifier name to the number of NON-DECLARATION uses. The declaration
// site itself is excluded so a symbol that's declared exactly once and
// never used has refs[name] == 0.
//
// Returns an empty (non-nil) map on directory read failure so callers can
// continue without a nil check.
func scanGoPackageReferences(pkgDir string) map[string]int {
	refs := map[string]int{}
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return refs
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		countFileReferences(filepath.Join(pkgDir, e.Name()), refs)
	}
	return refs
}

// countFileReferences parses path and increments refs for every Ident node
// that is not itself a top-level declaration site. Selector expressions
// `pkg.Foo` count Foo as a reference (we walk the whole file regardless of
// where the Ident sits).
func countFileReferences(path string, refs map[string]int) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return
	}
	declSites := collectTopLevelDeclSites(file)
	ast.Inspect(file, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		if declSites[ident] {
			return true
		}
		refs[ident.Name]++
		return true
	})
}

// collectTopLevelDeclSites returns the set of *ast.Ident nodes that are the
// name of a top-level declaration in file. Used to subtract declarations
// from the reference count so a symbol declared once and used once has
// refs[name] == 1 (not 2). Local declarations inside function bodies are
// not tracked because they're caught by the Go compiler already (unused
// locals are a build error).
func collectTopLevelDeclSites(file *ast.File) map[*ast.Ident]bool {
	sites := map[*ast.Ident]bool{}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name != nil {
				sites[d.Name] = true
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				addSpecDeclSites(spec, sites)
			}
		}
	}
	return sites
}

func addSpecDeclSites(spec ast.Spec, sites map[*ast.Ident]bool) {
	switch s := spec.(type) {
	case *ast.ValueSpec:
		for _, name := range s.Names {
			if name != nil {
				sites[name] = true
			}
		}
	case *ast.TypeSpec:
		if s.Name != nil {
			sites[s.Name] = true
		}
	}
}
