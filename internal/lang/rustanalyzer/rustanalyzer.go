package rustanalyzer

import (
	"strings"
	"time"

	"github.com/0xPolygon/diffguard/internal/lang"
)

// defaultRustTestTimeout is the per-mutant test timeout applied when the
// caller did not set one in TestRunConfig. Rust `cargo test` cold-starts
// are slow (compile + link per mutant) so the default is generous.
const defaultRustTestTimeout = 120 * time.Second

// Language is the Rust implementation of lang.Language. Like the Go
// analyzer, it holds no state; sub-component impls are stateless.
type Language struct{}

// Name returns the canonical language identifier used by the registry and
// by report section suffixes.
func (*Language) Name() string { return "rust" }

// FileFilter returns the Rust-specific file selection rules used by the
// diff parser: .rs extension; any path segment literally equal to `tests`
// marks the file as an integration test (i.e. excluded from analysis).
func (*Language) FileFilter() lang.FileFilter {
	return lang.FileFilter{
		Extensions: []string{".rs"},
		IsTestFile: isRustTestFile,
		DiffGlobs:  []string{"*.rs"},
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
func (*Language) DeadCodeDetector() lang.DeadCodeDetector         { return nil }

// isRustTestFile reports whether path is a Rust integration test file. The
// design doc settles this: any file whose path contains a `tests` segment
// is treated as a test file. Inline `#[cfg(test)] mod tests { ... }` stays
// ambiguous from path alone — we simply ignore those blocks during analysis
// (they sit inside ordinary source files which are still analyzed).
func isRustTestFile(path string) bool {
	// Normalize separators so Windows-style paths behave the same.
	segs := strings.Split(strings.ReplaceAll(path, "\\", "/"), "/")
	for _, s := range segs {
		if s == "tests" {
			return true
		}
	}
	return false
}

// init registers the Rust analyzer. The blank import in cmd/diffguard/main.go
// triggers this; external callers wanting Rust must also blank-import.
func init() {
	lang.Register(&Language{})
	lang.RegisterManifest("Cargo.toml", "rust")
}
