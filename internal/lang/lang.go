// Package lang defines the per-language analyzer interfaces that diffguard
// plugs into. A language implementation registers itself via Register() from
// an init() function; the diffguard CLI blank-imports each language package it
// supports so the registration happens at process start.
//
// The types and interfaces declared here are the single source of truth for
// the data passed between the diff parser, the analyzers, and the language
// back-ends. Keeping them in one package avoids import cycles (analyzer
// packages import `lang`; language packages import `lang`; neither imports
// the other).
package lang

import (
	"context"
	"io"
	"time"

	"github.com/0xPolygon/diffguard/internal/diff"
)

// FileFilter controls which files the diff parser includes and which it
// classifies as test files. A language exposes its filter as a plain value
// struct so callers can read the fields directly — the diff parser uses
// Extensions/IsTestFile/DiffGlobs during path walks.
type FileFilter struct {
	// Extensions is the list of source file extensions (including the leading
	// dot) that belong to this language, e.g. [".go"] or [".ts", ".tsx"].
	Extensions []string
	// IsTestFile reports whether the given path is a test file that should be
	// excluded from analysis.
	IsTestFile func(path string) bool
	// DiffGlobs is the list of globs passed to `git diff -- <globs>` to scope
	// the diff output to this language's files.
	DiffGlobs []string
}

// MatchesExtension reports whether path has one of the filter's source
// extensions. It does not apply the IsTestFile check.
func (f FileFilter) MatchesExtension(path string) bool {
	for _, ext := range f.Extensions {
		if hasSuffix(path, ext) {
			return true
		}
	}
	return false
}

// IncludesSource reports whether path is an analyzable source file: the
// extension matches, the file is not a test file, and it doesn't live
// under a `testdata/` directory. The testdata rule is a Go convention
// — `go build` ignores any directory literally named `testdata` — and
// we apply it cross-language so fixtures shared across analyzers
// don't get flagged as production violations.
func (f FileFilter) IncludesSource(path string) bool {
	if !f.MatchesExtension(path) {
		return false
	}
	if hasTestdataSegment(path) {
		return false
	}
	if f.IsTestFile != nil && f.IsTestFile(path) {
		return false
	}
	return true
}

// hasTestdataSegment reports whether any directory component of path is
// literally `testdata`. Normalizes Windows separators so CI and dev
// machines agree.
func hasTestdataSegment(path string) bool {
	start := 0
	for i := 0; i <= len(path); i++ {
		if i == len(path) || path[i] == '/' || path[i] == '\\' {
			if i-start == len("testdata") && path[start:i] == "testdata" {
				return true
			}
			start = i + 1
		}
	}
	return false
}

// hasSuffix is a tiny helper used to avoid pulling in strings just for this
// single call — FileFilter is referenced on hot paths (every file walked) so
// keeping the dependency list short is worthwhile.
func hasSuffix(s, suffix string) bool {
	if len(s) < len(suffix) {
		return false
	}
	return s[len(s)-len(suffix):] == suffix
}

// FunctionInfo identifies a function in a source file. It's embedded by the
// richer FunctionSize and FunctionComplexity types so analyzers can share one
// identity struct.
type FunctionInfo struct {
	File    string
	Line    int
	EndLine int
	Name    string
}

// FunctionSize holds size info for a single function.
type FunctionSize struct {
	FunctionInfo
	Lines int
}

// FileSize holds size info for a single file.
type FileSize struct {
	Path  string
	Lines int
}

// FunctionComplexity holds a complexity score for a single function. It's
// used by both the complexity analyzer and the churn analyzer (via the
// ComplexityScorer interface, which may reuse the ComplexityCalculator's
// implementation or provide a lighter approximation).
type FunctionComplexity struct {
	FunctionInfo
	Complexity int
}

// MutantSite describes a single potential mutation within changed code.
type MutantSite struct {
	File        string
	Line        int
	Description string
	Operator    string
	// Offset is the byte offset of the mutated token, when the language can
	// supply one. It tells apart several sites on one line; zero means the
	// language identifies sites by line only.
	Offset int
}

// MutantOutcome is the result of testing one mutant.
type MutantOutcome int

