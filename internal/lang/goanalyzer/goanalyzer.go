package goanalyzer

import (
	"time"

	"github.com/0xPolygon/diffguard/internal/lang"
)

// defaultGoTestTimeout is the per-mutant test timeout applied when the
// caller did not set one in TestRunConfig. It matches the fallback the
// mutation orchestrator used before the language split so behavior is
// preserved byte-for-byte for existing Go runs.
const defaultGoTestTimeout = 30 * time.Second

// Language is the Go implementation of lang.Language. It holds no state —
// the sub-component impls are stateless too — but exists as a concrete
// type so external tests can construct one without relying on the
// side-effectful init() registration.
type Language struct{}

// Name returns the canonical language identifier used by the registry and
// by report section suffixes.
func (*Language) Name() string { return "go" }

// FileFilter returns the Go-specific file selection rules used by the diff
// parser: .go extension, _test.go files excluded from analysis.
func (*Language) FileFilter() lang.FileFilter {
	return lang.FileFilter{
		Extensions: []string{".go"},
		IsTestFile: isGoTestFile,
		DiffGlobs:  []string{"*.go"},
	}
}

// Sub-component accessors. Every method returns a fresh zero-value impl
// value, which is fine because all impls are stateless.
func (*Language) ComplexityCalculator() lang.ComplexityCalculator { return complexityImpl{} }
func (*Language) ComplexityScorer() lang.ComplexityScorer         { return complexityImpl{} }
func (*Language) FunctionExtractor() lang.FunctionExtractor       { return sizesImpl{} }
func (*Language) ImportResolver() lang.ImportResolver             { return depsImpl{} }
func (*Language) MutantGenerator() lang.MutantGenerator           { return mutantGeneratorImpl{} }
func (*Language) MutantApplier() lang.MutantApplier               { return mutantApplierImpl{} }
func (*Language) AnnotationScanner() lang.AnnotationScanner       { return annotationScannerImpl{} }
func (*Language) TestRunner() lang.TestRunner                     { return testRunnerImpl{} }

// isGoTestFile matches the historical internal/diff check: any path ending
// in `_test.go` is a test file. No magic, no parse.
func isGoTestFile(path string) bool {
	return hasSuffix(path, "_test.go")
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

// init registers the Go analyzer with the global lang registry. The blank
// import in cmd/diffguard/main.go triggers this; other binaries wishing to
// include the Go analyzer must also blank-import this package.
func init() {
	lang.Register(&Language{})
	lang.RegisterManifest("go.mod", "go")
}
