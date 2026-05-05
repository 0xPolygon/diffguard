// Package complexity runs a language's ComplexityCalculator across a diff's
// changed files and formats the results into a report.Section.
//
// All AST-level work happens in the language back-end (for Go:
// internal/lang/goanalyzer/complexity.go). This package is now a thin
// orchestrator — threshold check, severity derivation, per-language stats
// summary — so new languages inherit the analyzer for free by implementing
// lang.ComplexityCalculator.
package complexity

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"

	"github.com/0xPolygon/diffguard/internal/baseline"
	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
	"github.com/0xPolygon/diffguard/internal/report"
)

// Analyze computes cognitive complexity for all functions in the diff's
// changed regions using the supplied language calculator, then produces the
// "Cognitive Complexity" report section. Parse errors are swallowed at the
// calculator layer (returning nil) so a single malformed file doesn't fail
// the whole run.
func Analyze(repoPath string, d *diff.Result, threshold int, calc lang.ComplexityCalculator) (report.Section, error) {
	var results []lang.FunctionComplexity
	for _, fc := range d.Files {
		absPath := filepath.Join(repoPath, fc.Path)
		fnResults, err := calc.AnalyzeFile(absPath, fc)
		if err != nil {
			return report.Section{}, fmt.Errorf("analyzing %s: %w", fc.Path, err)
		}
		results = append(results, fnResults...)
	}

	// Delta gating: in diff mode, drop pre-existing violations so PRs aren't
	// blamed for legacy complexity they merely touched. A function over the
	// threshold is only flagged if its complexity is greater than its value
	// at the merge-base (or the function is brand new on this branch).
	// Refactoring mode (CollectPaths) leaves MergeBase empty and falls
	// through to absolute thresholds.
	if d.MergeBase != "" {
		results = applyDeltaFilter(repoPath, d, results, calc)
	}

	return buildSection(results, threshold), nil
}

// applyDeltaFilter drops findings whose complexity at the merge-base was
// already >= the head value — i.e. the diff did not make the function worse.
// New functions (absent at base) are kept as-is so they're still gated by the
// absolute threshold downstream.
func applyDeltaFilter(repoPath string, d *diff.Result, head []lang.FunctionComplexity, calc lang.ComplexityCalculator) []lang.FunctionComplexity {
	baseByFile := make(map[string]map[string]int)
	for _, fc := range d.Files {
		if base, ok := baseComplexity(repoPath, d.MergeBase, fc.Path, calc); ok {
			baseByFile[fc.Path] = base
		}
	}

	var out []lang.FunctionComplexity
	for _, h := range head {
		if worsened(h, baseByFile[h.File]) {
			out = append(out, h)
		}
	}
	return out
}

// worsened reports whether a head finding represents a genuine regression
// against the base map. A nil/missing entry (function did not exist at base)
// counts as worsened so brand-new over-threshold functions still fail.
func worsened(h lang.FunctionComplexity, baseFuncs map[string]int) bool {
	base, exists := baseFuncs[h.Name]
	if !exists {
		return true
	}
	return h.Complexity > base
}

// baseComplexity returns a name->complexity map for repoRelPath at ref, or
// (nil, false) if the file did not exist at ref or could not be analyzed
// (treated as "no baseline" — head findings stay as-is).
func baseComplexity(repoPath, ref, repoRelPath string, calc lang.ComplexityCalculator) (map[string]int, bool) {
	tmp, err := baseline.FetchToTemp(repoPath, ref, repoRelPath)
	if err != nil || tmp == "" {
		return nil, false
	}
	defer os.Remove(tmp)

	baseFuncs, err := calc.AnalyzeFile(tmp, baseline.FullCoverage(repoRelPath))
	if err != nil {
		return nil, false
	}
	m := make(map[string]int, len(baseFuncs))
	for _, f := range baseFuncs {
		m[f.Name] = f.Complexity
	}
	return m, true
}

func collectComplexityFindings(results []lang.FunctionComplexity, threshold int) ([]report.Finding, []float64, int) {
	var findings []report.Finding
	var values []float64
	failCount := 0

	for _, r := range results {
		values = append(values, float64(r.Complexity))
		if r.Complexity <= threshold {
			continue
		}
		failCount++
		findings = append(findings, report.Finding{
			File:     r.File,
			Line:     r.Line,
			Function: r.Name,
			Message:  fmt.Sprintf("complexity=%d", r.Complexity),
			Value:    float64(r.Complexity),
			Limit:    float64(threshold),
			Severity: report.SeverityFail,
		})
	}

	sort.Slice(findings, func(i, j int) bool {
		return findings[i].Value > findings[j].Value
	})

	return findings, values, failCount
}

func buildSection(results []lang.FunctionComplexity, threshold int) report.Section {
	if len(results) == 0 {
		return report.Section{
			Name:     "Cognitive Complexity",
			Summary:  "No changed functions to analyze",
			Severity: report.SeverityPass,
		}
	}

	findings, values, failCount := collectComplexityFindings(results, threshold)

	sev := report.SeverityPass
	if failCount > 0 {
		sev = report.SeverityFail
	}

	m, med := mean(values), median(values)
	summary := fmt.Sprintf("%d functions analyzed | Mean: %.1f | Median: %.0f | Max: %.0f | %d over threshold (%d)",
		len(results), m, med, max(values), failCount, threshold)

	return report.Section{
		Name:     "Cognitive Complexity",
		Summary:  summary,
		Severity: sev,
		Findings: findings,
		Stats: map[string]any{
			"total_functions": len(results),
			"mean":            math.Round(m*10) / 10,
			"median":          med,
			"max":             max(values),
			"violations":      failCount,
			"threshold":       threshold,
			"histogram":       report.Histogram(values, []float64{5, 10, 15, 20}),
		},
	}
}

func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func median(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := make([]float64, len(vals))
	copy(sorted, vals)
	sort.Float64s(sorted)
	n := len(sorted)
	if n%2 == 0 {
		return (sorted[n/2-1] + sorted[n/2]) / 2
	}
	return sorted[n/2]
}

func max(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	m := vals[0]
	for _, v := range vals[1:] {
		if v > m {
			m = v
		}
	}
	return m
}
