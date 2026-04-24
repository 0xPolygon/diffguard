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

// runMutantsParallel processes mutants in two phases:
//
//  1. Prepare: ApplyMutation for every mutant and write the mutated bytes
//     to a temp file. Runs before any RunTest so ApplyMutation always sees
//     pristine source on disk — critical for temp-copy runners (TypeScript)
//     that swap the mutant over the real file during RunTest. If we
//     interleaved apply + test, a concurrent worker's ApplyMutation could
//     read a file that another worker's RunTest had temporarily mutated,
//     produce the wrong mutant (or fail to locate its target), and get
//     silently reported as SURVIVED.
//  2. Test: RunTest for each prepared mutant, capped at opts.workers().
//     The TestRunner is responsible for per-file isolation (Go uses overlay,
//     non-Go runners serialize same-file mutants via a mutex).
func runMutantsParallel(repoPath string, mutants []Mutant, l lang.Language, opts Options, workDir string) int {
	applier := l.MutantApplier()
	runner := l.TestRunner()

	prepared := prepareMutants(repoPath, mutants, applier, workDir, opts.workers())

	var wg sync.WaitGroup
	sem := make(chan struct{}, opts.workers())

	for i := range mutants {
		if prepared[i] == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			mutants[idx].Killed = runPreparedMutant(repoPath, &mutants[idx], prepared[idx], runner, opts, workDir, idx)
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

// prepareMutants applies every mutation to pristine source and stores the
// mutated bytes in per-mutant temp files. Returns a parallel slice of
// mutantFile paths; an empty string marks a skipped mutant (the applier
// declined to produce bytes — parse error, no target, equivalence, …).
//
// Apply is parallelized across mutants because no RunTest has fired yet,
// so the source files on disk are still pristine and safe for concurrent
// reads.
func prepareMutants(repoPath string, mutants []Mutant, applier lang.MutantApplier, workDir string, workers int) []string {
	prepared := make([]string, len(mutants))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for i := range mutants {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			prepared[idx] = prepareMutant(repoPath, mutants[idx], applier, workDir, idx)
		}(i)
	}
	wg.Wait()
	return prepared
}

// prepareMutant runs ApplyMutation and writes the resulting bytes to a
// mutant-specific temp file. Returns the temp file path on success, or
// "" if the mutant was skipped (apply error / nil bytes / write error).
func prepareMutant(repoPath string, m Mutant, applier lang.MutantApplier, workDir string, idx int) string {
	absPath := filepath.Join(repoPath, m.File)

	mutated, err := applier.ApplyMutation(absPath, lang.MutantSite{
		File:        m.File,
		Line:        m.Line,
		Description: m.Description,
		Operator:    m.Operator,
	})
	if err != nil || mutated == nil {
		return ""
	}

	mutantFile := filepath.Join(workDir, fmt.Sprintf("m%d%s", idx, filepath.Ext(absPath)))
	if err := os.WriteFile(mutantFile, mutated, 0644); err != nil {
		return ""
	}
	return mutantFile
}

// runPreparedMutant hands an already-written mutant file to the language's
// TestRunner. The runner returns (killed, output, err); on runner error we
// skip the mutant.
func runPreparedMutant(repoPath string, m *Mutant, mutantFile string, runner lang.TestRunner, opts Options, workDir string, idx int) bool {
	absPath := filepath.Join(repoPath, m.File)

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
