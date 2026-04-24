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
}
