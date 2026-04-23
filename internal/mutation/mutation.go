// Package mutation orchestrates mutation testing across a diff's changed
// files. The AST-level work (generating mutants, applying them, scanning
// annotations, running tests) is provided by the language back-end via
// lang.MutantGenerator / lang.MutantApplier / lang.AnnotationScanner /
// lang.TestRunner. This package owns the scheduling, tiering, and report
// formatting — pieces that don't depend on any particular language.
package mutation

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
	"github.com/0xPolygon/diffguard/internal/report"
)

// Mutant represents a single mutation applied to the source code.
type Mutant struct {
	File        string
	Line        int
	Description string
	Operator    string
	Killed      bool
	TestOutput  string
}

// Options configures a mutation testing run.
type Options struct {
	// SampleRate is the percentage (0-100) of generated mutants to actually test.
	SampleRate float64
	// TestTimeout is the per-mutant timeout.
	// Zero means use the default (30s).
	TestTimeout time.Duration
	// TestPattern, if non-empty, is passed to the language's test runner to
	// scope tests.
	TestPattern string
	// Tier1Threshold is the minimum killed-percentage for Tier 1 operators
	// below which the section is reported as FAIL. Zero falls back to
	// defaultTier1Threshold.
	Tier1Threshold float64
	// Tier2Threshold is the minimum killed-percentage for Tier 2 operators
	// below which the section is reported as WARN. Zero falls back to
	// defaultTier2Threshold.
	Tier2Threshold float64
	// Workers caps the number of mutants processed concurrently. Zero or
	// negative means use runtime.NumCPU().
	Workers int
}

const (
	defaultTier1Threshold = 90.0
	defaultTier2Threshold = 70.0
	defaultTestTimeout    = 30 * time.Second
)

func (o Options) timeout() time.Duration {
	if o.TestTimeout <= 0 {
		return defaultTestTimeout
	}
	return o.TestTimeout
}

func (o Options) tier1Threshold() float64 {
	if o.Tier1Threshold <= 0 {
		return defaultTier1Threshold
	}
	return o.Tier1Threshold
}

func (o Options) tier2Threshold() float64 {
	if o.Tier2Threshold <= 0 {
		return defaultTier2Threshold
	}
	return o.Tier2Threshold
}

func (o Options) workers() int {
	if o.Workers <= 0 {
		return runtime.NumCPU()
	}
	return o.Workers
}

