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
func Analyze(repoPath string, d *diff.Result, threshold, deltaTolerance int, calc lang.ComplexityCalculator) (report.Section, error) {
	var analyzed []lang.FunctionComplexity
	for _, fc := range d.Files {
		absPath := filepath.Join(repoPath, fc.Path)
		fnResults, err := calc.AnalyzeFile(absPath, fc)
		if err != nil {
			return report.Section{}, fmt.Errorf("analyzing %s: %w", fc.Path, err)
		}
		analyzed = append(analyzed, fnResults...)
	}

	// Delta gating: in diff mode, drop pre-existing violations so PRs aren't
	// blamed for legacy complexity they merely touched. A function over the
	// threshold is only flagged if its complexity grew by more than
	// deltaTolerance vs. the merge-base (or the function is brand new on
	// this branch). Refactoring mode (CollectPaths) leaves MergeBase empty
	// and falls through to absolute thresholds.
	//
	// `candidates` is the subset of analyzed functions eligible to become
	// findings; `analyzed` stays full so the section's mean / median / max /
	// total stats describe everything in the diff, not just the worsened
	// subset. deltas tracks (file, name) -> base complexity for those
	// candidates that existed at base, so the message shows head/base/Δ.
	candidates := analyzed
	var deltas map[string]map[string]int
	if d.MergeBase != "" {
		candidates, deltas = applyDeltaFilter(repoPath, d, analyzed, calc, deltaTolerance)
	}

	return buildSection(analyzed, candidates, threshold, deltas), nil
}

// applyDeltaFilter drops findings whose complexity at the merge-base was
// within tolerance of the head value — i.e. the diff did not measurably
// worsen the function. New functions (absent at base) are kept as-is so
// they're still gated by the absolute threshold downstream.
//
// Returned `deltas` is a (file -> name -> base complexity) map covering only
// kept findings whose function existed at base, so the report can show the
// delta alongside the head value.
func applyDeltaFilter(repoPath string, d *diff.Result, head []lang.FunctionComplexity, calc lang.ComplexityCalculator, tolerance int) ([]lang.FunctionComplexity, map[string]map[string]int) {
	baseByFile := make(map[string]map[string]int)
	for _, fc := range d.Files {
		if base, ok := baseComplexity(repoPath, d.MergeBase, fc.Path, calc); ok {
			baseByFile[fc.Path] = base
		}
	}

	var out []lang.FunctionComplexity
	deltas := make(map[string]map[string]int)
	for _, h := range head {
		baseFuncs := baseByFile[h.File]
		if !worsened(h, baseFuncs, tolerance) {
			continue
		}
		out = append(out, h)
		recordDelta(deltas, h, baseFuncs)
	}
	return out, deltas
}

// recordDelta stores the base complexity for h in deltas[h.File][h.Name] when
// the function existed at base. New functions (no entry) are skipped so the
// report can distinguish them from regressions.
func recordDelta(deltas map[string]map[string]int, h lang.FunctionComplexity, baseFuncs map[string]int) {
	base, exists := baseFuncs[h.Name]
	if !exists {
		return
	}
	if deltas[h.File] == nil {
		deltas[h.File] = make(map[string]int)
	}
	deltas[h.File][h.Name] = base
}

// worsened reports whether a head finding represents a genuine regression
// against the base map. A nil/missing entry (function did not exist at base)
// counts as worsened so brand-new over-threshold functions still fail.
// Otherwise the head value must exceed base by more than tolerance.
func worsened(h lang.FunctionComplexity, baseFuncs map[string]int, tolerance int) bool {
	base, exists := baseFuncs[h.Name]
	if !exists {
		return true
	}
	return h.Complexity > base+tolerance
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

func collectComplexityFindings(results []lang.FunctionComplexity, threshold int, deltas map[string]map[string]int) ([]report.Finding, []float64, int) {
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
			Message:  formatComplexityMsg(r, deltas),
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

// formatComplexityMsg renders the per-finding message, appending a
// "(+Δ vs base)" suffix when delta-gating recorded a baseline value for the
// function. Brand-new functions (no entry in deltas) get the bare form so
// PR authors can tell "I added this hot function" from "I made an existing
// hot function hotter".
func formatComplexityMsg(r lang.FunctionComplexity, deltas map[string]map[string]int) string {
	base, ok := deltas[r.File][r.Name]
	if !ok {
		return fmt.Sprintf("complexity=%d", r.Complexity)
	}
	return fmt.Sprintf("complexity=%d (+%d vs base)", r.Complexity, r.Complexity-base)
}

// buildSection renders the section. `analyzed` is every function the
// per-language calculator returned (used for stats / summary so the report
// describes the whole diff, not just the worsened subset). `candidates` is
// the subset eligible to become findings — in refactoring mode the two are
// the same; in diff mode candidates is the post-delta-filter list.
func buildSection(analyzed, candidates []lang.FunctionComplexity, threshold int, deltas map[string]map[string]int) report.Section {
	if len(analyzed) == 0 {
		return report.Section{
			Name:     "Cognitive Complexity",
			Summary:  "No changed functions to analyze",
			Severity: report.SeverityPass,
		}
	}

	findings, _, failCount := collectComplexityFindings(candidates, threshold, deltas)
	values := complexityValues(analyzed)

	sev := report.SeverityPass
	if failCount > 0 {
		sev = report.SeverityFail
	}

	m, med := mean(values), median(values)
	summary := fmt.Sprintf("%d functions analyzed | Mean: %.1f | Median: %.0f | Max: %.0f | %d over threshold (%d)",
		len(analyzed), m, med, max(values), failCount, threshold)

	return report.Section{
		Name:     "Cognitive Complexity",
		Summary:  summary,
		Severity: sev,
		Findings: findings,
		Stats: map[string]any{
			"total_functions": len(analyzed),
			"mean":            math.Round(m*10) / 10,
			"median":          med,
			"max":             max(values),
			"violations":      failCount,
			"threshold":       threshold,
			"histogram":       report.Histogram(values, []float64{5, 10, 15, 20}),
		},
	}
}

func complexityValues(results []lang.FunctionComplexity) []float64 {
	values := make([]float64, len(results))
	for i, r := range results {
		values[i] = float64(r.Complexity)
	}
	return values
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