const (
	// OutcomeUnset means no session reported a result (legacy runners).
	OutcomeUnset MutantOutcome = iota
	// OutcomeKilled: at least one test failed against the mutant.
	OutcomeKilled
	// OutcomeSurvived: every test that ran passed.
	OutcomeSurvived
	// OutcomeTimedOut: the tests exceeded their deadline. Counted as killed.
	OutcomeTimedOut
	// OutcomeNotCovered: no test executes the mutated code. Counted as survived.
	OutcomeNotCovered
	// OutcomeBuildFailed: the mutant does not compile. Excluded from scores.
	OutcomeBuildFailed
	// OutcomeNotApplied: the mutation could not be produced. Excluded.
	OutcomeNotApplied
	// OutcomeNotRun: the budget ran out or the run was cancelled. Excluded.
	OutcomeNotRun
	// OutcomeStoppedEarly: skipped because the tier's verdict was already
	// statistically settled. Excluded.
	OutcomeStoppedEarly
	// OutcomeBaselineFailed: the tests that would judge the mutant fail
	// without any mutation, so it could not be tested. Excluded; fails the gate.
	OutcomeBaselineFailed
)

var outcomeNames = map[MutantOutcome]string{
	OutcomeKilled:         "killed",
	OutcomeSurvived:       "survived",
	OutcomeTimedOut:       "timed_out",
	OutcomeNotCovered:     "not_covered",
	OutcomeBuildFailed:    "build_failed",
	OutcomeNotApplied:     "not_applied",
	OutcomeNotRun:         "not_run",
	OutcomeStoppedEarly:   "stopped_early",
	OutcomeBaselineFailed: "baseline_failed",
}

// String returns the snake_case name used in reports and the result cache.
func (o MutantOutcome) String() string {
	if name, ok := outcomeNames[o]; ok {
		return name
	}
	return "unset"
}

// ParseMutantOutcome is the inverse of String; unknown names yield
// OutcomeUnset.
func ParseMutantOutcome(name string) MutantOutcome {
	for o, n := range outcomeNames {
		if n == name {
			return o
		}
	}
	// mutator-disable-next-line (OutcomeUnset is the zero value: zeroing the return changes nothing)
	return OutcomeUnset
}

// MutantResult is what a MutationSession reports for one mutant.
type MutantResult struct {
	Outcome MutantOutcome
	Output  string
}

// SessionConfig carries the run-wide settings a MutationSession needs.
type SessionConfig struct {
	RepoPath string
	WorkDir  string
	// Timeout caps a single mutant's test run.
	Timeout     time.Duration
	TestPattern string
	Workers     int
	// Log receives warnings about packages that could not be tested as
	// planned. Nil discards them.
	Log io.Writer
}

// MutationSession tests mutants of one run. Run may be called concurrently;
// idx indexes the sites slice the session was created with.
type MutationSession interface {
	Run(ctx context.Context, idx int) MutantResult
	Close()
}

// SessionRunner is an optional Language capability: the language tests a
// whole batch of mutants itself, so it can share builds and pick tests per
// mutant. Languages without it are driven through MutantApplier/TestRunner.
type SessionRunner interface {
	NewMutationSession(ctx context.Context, cfg SessionConfig, sites []MutantSite) (MutationSession, error)
}

// Fingerprinter is an optional Language capability used by the result
// cache: the fingerprint must change whenever anything that can alter a
// mutant's outcome in file's package changes.
type Fingerprinter interface {
	MutationFingerprint(repoPath, file string) (string, error)
}

// TestRunConfig carries the parameters needed to run tests against a single
// mutant. The set of fields is deliberately broad so temp-copy runners
// (which need WorkDir and Index to write a scratch copy) and overlay-based
// runners (which only need the MutantFile, OriginalFile, and RepoPath) can
// share one shape.
type TestRunConfig struct {
	// RepoPath is the absolute path to the repository root.
	RepoPath string
	// MutantFile is the absolute path to the file containing the mutated
	// source (usually a temp file). For languages that run tests directly on
	// the original tree this may be the path to the original file after the
	// mutation has been written to it.
	MutantFile string
	// OriginalFile is the absolute path to the original (unmutated) source
	// file. Temp-copy runners use this to restore the original after running
	// the tests.
	OriginalFile string
	// Timeout caps the test run's wall-clock duration.
	Timeout time.Duration
	// TestPattern, if non-empty, is passed to the runner's test filter flag
	// (e.g. `go test -run <pattern>`).
	TestPattern string
	// WorkDir is a writable directory private to this run, available for
	// overlay files, backups, etc.
	WorkDir string
	// Index is a monotonically-increasing identifier for the mutant within
	// the current run. Useful for naming per-mutant temp files without
	// collision.
	Index int
}

