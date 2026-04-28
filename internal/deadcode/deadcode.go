// Package deadcode runs a language's DeadCodeDetector across a diff's
// changed files and produces the "Dead Code" report section.
//
// The per-language detection logic (AST/CST walk, reference counting) lives
// in the language back-end (for Go: internal/lang/goanalyzer/deadcode.go;
// for TypeScript: internal/lang/tsanalyzer/deadcode.go). This package is a
// thin orchestrator: it iterates over changed files, calls the detector on
// each, and formats the resulting unused-symbol list.
//
// Dead-code findings are reported as WARN (not FAIL) because the detector
// is conservative but not omniscient — symbols can be referenced via
// reflection, framework registration, or codegen the detector can't see.
// Treating them as warnings nudges the human to verify rather than blocking
// the build outright.
package deadcode

import (
	"fmt"
	"sort"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
	"github.com/0xPolygon/diffguard/internal/report"
)

// Analyze runs detector across every file in d and returns the "Dead Code"
// report section. detector == nil produces a PASS section with a summary
// noting that the language has no detector wired up — useful as a hedge
// when adding the feature to languages incrementally.
func Analyze(repoPath string, d *diff.Result, detector lang.DeadCodeDetector) (report.Section, error) {
	if detector == nil {
		return report.Section{
			Name:     "Dead Code",
			Summary:  "No dead code detector available for this language",
			Severity: report.SeverityPass,
		}, nil
	}

	var results []lang.UnusedSymbol
	for _, fc := range d.Files {
		found, err := detector.FindDeadCode(repoPath, fc)
		if err != nil {
			return report.Section{}, fmt.Errorf("dead code analysis %s: %w", fc.Path, err)
		}
		results = append(results, found...)
	}
	return buildSection(results), nil
}

// buildSection turns a list of unused symbols into the "Dead Code" section.
// An empty list yields a PASS; any unused symbols flip the section to WARN.
// Findings are sorted by file path then line so the output is deterministic.
func buildSection(results []lang.UnusedSymbol) report.Section {
	if len(results) == 0 {
		return report.Section{
			Name:     "Dead Code",
			Summary:  "No unused symbols detected in changed code",
			Severity: report.SeverityPass,
		}
	}

	findings := make([]report.Finding, 0, len(results))
	for _, r := range results {
		findings = append(findings, report.Finding{
			File:     r.File,
			Line:     r.Line,
			Function: r.Name,
			Message:  fmt.Sprintf("unused %s %q", r.Kind, r.Name),
			Value:    1,
			Severity: report.SeverityWarn,
		})
	}
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})

	summary := fmt.Sprintf("%d unused %s detected in changed code",
		len(results), pluralize("symbol", len(results)))

	return report.Section{
		Name:     "Dead Code",
		Summary:  summary,
		Severity: report.SeverityWarn,
		Findings: findings,
		Stats: map[string]any{
			"unused_symbols": len(results),
			"by_kind":        countByKind(results),
		},
	}
}

func countByKind(results []lang.UnusedSymbol) map[string]int {
	out := map[string]int{}
	for _, r := range results {
		out[r.Kind]++
	}
	return out
}

func pluralize(word string, n int) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
