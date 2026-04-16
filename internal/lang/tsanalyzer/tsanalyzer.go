package tsanalyzer

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/0xPolygon/diffguard/internal/lang"
)

// defaultTSTestTimeout is the per-mutant test timeout applied when the
// caller did not set one in TestRunConfig. npm-based runners (jest, vitest)
// spend real time on process boot + TS compile per run, so the default is
// generous.
const defaultTSTestTimeout = 120 * time.Second

// Language is the TypeScript implementation of lang.Language. Like the Go
// and Rust analyzers, it holds no state; sub-component impls are stateless.
type Language struct{}

// Name returns the canonical language identifier used by the registry and
// by report section suffixes.
func (*Language) Name() string { return "typescript" }

// FileFilter returns the TypeScript-specific file selection rules used by
// the diff parser: .ts and .tsx extensions; anything matching our test
// suffix list OR sitting under a __tests__ / __mocks__ segment counts as a
// test file. We deliberately exclude .js / .jsx / .mjs / .cjs — JS-only
// repos are out of scope for this analyzer (the detector backs that choice
// up by only firing when at least one .ts/.tsx file is present).
func (*Language) FileFilter() lang.FileFilter {
	return lang.FileFilter{
		Extensions: []string{".ts", ".tsx"},
		IsTestFile: isTSTestFile,
		DiffGlobs:  []string{"*.ts", "*.tsx"},
	}
}

// Sub-component accessors. Stateless impls return fresh zero-value structs.
func (*Language) ComplexityCalculator() lang.ComplexityCalculator { return complexityImpl{} }
func (*Language) ComplexityScorer() lang.ComplexityScorer         { return complexityImpl{} }
func (*Language) FunctionExtractor() lang.FunctionExtractor       { return sizesImpl{} }
func (*Language) ImportResolver() lang.ImportResolver             { return depsImpl{} }
func (*Language) MutantGenerator() lang.MutantGenerator           { return mutantGeneratorImpl{} }
func (*Language) MutantApplier() lang.MutantApplier               { return mutantApplierImpl{} }
func (*Language) AnnotationScanner() lang.AnnotationScanner       { return annotationScannerImpl{} }
func (*Language) TestRunner() lang.TestRunner                     { return newTestRunner() }

// isTSTestFile reports whether path is a TypeScript test file.
//
// Rules:
//   - any path segment equal to `__tests__` or `__mocks__` marks the file
//     as test-only (Jest convention).
//   - the file's basename ending in `.test.ts`, `.test.tsx`, `.spec.ts`, or
//     `.spec.tsx` marks the file as a test.
//
// Edge case explicitly called out by the spec: `utils.test-helper.ts` is
// NOT a test file. We detect test suffixes by splitting the basename on `.`
// and checking the second-to-last segment; `test-helper` lives in the last
// segment of that split (or really, it's just part of the stem and the
// final extension segment is `ts`, so the penultimate segment is
// `test-helper`, not `test`). We match only exact `test` / `spec` tokens.
func isTSTestFile(path string) bool {
	// Normalize separators so Windows-style paths behave the same.
	norm := strings.ReplaceAll(path, "\\", "/")
	for _, s := range strings.Split(norm, "/") {
		if s == "__tests__" || s == "__mocks__" {
			return true
		}
	}

	base := filepath.Base(norm)
	// Only .ts / .tsx files can be test files in our universe; the outer
	// filter strips .js anyway, but short-circuiting here keeps the token
	// check honest.
	lower := strings.ToLower(base)
	var stem string
	switch {
	case strings.HasSuffix(lower, ".tsx"):
		stem = base[:len(base)-len(".tsx")]
	case strings.HasSuffix(lower, ".ts"):
		stem = base[:len(base)-len(".ts")]
	default:
		return false
	}
	// stem is now e.g. "foo.test" or "foo.spec" or "utils.test-helper".
	// Split on `.` and inspect the last segment: it must be exactly `test`
	// or `spec` to count. This naturally excludes `test-helper`, `spec-ish`,
	// and similar false-positive stems.
	dot := strings.LastIndex(stem, ".")
	if dot < 0 {
		return false
	}
	tail := stem[dot+1:]
	return tail == "test" || tail == "spec"
}

// hasTSFile reports whether the tree rooted at root contains at least one
// file whose extension matches a registered TS extension. Exposed via the
// detector hook so the language only activates on repos that actually
// ship TS — JS-only repos (package.json + only .js) remain unmatched.
func hasTSFile(root string) bool {
	found := false
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			// Skip unreadable subtrees rather than failing detection.
			return nil
		}
		if d.IsDir() {
			// Common heavy/noisy dirs we never want to traverse.
			name := d.Name()
			if name == "node_modules" || name == ".git" || name == "dist" ||
				name == "build" || name == "out" || name == "coverage" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if ext == ".ts" || ext == ".tsx" {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// init registers the TypeScript analyzer. Detection uses a custom hook
// because package.json alone is too coarse — a plain JS project would
// trigger it spuriously. We demand both package.json at the root AND at
// least one .ts/.tsx file anywhere in the tree before claiming the repo.
func init() {
	lang.Register(&Language{})
	lang.RegisterDetector("typescript", func(repoPath string) bool {
		if _, err := os.Stat(filepath.Join(repoPath, "package.json")); err != nil {
			return false
		}
		return hasTSFile(repoPath)
	})
}
