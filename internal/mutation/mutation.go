package mutation

import (
	"bytes"
	"encoding/json"
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
	// Workers caps the number of packages processed concurrently. Zero or
	// negative means use runtime.NumCPU(). Mutants within a single package
	// always run sequentially regardless of this setting.
	Workers int
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

func (o Options) workers() int {
	if o.Workers <= 0 {
		return runtime.NumCPU()
	}
	return o.Workers
}

// Analyze applies mutation operators to changed code and runs tests.
//
// Each mutant is tested in isolation using `go test -overlay` so mutants
// never touch the real source files on disk. This means mutants can be
// fully parallelized — including mutants on the same file or package —
// up to opts.workers() concurrent go test invocations.
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

	workDir, err := os.MkdirTemp("", "diffguard-mutation-")
	if err != nil {
		return report.Section{}, fmt.Errorf("creating mutation work dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	killed := runMutantsParallel(repoPath, allMutants, opts, workDir)
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

// runMutantsParallel processes mutants fully in parallel (including mutants
// on the same file) up to opts.workers() concurrent workers. Isolation
// between mutants is provided by `go test -overlay`, not by serialization.
func runMutantsParallel(repoPath string, mutants []Mutant, opts Options, workDir string) int {
	var wg sync.WaitGroup
	sem := make(chan struct{}, opts.workers())

	for i := range mutants {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			mutants[idx].Killed = runMutant(repoPath, &mutants[idx], opts, workDir, idx)
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

// runMutant applies a mutation to a temp file, uses go test -overlay to
// have the test compile against the temp file (leaving the real source
// untouched), and returns whether any test failed.
func runMutant(repoPath string, m *Mutant, opts Options, workDir string, idx int) bool {
	absPath := filepath.Join(repoPath, m.File)

	mutated := applyMutation(absPath, m)
	if mutated == nil {
		return false
	}

	mutantFile := filepath.Join(workDir, fmt.Sprintf("m%d.go", idx))
	if err := os.WriteFile(mutantFile, mutated, 0644); err != nil {
		return false
	}

	overlayPath := filepath.Join(workDir, fmt.Sprintf("m%d-overlay.json", idx))
	if err := writeOverlayJSON(overlayPath, absPath, mutantFile); err != nil {
		return false
	}

	pkgDir := filepath.Dir(absPath)
	cmd := exec.Command("go", buildTestArgs(opts, overlayPath)...)
	cmd.Dir = pkgDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()

	if err != nil {
		m.TestOutput = stderr.String()
		return true
	}
	return false
}

// writeOverlayJSON writes a go build overlay file mapping originalPath to
// mutantPath. See `go help build` -overlay flag for format details.
func writeOverlayJSON(path, originalPath, mutantPath string) error {
	overlay := struct {
		Replace map[string]string `json:"Replace"`
	}{
		Replace: map[string]string{originalPath: mutantPath},
	}
	data, err := json.Marshal(overlay)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func buildTestArgs(opts Options, overlayPath string) []string {
	args := []string{"test", "-overlay=" + overlayPath, "-count=1", "-timeout", opts.timeout().String()}
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
