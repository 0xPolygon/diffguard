package mutation

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0xPolygon/diffguard/internal/lang"
)

// TestRunMutantsParallel_ApplyPrecedesTest guards against the concurrency
// bug that surfaced on CI: when ApplyMutation ran interleaved with
// RunTest, a temp-copy runner (TypeScript) could have the source file
// swapped to mutant bytes while another worker's ApplyMutation was
// reading it. The applier then failed to locate its target and the mutant
// was silently classified as SURVIVED.
//
// The fix is structural — ApplyMutation for every mutant now completes
// before any RunTest fires. This test enforces that ordering using fakes
// that timestamp their activity.
func TestRunMutantsParallel_ApplyPrecedesTest(t *testing.T) {
	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "src.ts")
	pristine := []byte("pristine\n")
	if err := os.WriteFile(sourcePath, pristine, 0644); err != nil {
		t.Fatal(err)
	}

	const nMutants = 8
	mutants := make([]Mutant, nMutants)
	for i := range mutants {
		mutants[i] = Mutant{File: "src.ts", Line: i + 1, Operator: "fake"}
	}

	applier := &recordingApplier{sourcePath: sourcePath, pristine: pristine}
	runner := &swappingRunner{onStart: func() { applier.runnerStarted.Store(true) }}

	l := &fakeLanguage{applier: applier, runner: runner}
	opts := Options{Workers: 4}

	workDir := t.TempDir()
	runMutantsParallel(tmp, mutants, l, opts, workDir)

	// Every ApplyMutation call must have observed pristine source: if any
	// applier read happened after any runner swap-in, the bytes would
	// differ.
	if bad := applier.readsAfterAnyRunnerStart.Load(); bad > 0 {
		t.Fatalf("%d ApplyMutation calls ran after RunTest started; "+
			"apply must complete before any test swaps the file", bad)
	}
	if applier.nonPristineReads.Load() > 0 {
		t.Fatalf("ApplyMutation observed non-pristine source %d times",
			applier.nonPristineReads.Load())
	}
	if applier.calls.Load() != int64(nMutants) {
		t.Fatalf("ApplyMutation called %d times, want %d",
			applier.calls.Load(), nMutants)
	}
	if runner.calls.Load() != int64(nMutants) {
		t.Fatalf("RunTest called %d times, want %d",
			runner.calls.Load(), nMutants)
	}
}

// recordingApplier reads the source on every ApplyMutation call and
// verifies the bytes match the pristine copy.
type recordingApplier struct {
	sourcePath string
	pristine   []byte

	calls                    atomic.Int64
	nonPristineReads         atomic.Int64
	readsAfterAnyRunnerStart atomic.Int64

	runnerStarted atomic.Bool
}

func (r *recordingApplier) ApplyMutation(absPath string, site lang.MutantSite) ([]byte, error) {
	r.calls.Add(1)
	got, err := os.ReadFile(r.sourcePath)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(got, r.pristine) {
		r.nonPristineReads.Add(1)
	}
	if r.runnerStarted.Load() {
		r.readsAfterAnyRunnerStart.Add(1)
	}
	// Return any non-nil non-empty bytes; they get written to a temp file
	// and fed to RunTest. The content doesn't matter for this test.
	return []byte("mutant\n"), nil
}

// swappingRunner simulates a temp-copy test runner (TS/Rust style) that
// writes the mutant over the original, waits briefly, then restores — the
// exact window the race exploited.
type swappingRunner struct {
	calls atomic.Int64
	mu    sync.Mutex

	onStart func()
}

func (s *swappingRunner) RunTest(cfg lang.TestRunConfig) (bool, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls.Add(1)
	if s.onStart != nil {
		s.onStart()
	}
	orig, err := os.ReadFile(cfg.OriginalFile)
	if err != nil {
		return false, "", err
	}
	mutant, err := os.ReadFile(cfg.MutantFile)
	if err != nil {
		return false, "", err
	}
	if err := os.WriteFile(cfg.OriginalFile, mutant, 0644); err != nil {
		return false, "", err
	}
	time.Sleep(5 * time.Millisecond) // widen the former race window
	if err := os.WriteFile(cfg.OriginalFile, orig, 0644); err != nil {
		return false, "", err
	}
	return true, "", nil
}

// fakeLanguage wires the applier + runner into a lang.Language. Only the
// two methods exercised by runMutantsParallel are implemented; the rest
// panic to fail loud if the orchestrator ever reaches for them.
type fakeLanguage struct {
	applier lang.MutantApplier
	runner  lang.TestRunner
}

func (l *fakeLanguage) Name() string                              { return "fake" }
func (l *fakeLanguage) FileFilter() lang.FileFilter               { return lang.FileFilter{} }
func (l *fakeLanguage) ComplexityCalculator() lang.ComplexityCalculator {
	panic("not used")
}
func (l *fakeLanguage) ComplexityScorer() lang.ComplexityScorer { panic("not used") }
func (l *fakeLanguage) FunctionExtractor() lang.FunctionExtractor {
	panic("not used")
}
func (l *fakeLanguage) ImportResolver() lang.ImportResolver     { panic("not used") }
func (l *fakeLanguage) MutantGenerator() lang.MutantGenerator   { panic("not used") }
func (l *fakeLanguage) MutantApplier() lang.MutantApplier       { return l.applier }
func (l *fakeLanguage) AnnotationScanner() lang.AnnotationScanner {
	panic("not used")
}
func (l *fakeLanguage) TestRunner() lang.TestRunner             { return l.runner }
func (l *fakeLanguage) DeadCodeDetector() lang.DeadCodeDetector { panic("not used") }
