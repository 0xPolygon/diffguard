// Package sizes reports function and file line counts for diff-scoped files
// using a language-supplied lang.FunctionExtractor. The per-language AST
// work lives in the language back-end (for Go: goanalyzer/sizes.go).
package sizes

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/0xPolygon/diffguard/internal/baseline"
	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
	"github.com/0xPolygon/diffguard/internal/report"
)

// Analyze measures lines of code for changed functions and files using the
// supplied language extractor.
func Analyze(repoPath string, d *diff.Result, funcThreshold, fileThreshold int, extractor lang.FunctionExtractor) (report.Section, error) {
	var funcResults []lang.FunctionSize
	var fileResults []lang.FileSize

	for _, fc := range d.Files {
		absPath := filepath.Join(repoPath, fc.Path)
		funcs, fileSize, err := extractor.ExtractFunctions(absPath, fc)
		if err != nil {
			return report.Section{}, fmt.Errorf("analyzing %s: %w", fc.Path, err)
		}
		funcResults = append(funcResults, funcs...)
		if fileSize != nil {
			fileResults = append(fileResults, *fileSize)
		}
	}

	// Delta gating mirrors the complexity gate: in diff mode, a PR is only
	// blamed for size violations it caused. Per-function: drop if base lines
	// >= head lines. Per-file: drop if base lines >= head lines (touching a
	// 4000-line file without growing it is not a regression).
	//
	// candidate{Funcs,Files} is the post-filter subset eligible to become
	// findings; the original {func,file}Results stay in scope so the section's
	// summary describes the whole diff, not just the worsened subset. Without
	// this split, "0 over threshold" tests against an empty list and the
	// section misreports "No changed functions or files to analyze" when
	// every legacy violation got correctly filtered out.
	candidateFuncs := funcResults
	candidateFiles := fileResults
	var funcDeltas map[string]map[string]int
	var fileDeltas map[string]int
	if d.MergeBase != "" {
		candidateFuncs, candidateFiles, funcDeltas, fileDeltas = applySizeDeltaFilter(repoPath, d, funcResults, fileResults, extractor)
	}

	return buildSection(funcResults, fileResults, candidateFuncs, candidateFiles, funcThreshold, fileThreshold, funcDeltas, fileDeltas), nil
}

// applySizeDeltaFilter drops per-function and per-file findings whose
// underlying line count did not grow vs. the merge-base. Files/functions that
// did not exist at base are kept as-is so absolute thresholds still gate
// brand-new code.
func applySizeDeltaFilter(
	repoPath string,
	d *diff.Result,
	headFuncs []lang.FunctionSize,
	headFiles []lang.FileSize,
	extractor lang.FunctionExtractor,
) ([]lang.FunctionSize, []lang.FileSize, map[string]map[string]int, map[string]int) {
	byFile := make(map[string]baseSizes)
	for _, fc := range d.Files {
		byFile[fc.Path] = baseSizesFor(repoPath, d.MergeBase, fc.Path, extractor)
	}
	keptFuncs, funcDeltas := filterFuncSizes(headFuncs, byFile)
	keptFiles, fileDeltas := filterFileSizes(headFiles, byFile)
	return keptFuncs, keptFiles, funcDeltas, fileDeltas
}

// filterFuncSizes drops per-function findings whose base-side line count was
// already >= the head-side count (no growth this PR). Returns both the kept
// findings and a delta map (file -> name -> base lines) so the message
// formatter can render "+N vs base" alongside the head value.
func filterFuncSizes(head []lang.FunctionSize, byFile map[string]baseSizes) ([]lang.FunctionSize, map[string]map[string]int) {
	var out []lang.FunctionSize
	deltas := make(map[string]map[string]int)
	for _, h := range head {
		b := byFile[h.File]
		if !grewFunc(h, b) {
			continue
		}
		out = append(out, h)
		recordFuncDelta(deltas, h, b)
	}
	return out, deltas
}

// recordFuncDelta records the base line count for a kept finding so the
// report can render "+N vs base". Brand-new functions (no base entry) skip
// the record so the message stays bare.
func recordFuncDelta(deltas map[string]map[string]int, h lang.FunctionSize, b baseSizes) {
	if !b.ok {
		return
	}
	base, exists := b.funcs[h.Name]
	if !exists {
		return
	}
	if deltas[h.File] == nil {
		deltas[h.File] = make(map[string]int)
	}
	deltas[h.File][h.Name] = base
}

// filterFileSizes drops per-file findings whose whole-file line count was
// already >= the head-side count. Returns kept findings + a path -> base
// lines map for delta-aware message formatting.
func filterFileSizes(head []lang.FileSize, byFile map[string]baseSizes) ([]lang.FileSize, map[string]int) {
	var out []lang.FileSize
	deltas := make(map[string]int)
	for _, h := range head {
		b := byFile[h.Path]
		if !grewFile(h, b) {
			continue
		}
		out = append(out, h)
		if b.ok {
			deltas[h.Path] = b.file
		}
	}
	return out, deltas
}

// grewFunc reports whether a function-size finding represents real growth on
// this branch. Missing baseline (file absent at base, or function not present
// at base) counts as growth so absolute thresholds still apply.
func grewFunc(h lang.FunctionSize, b baseSizes) bool {
	if !b.ok {
		return true
	}
	base, exists := b.funcs[h.Name]
	if !exists {
		return true
	}
	return h.Lines > base
}

