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
	if d.MergeBase != "" {
		funcResults, fileResults = applySizeDeltaFilter(repoPath, d, funcResults, fileResults, extractor)
	}

	return buildSection(funcResults, fileResults, funcThreshold, fileThreshold), nil
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
) ([]lang.FunctionSize, []lang.FileSize) {
	byFile := make(map[string]baseSizes)
	for _, fc := range d.Files {
		byFile[fc.Path] = baseSizesFor(repoPath, d.MergeBase, fc.Path, extractor)
	}
	return filterFuncSizes(headFuncs, byFile), filterFileSizes(headFiles, byFile)
}

// filterFuncSizes drops per-function findings whose base-side line count was
// already >= the head-side count (no growth this PR).
func filterFuncSizes(head []lang.FunctionSize, byFile map[string]baseSizes) []lang.FunctionSize {
	var out []lang.FunctionSize
	for _, h := range head {
		if !grewFunc(h, byFile[h.File]) {
			continue
		}
		out = append(out, h)
	}
	return out
}

// filterFileSizes drops per-file findings whose whole-file line count was
// already >= the head-side count.
func filterFileSizes(head []lang.FileSize, byFile map[string]baseSizes) []lang.FileSize {
	var out []lang.FileSize
	for _, h := range head {
		if !grewFile(h, byFile[h.Path]) {
			continue
		}
		out = append(out, h)
	}
	return out
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

func checkFuncSizes(funcs []lang.FunctionSize, threshold int) []report.Finding {
	var findings []report.Finding
	for _, f := range funcs {
		if f.Lines > threshold {
			findings = append(findings, report.Finding{
				File:     f.File,
				Line:     f.Line,
				Function: f.Name,
				Message:  fmt.Sprintf("function=%d lines", f.Lines),
				Value:    float64(f.Lines),
				Limit:    float64(threshold),
				Severity: report.SeverityFail,
			})
		}
	}
	return findings
}

func checkFileSizes(files []lang.FileSize, threshold int) []report.Finding {
	var findings []report.Finding
	for _, f := range files {
		if f.Lines > threshold {
			findings = append(findings, report.Finding{
				File:     f.Path,
				Message:  fmt.Sprintf("file=%d lines", f.Lines),
				Value:    float64(f.Lines),
				Limit:    float64(threshold),
				Severity: report.SeverityFail,
			})
		}
	}
	return findings
}

func buildSection(funcs []lang.FunctionSize, files []lang.FileSize, funcThreshold, fileThreshold int) report.Section {
	if len(funcs) == 0 && len(files) == 0 {
		return report.Section{
			Name:     "Code Sizes",
			Summary:  "No changed functions or files to analyze",
			Severity: report.SeverityPass,
		}
	}

	findings := append(checkFuncSizes(funcs, funcThreshold), checkFileSizes(files, fileThreshold)...)

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