// Analyze applies mutation operators to changed code (via the language's
// MutantGenerator/Applier) and runs the language's TestRunner against each
// mutant.
//
// Parallelism is controlled by Options.Workers; concurrency safety is the
// TestRunner's responsibility (Go's overlay-based runner is safe to call
// concurrently; temp-copy runners for other languages must serialize
// per-file internally).
func Analyze(repoPath string, d *diff.Result, l lang.Language, opts Options) (report.Section, error) {
	allMutants := collectMutants(repoPath, d, l)

	if len(allMutants) == 0 {
		return report.Section{
			Name:     "Mutation Testing",
			Summary:  "No mutants generated from changed code",
			Severity: report.SeverityPass,
		}, nil
	}

	if opts.SampleRate < 100 {
		allMutants = sampleMutants(allMutants, opts.SampleRate)
	}

	workDir, err := os.MkdirTemp("", "diffguard-mutation-")
	if err != nil {
		return report.Section{}, fmt.Errorf("creating mutation work dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	killed := runMutantsParallel(repoPath, allMutants, l, opts, workDir)
	return buildSection(allMutants, killed, opts), nil
}

// collectMutants gathers mutation sites for every changed file, honoring
// the language's annotation scanner so lines marked
// `// mutator-disable-*` never produce mutants.
func collectMutants(repoPath string, d *diff.Result, l lang.Language) []Mutant {
	gen := l.MutantGenerator()
	scanner := l.AnnotationScanner()

	var all []Mutant
	for _, fc := range d.Files {
		absPath := filepath.Join(repoPath, fc.Path)
		disabled, err := scanner.ScanAnnotations(absPath)
		if err != nil {
			continue
		}
		sites, err := gen.GenerateMutants(absPath, fc, disabled)
		if err != nil {
			continue
		}
		for _, s := range sites {
			all = append(all, Mutant{
				File:        s.File,
				Line:        s.Line,
				Description: s.Description,
				Operator:    s.Operator,
			})
		}
	}
	return all
}

// runMutantsParallel processes mutants concurrently up to opts.workers().
// Each mutant goes through ApplyMutation -> TestRunner.RunTest; the
// TestRunner implementation is responsible for isolating concurrent
// invocations (the Go runner uses `go test -overlay`; non-Go runners use
// per-file temp-copy + mutex).
//
// We additionally serialize ApplyMutation + RunTest per source file here:
// the in-place temp-copy runners write the mutant over the original on
// disk while the test runs, and ApplyMutation reads the source from
// disk. Without this lock, worker B's ApplyMutation can observe worker
// A's mutated bytes mid-run, producing a compound mutant whose two
// mutations cancel out and survive the test. Serializing per-file keeps
// different-file mutants running in parallel while making same-file
// mutants see pristine source each time.
func runMutantsParallel(repoPath string, mutants []Mutant, l lang.Language, opts Options, workDir string) int {
	applier := l.MutantApplier()
	runner := l.TestRunner()

	var (
		wg       sync.WaitGroup
		sem      = make(chan struct{}, opts.workers())
		lockMu   sync.Mutex
		fileLock = map[string]*sync.Mutex{}
	)

	lockFor := func(path string) *sync.Mutex {
		lockMu.Lock()
		defer lockMu.Unlock()
		m, ok := fileLock[path]
		if !ok {
			m = &sync.Mutex{}
			fileLock[path] = m
		}
		return m
	}

	for i := range mutants {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			absPath := filepath.Join(repoPath, mutants[idx].File)
			lock := lockFor(absPath)
			lock.Lock()
			defer lock.Unlock()
			mutants[idx].Killed = runMutant(repoPath, &mutants[idx], applier, runner, opts, workDir, idx)
		}(i)
	}
	wg.Wait()

	killed := 0
	for i := range mutants {
		if mutants[i].Killed {
			killed++
		}
	}
	return killed
}

// runMutant applies the mutation, writes the mutated source to a temp file
// inside workDir, and hands it to the language's TestRunner. The runner
// returns (killed, output, err); on runner error we skip the mutant.
func runMutant(repoPath string, m *Mutant, applier lang.MutantApplier, runner lang.TestRunner, opts Options, workDir string, idx int) bool {
	absPath := filepath.Join(repoPath, m.File)

	mutated, err := applier.ApplyMutation(absPath, lang.MutantSite{
		File:        m.File,
		Line:        m.Line,
		Description: m.Description,
		Operator:    m.Operator,
	})
	if err != nil || mutated == nil {
		return false
	}

	mutantFile := filepath.Join(workDir, fmt.Sprintf("m%d%s", idx, filepath.Ext(absPath)))
	if err := os.WriteFile(mutantFile, mutated, 0644); err != nil {
		return false
	}

	killed, output, err := runner.RunTest(lang.TestRunConfig{
		RepoPath:     repoPath,
		MutantFile:   mutantFile,
		OriginalFile: absPath,
		Timeout:      opts.timeout(),
		TestPattern:  opts.TestPattern,
		WorkDir:      workDir,
		Index:        idx,
	})
	if err != nil {
		return false
	}
	if killed {
		m.TestOutput = output
	}
	return killed
}

func sampleMutants(mutants []Mutant, rate float64) []Mutant {
	if rate >= 100 {
		return mutants
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	n := int(float64(len(mutants)) * rate / 100)
	if n == 0 {
		n = 1
	}
	rng.Shuffle(len(mutants), func(i, j int) {
		mutants[i], mutants[j] = mutants[j], mutants[i]
	})
	return mutants[:n]
}

func buildSection(mutants []Mutant, killed int, opts Options) report.Section {
	total := len(mutants)
	survived := total - killed

	score := 0.0
	if total > 0 {
		score = float64(killed) / float64(total) * 100
	}

	tiers := computeTierStats(mutants)
	sev := tieredSeverity(tiers, opts)
	findings := survivedFindings(mutants)

	summary := buildTieredSummary(score, killed, total, survived, tiers)

	return report.Section{
		Name:     "Mutation Testing",
		Summary:  summary,
		Severity: sev,
		Findings: findings,
		Stats: map[string]any{
			"total":           total,
			"killed":          killed,
			"survived":        survived,
			"score":           score,
			"tiers":           tiers,
			"tier1_threshold": opts.tier1Threshold(),
			"tier2_threshold": opts.tier2Threshold(),
		},
	}
}

// tieredSeverity classifies the run using per-tier thresholds:
//   - Tier 1 below threshold → FAIL (logic gaps are real bugs-in-waiting).
//   - Tier 2 below threshold → WARN (semantic gaps are often but not always
//     real, and equivalent mutants are possible).
//   - Tier 3 is informational only — its score is reported but does not
//     contribute to severity, because log/metric mutations are untestable
//     by design in most Go code.
//
// Tiers with zero mutants are treated as passing (nothing to gate on).
func tieredSeverity(tiers []TierStats, opts Options) report.Severity {
	sev := report.SeverityPass
	for _, ts := range tiers {
		if ts.Total == 0 {
			continue
		}
		switch tierSeverity(ts, opts) {
		case report.SeverityFail:
			return report.SeverityFail
		case report.SeverityWarn:
			sev = report.SeverityWarn
		}
	}
	return sev
}

// tierSeverity returns the severity contribution of a single tier: FAIL for
// Tier 1 below its threshold, WARN for Tier 2 below its threshold, PASS
// otherwise (including Tier 3, which is report-only).
func tierSeverity(ts TierStats, opts Options) report.Severity {
	switch ts.Tier {
	case TierLogic:
		if ts.Score < opts.tier1Threshold() {
			return report.SeverityFail
		}
	case TierSemantic:
		if ts.Score < opts.tier2Threshold() {
			return report.SeverityWarn
		}
	}
	return report.SeverityPass
}

// buildTieredSummary formats the one-line summary with overall score plus
// per-tier scores. Tiers with zero mutants are omitted to keep the line
// readable on small diffs.
func buildTieredSummary(score float64, killed, total, survived int, tiers []TierStats) string {
	parts := []string{
		fmt.Sprintf("Score: %.1f%% (%d/%d killed, %d survived)",
			score, killed, total, survived),
	}
	for _, ts := range tiers {
		if ts.Total == 0 {
			continue
		}
		parts = append(parts,
			fmt.Sprintf("T%d %s: %.1f%% (%d/%d)",
				int(ts.Tier), ts.Tier, ts.Score, ts.Killed, ts.Total))
	}
	return strings.Join(parts, " | ")
}

func survivedFindings(mutants []Mutant) []report.Finding {
	var findings []report.Finding
	for _, m := range mutants {
		if m.Killed {
			continue
		}
		findings = append(findings, report.Finding{
			File:     m.File,
			Line:     m.Line,
			Message:  fmt.Sprintf("SURVIVED: %s (%s)", m.Description, m.Operator),
			Severity: report.SeverityFail,
		})
	}
	return findings
}
