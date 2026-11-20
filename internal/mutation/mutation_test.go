package mutation

import (
	"errors"
	"runtime"
	"testing"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
)

// Most of what used to be tested here was the Go AST machinery:
// binaryMutants, boolMutants, applyBoolMutation, writeOverlayJSON,
// buildTestArgs, scanAnnotations, etc. All of that now lives in
// internal/lang/goanalyzer/ next to the code, and the tests moved with it.
//
// What remains here exercises the orchestration: options defaults, mutant
// sampling, tier aggregation, and section formatting.

func TestSampleMutants(t *testing.T) {
	mutants := make([]Mutant, 100)
	for i := range mutants {
		mutants[i] = Mutant{File: "test.go", Line: i}
	}

	sampled := sampleMutants(mutants, 50)
	if len(sampled) != 50 {
		t.Errorf("sampleMutants(100, 50%%) = %d, want 50", len(sampled))
	}

	full := sampleMutants(mutants, 100)
	if len(full) != 100 {
		t.Errorf("sampleMutants(100, 100%%) = %d, want 100", len(full))
	}
}

func TestOptionsTimeout_Default(t *testing.T) {
	opts := Options{}
	if opts.timeout() != defaultTestTimeout {
		t.Errorf("default timeout = %v, want %v", opts.timeout(), defaultTestTimeout)
	}
}

func TestOptionsWorkers(t *testing.T) {
	zero := Options{}
	if got, want := zero.workers(), runtime.NumCPU(); got != want {
		t.Errorf("zero workers = %d, want NumCPU = %d", got, want)
	}

	neg := Options{Workers: -4}
	if got, want := neg.workers(), runtime.NumCPU(); got != want {
		t.Errorf("negative workers = %d, want NumCPU = %d", got, want)
	}

	explicit := Options{Workers: 3}
	if got := explicit.workers(); got != 3 {
		t.Errorf("explicit workers = %d, want 3", got)
	}
}

func TestOptionsTiers(t *testing.T) {
	// Defaults kick in when thresholds are zero.
	zero := Options{}
	if got := zero.tier1Threshold(); got != defaultTier1Threshold {
		t.Errorf("tier1 default = %v, want %v", got, defaultTier1Threshold)
	}
	if got := zero.tier2Threshold(); got != defaultTier2Threshold {
		t.Errorf("tier2 default = %v, want %v", got, defaultTier2Threshold)
	}

	// Explicit values are honored.
	explicit := Options{Tier1Threshold: 75, Tier2Threshold: 50}
	if got := explicit.tier1Threshold(); got != 75 {
		t.Errorf("tier1 explicit = %v, want 75", got)
	}
	if got := explicit.tier2Threshold(); got != 50 {
		t.Errorf("tier2 explicit = %v, want 50", got)
	}
}

func TestAnalyze_PropagatesMutationCollectionErrors(t *testing.T) {
	scanErr := errors.New("synthetic annotation scan failure")
	generateErr := errors.New("synthetic mutant generation failure")

	tests := []struct {
		name       string
		scannerErr error
		generatorErr error
		wantErr    error
	}{
		{
			name:       "annotation scanner failure",
			scannerErr: scanErr,
			wantErr:    scanErr,
		},
		{
			name:         "mutant generator failure",
			generatorErr: generateErr,
			wantErr:      generateErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := &analysisErrorLanguage{
				scanner:   stubAnnotationScanner{err: tt.scannerErr},
				generator: stubMutantGenerator{err: tt.generatorErr},
			}
			d := &diff.Result{Files: []diff.FileChange{{Path: "changed.go"}}}

			_, err := Analyze(t.TempDir(), d, l, Options{})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Analyze() error = %v, want error wrapping %v", err, tt.wantErr)
			}
		})
	}
}

type stubAnnotationScanner struct {
	err error
}

func (s stubAnnotationScanner) ScanAnnotations(string) (map[int]bool, error) {
	return nil, s.err
}

type stubMutantGenerator struct {
	err error
}

func (g stubMutantGenerator) GenerateMutants(string, diff.FileChange, map[int]bool) ([]lang.MutantSite, error) {
	return nil, g.err
}

// analysisErrorLanguage exposes only the mutation collection dependencies.
// Every other capability is intentionally nil because Analyze returns before
// it can use them when collection fails.
type analysisErrorLanguage struct {
	scanner   lang.AnnotationScanner
	generator lang.MutantGenerator
}

func (l *analysisErrorLanguage) Name() string { return "analysis-error-test" }
func (l *analysisErrorLanguage) FileFilter() lang.FileFilter { return lang.FileFilter{} }
func (l *analysisErrorLanguage) ComplexityCalculator() lang.ComplexityCalculator { return nil }
func (l *analysisErrorLanguage) FunctionExtractor() lang.FunctionExtractor { return nil }
func (l *analysisErrorLanguage) ImportResolver() lang.ImportResolver { return nil }
func (l *analysisErrorLanguage) ComplexityScorer() lang.ComplexityScorer { return nil }
func (l *analysisErrorLanguage) MutantGenerator() lang.MutantGenerator { return l.generator }
func (l *analysisErrorLanguage) MutantApplier() lang.MutantApplier { return nil }
func (l *analysisErrorLanguage) AnnotationScanner() lang.AnnotationScanner { return l.scanner }
func (l *analysisErrorLanguage) TestRunner() lang.TestRunner { return nil }
func (l *analysisErrorLanguage) DeadCodeDetector() lang.DeadCodeDetector { return nil }