// ComplexityCalculator computes cognitive complexity per function for a
// single file's changed regions.
type ComplexityCalculator interface {
	AnalyzeFile(absPath string, fc diff.FileChange) ([]FunctionComplexity, error)
}

// ComplexityScorer is a lightweight complexity score for churn weighting. It
// may share its implementation with ComplexityCalculator or be a faster,
// coarser approximation — the churn analyzer only needs a number, not a
// categorized score.
type ComplexityScorer interface {
	ScoreFile(absPath string, fc diff.FileChange) ([]FunctionComplexity, error)
}

// FunctionExtractor parses a single file and reports its function sizes plus
// the overall file size.
type FunctionExtractor interface {
	ExtractFunctions(absPath string, fc diff.FileChange) ([]FunctionSize, *FileSize, error)
}

// ImportResolver drives the deps analyzer. DetectModulePath returns the
// project-level identifier used to classify internal vs. external imports;
// ScanPackageImports returns a per-package adjacency list keyed by the
// importing package's directory-level identifier.
type ImportResolver interface {
	DetectModulePath(repoPath string) (string, error)
	ScanPackageImports(repoPath, pkgDir, modulePath string) map[string]map[string]bool
}

// MutantGenerator returns the mutation sites produced for a single file's
// changed regions, after disabled lines have been filtered out.
type MutantGenerator interface {
	GenerateMutants(absPath string, fc diff.FileChange, disabledLines map[int]bool) ([]MutantSite, error)
}

// MutantApplier produces the mutated source bytes for a given mutation site.
// Returning nil signals "skip this mutant" — callers should not treat a nil
// return as an error.
type MutantApplier interface {
	ApplyMutation(absPath string, site MutantSite) ([]byte, error)
}

// AnnotationScanner returns the set of source lines on which mutation
// generation should be suppressed, based on in-source annotations.
type AnnotationScanner interface {
	ScanAnnotations(absPath string) (map[int]bool, error)
}

// TestRunner executes the language's test suite against a mutated source
// tree and reports whether any test failed (the mutant was "killed").
type TestRunner interface {
	RunTest(cfg TestRunConfig) (killed bool, output string, err error)
}

// UnusedSymbol describes a symbol declared in a diff's changed region that
// has no references within its analyzable scope. Reported by the dead code
// detector as a warning — false positives are possible (reflective use,
// frameworks that wire symbols at runtime) so the human is the final judge.
type UnusedSymbol struct {
	File string
	Line int
	Name string
	// Kind is the language-agnostic declaration kind: "func", "var", "const",
	// "type", "class". Languages reuse the same set so the orchestrator can
	// format a uniform message.
	Kind string
}

// DeadCodeDetector finds non-exported symbols declared inside a diff's
// changed regions that have no references in their analyzable scope. The
// implementation defines the scope: Go scans the whole package directory
// (including _test.go files); TypeScript scans the single source file
// because non-exported symbols there are file-local.
//
// The detector is deliberately conservative: it only considers symbols that
// CANNOT be referenced from outside their scope, so a "no references" result
// is a real signal rather than a guess. Exported / public symbols, methods
// (which may satisfy interfaces), and well-known framework hooks (init,
// main, TestXxx, etc.) are skipped.
type DeadCodeDetector interface {
	FindDeadCode(repoPath string, fc diff.FileChange) ([]UnusedSymbol, error)
}

// Language is the top-level per-language interface. Every language
// implementation exposes its sub-components through this one type so the
// orchestrator can iterate `for _, l := range lang.All()` and read out any
// capability it needs.
type Language interface {
	Name() string
	FileFilter() FileFilter
	ComplexityCalculator() ComplexityCalculator
	FunctionExtractor() FunctionExtractor
	ImportResolver() ImportResolver
	ComplexityScorer() ComplexityScorer
	MutantGenerator() MutantGenerator
	MutantApplier() MutantApplier
	AnnotationScanner() AnnotationScanner
	TestRunner() TestRunner
	DeadCodeDetector() DeadCodeDetector
}
