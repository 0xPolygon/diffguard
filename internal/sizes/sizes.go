// Package sizes reports function and file line counts for diff-scoped files
// using a language-supplied lang.FunctionExtractor. The per-language AST
// work lives in the language back-end (for Go: goanalyzer/sizes.go).
package sizes

import (
	"fmt"
	"path/filepath"
	"sort"

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

	return buildSection(funcResults, fileResults, funcThreshold, fileThreshold), nil
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