// grewFile mirrors grewFunc for whole-file size: missing baseline → growth;
// otherwise growth iff head exceeds base.
func grewFile(h lang.FileSize, b baseSizes) bool {
	if !b.ok {
		return true
	}
	return h.Lines > b.file
}

// baseSizes captures the per-function and whole-file line counts of a single
// repo-relative path at the merge-base. ok=false means "no baseline" (file
// absent at base, or extractor failed) — callers should treat that as
// "compare nothing" so head findings stay in place.
type baseSizes struct {
	funcs map[string]int
	file  int
	ok    bool
}

func baseSizesFor(repoPath, ref, repoRelPath string, extractor lang.FunctionExtractor) baseSizes {
	tmp, err := baseline.FetchToTemp(repoPath, ref, repoRelPath)
	if err != nil || tmp == "" {
		return baseSizes{}
	}
	defer os.Remove(tmp)

	baseFuncs, baseFile, err := extractor.ExtractFunctions(tmp, baseline.FullCoverage(repoRelPath))
	if err != nil {
		return baseSizes{}
	}
	out := baseSizes{
		funcs: make(map[string]int, len(baseFuncs)),
		ok:    true,
	}
	for _, f := range baseFuncs {
		out.funcs[f.Name] = f.Lines
	}
	if baseFile != nil {
		out.file = baseFile.Lines
	}
	return out
}

func checkFuncSizes(funcs []lang.FunctionSize, threshold int, deltas map[string]map[string]int) []report.Finding {
	var findings []report.Finding
	for _, f := range funcs {
		if f.Lines > threshold {
			findings = append(findings, report.Finding{
				File:     f.File,
				Line:     f.Line,
				Function: f.Name,
				Message:  formatFuncSizeMsg(f, deltas),
				Value:    float64(f.Lines),
				Limit:    float64(threshold),
				Severity: report.SeverityFail,
			})
		}
	}
	return findings
}

func checkFileSizes(files []lang.FileSize, threshold int, deltas map[string]int) []report.Finding {
	var findings []report.Finding
	for _, f := range files {
		if f.Lines > threshold {
			findings = append(findings, report.Finding{
				File:     f.Path,
				Message:  formatFileSizeMsg(f, deltas),
				Value:    float64(f.Lines),
				Limit:    float64(threshold),
				Severity: report.SeverityFail,
			})
		}
	}
	return findings
}

// formatFuncSizeMsg renders the per-function size message with a "+Δ vs
// base" suffix when the function existed at base. New functions get the
// bare form so the report distinguishes "added a 200-line function" from
// "made an existing 195-line function 200 lines".
func formatFuncSizeMsg(f lang.FunctionSize, deltas map[string]map[string]int) string {
	base, ok := deltas[f.File][f.Name]
	if !ok {
		return fmt.Sprintf("function=%d lines", f.Lines)
	}
	return fmt.Sprintf("function=%d lines (+%d vs base)", f.Lines, f.Lines-base)
}

// formatFileSizeMsg mirrors formatFuncSizeMsg for whole-file size.
func formatFileSizeMsg(f lang.FileSize, deltas map[string]int) string {
	base, ok := deltas[f.Path]
	if !ok {
		return fmt.Sprintf("file=%d lines", f.Lines)
	}
	return fmt.Sprintf("file=%d lines (+%d vs base)", f.Lines, f.Lines-base)
}

// buildSection renders the section. {func,file}s are everything the language
// extractor returned (used for stats); candidate{Funcs,Files} is the subset
// eligible to become findings. In refactoring mode they're identical; in
// diff mode candidate{Funcs,Files} is the post-delta-filter list.
func buildSection(
	funcs []lang.FunctionSize,
	files []lang.FileSize,
	candidateFuncs []lang.FunctionSize,
	candidateFiles []lang.FileSize,
	funcThreshold, fileThreshold int,
	funcDeltas map[string]map[string]int,
	fileDeltas map[string]int,
) report.Section {
	if len(funcs) == 0 && len(files) == 0 {
		return report.Section{
			Name:     "Code Sizes",
			Summary:  "No changed functions or files to analyze",
			Severity: report.SeverityPass,
		}
	}

	findings := append(checkFuncSizes(candidateFuncs, funcThreshold, funcDeltas), checkFileSizes(candidateFiles, fileThreshold, fileDeltas)...)

	sort.Slice(findings, func(i, j int) bool {
		return findings[i].Value > findings[j].Value
	})

	sev := report.SeverityPass
	if len(findings) > 0 {
		sev = report.SeverityFail
	}

	summary := fmt.Sprintf("%d functions, %d files analyzed | %d over threshold (func>%d, file>%d)",
		len(funcs), len(files), len(findings), funcThreshold, fileThreshold)

	return report.Section{
		Name:     "Code Sizes",
		Summary:  summary,
		Severity: sev,
		Findings: findings,
		Stats: map[string]any{
			"total_functions":    len(funcs),
			"total_files":        len(files),
			"violations":         len(findings),
			"function_threshold": funcThreshold,
			"file_threshold":     fileThreshold,
		},
	}
}
