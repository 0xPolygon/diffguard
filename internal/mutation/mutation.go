package mutation

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/0xPolygon/diffguard/internal/diff"
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
	// TestTimeout is the per-mutant timeout passed to `go test -timeout`.
	// Zero means use the default (30s).
	TestTimeout time.Duration
	// TestPattern, if non-empty, is passed to `go test -run` to scope tests.
	TestPattern string
	// Tier1Threshold is the minimum killed-percentage for Tier 1 operators
	// (logic mutations) below which the section is reported as FAIL. Zero
	// falls back to defaultTier1Threshold.
	Tier1Threshold float64
	// Tier2Threshold is the minimum killed-percentage for Tier 2 operators
	// (semantic mutations) below which the section is reported as WARN. Zero
	// falls back to defaultTier2Threshold.
	Tier2Threshold float64
}

const (
	defaultTier1Threshold = 90.0
	defaultTier2Threshold = 70.0
)

func (o Options) timeout() time.Duration {
	if o.TestTimeout <= 0 {
		return 30 * time.Second
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

// Analyze applies mutation operators to changed code and runs tests.
func Analyze(repoPath string, d *diff.Result, opts Options) (report.Section, error) {
	allMutants := collectMutants(repoPath, d)

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

	killed := runMutantsParallel(repoPath, allMutants, opts)
	return buildSection(allMutants, killed, opts), nil
}

func collectMutants(repoPath string, d *diff.Result) []Mutant {
	var all []Mutant
	for _, fc := range d.Files {
		absPath := filepath.Join(repoPath, fc.Path)
		mutants, err := generateMutants(absPath, fc)
		if err != nil {
			continue
		}
		all = append(all, mutants...)
	}
	return all
}

// runMutantsParallel processes mutants in parallel, serializing within a
// package directory to avoid racing on file writes and go test runs.
// Mutants in different packages run concurrently up to runtime.NumCPU().
func runMutantsParallel(repoPath string, mutants []Mutant, opts Options) int {
	groups := groupByPackage(mutants)
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU())

	for _, group := range groups {
		wg.Add(1)
		sem <- struct{}{}
		go func(ms []*Mutant) {
			defer wg.Done()
			defer func() { <-sem }()
			for _, m := range ms {
				m.Killed = runMutant(repoPath, m, opts)
			}
		}(group)
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

func groupByPackage(mutants []Mutant) [][]*Mutant {
	byPkg := make(map[string][]*Mutant)
	for i := range mutants {
		m := &mutants[i]
		pkgDir := filepath.Dir(m.File)
		byPkg[pkgDir] = append(byPkg[pkgDir], m)
	}
	var out [][]*Mutant
	for _, g := range byPkg {
		out = append(out, g)
	}
	return out
}

// runMutant applies a mutation, runs tests, and returns whether it was killed.
func runMutant(repoPath string, m *Mutant, opts Options) bool {
	absPath := filepath.Join(repoPath, m.File)

	original, err := os.ReadFile(absPath)
	if err != nil {
		return false
	}

	mutated := applyMutation(absPath, m)
	if mutated == nil {
		return false
	}

	if err := os.WriteFile(absPath, mutated, 0644); err != nil {
		return false
	}

	pkgDir := filepath.Dir(absPath)
	cmd := exec.Command("go", buildTestArgs(opts)...)
	cmd.Dir = pkgDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()

	os.WriteFile(absPath, original, 0644)

	if err != nil {
		m.TestOutput = stderr.String()
		return true
	}
	return false
}

func buildTestArgs(opts Options) []string {
	args := []string{"test", "-count=1", "-timeout", opts.timeout().String()}
	if opts.TestPattern != "" {
		args = append(args, "-run", opts.TestPattern)
	}
	args = append(args, "./...")
	return args
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
