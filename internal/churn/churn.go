// Package churn cross-references git log with per-function complexity scores
// using a language-supplied lang.ComplexityScorer. The AST-level work lives
// in the language back-end (for Go: goanalyzer/complexity.go); this file
// owns the git log counting (which is language-agnostic) and the severity
// derivation.
package churn

import (
	"bufio"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
	"github.com/0xPolygon/diffguard/internal/report"
)

// FunctionChurn holds churn and complexity data for a single function.
type FunctionChurn struct {
	File       string
	Line       int
	Name       string
	Commits    int
	Complexity int
	Score      float64
}

// Analyze cross-references git log with per-function complexity scores for
// the diff's changed files.
func Analyze(repoPath string, d *diff.Result, complexityThreshold int, scorer lang.ComplexityScorer) (report.Section, error) {
	fileCommits := collectFileCommits(repoPath, d.Files)
	results, err := collectChurnResults(repoPath, d.Files, fileCommits, scorer)
	if err != nil {
		return report.Section{}, err
	}
	return buildSection(results, complexityThreshold), nil
}

func collectFileCommits(repoPath string, files []diff.FileChange) map[string]int {
	commits := make(map[string]int)
	for _, fc := range files {
		commits[fc.Path] = countFileCommits(repoPath, fc.Path)
	}
	return commits
}

func collectChurnResults(repoPath string, files []diff.FileChange, fileCommits map[string]int, scorer lang.ComplexityScorer) ([]FunctionChurn, error) {
	var results []FunctionChurn
	for _, fc := range files {
		fnResults, err := analyzeFileChurn(repoPath, fc, fileCommits[fc.Path], scorer)
		if err != nil {
			return nil, fmt.Errorf("analyzing %s: %w", fc.Path, err)
		}
		results = append(results, fnResults...)
	}
	return results, nil
}

func analyzeFileChurn(repoPath string, fc diff.FileChange, commits int, scorer lang.ComplexityScorer) ([]FunctionChurn, error) {
	absPath := filepath.Join(repoPath, fc.Path)
	scores, err := scorer.ScoreFile(absPath, fc)
	if err != nil {
		return nil, err
	}

	results := make([]FunctionChurn, 0, len(scores))
	for _, s := range scores {
		results = append(results, FunctionChurn{
			File:       s.File,
			Line:       s.Line,
			Name:       s.Name,
			Commits:    commits,
			Complexity: s.Complexity,
			Score:      float64(commits) * float64(s.Complexity),
		})
	}
	return results, nil
}

// countFileCommits counts the total number of commits that touched a file.
func countFileCommits(repoPath, filePath string) int {
	cmd := exec.Command("git", "log", "--oneline", "--follow", "--", filePath)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return 0
	}

	count := 0
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		count++
	}
	return count
}

func collectChurnFindings(results []FunctionChurn, complexityThreshold int) ([]report.Finding, int) {
	var findings []report.Finding
	var warnCount int

	limit := 10
	if len(results) < limit {
		limit = len(results)
	}

	for _, r := range results[:limit] {
		if r.Score == 0 {
			continue
		}
		sev := report.SeverityPass
		if r.Complexity > complexityThreshold && r.Commits > 5 {
			sev = report.SeverityWarn
			warnCount++
		}
		findings = append(findings, report.Finding{
			File:     r.File,
			Line:     r.Line,
			Function: r.Name,
			Message:  fmt.Sprintf("commits=%d complexity=%d score=%.0f", r.Commits, r.Complexity, r.Score),
			Value:    r.Score,
			Severity: sev,
		})
	}

	return findings, warnCount
}

func buildSection(results []FunctionChurn, complexityThreshold int) report.Section {
	if len(results) == 0 {
		return report.Section{
			Name:     "Churn-Weighted Complexity",
			Summary:  "No changed functions to analyze",
			Severity: report.SeverityPass,
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	findings, warnCount := collectChurnFindings(results, complexityThreshold)

	sev := report.SeverityPass
	if warnCount > 0 {
		sev = report.SeverityWarn
	}

	return report.Section{
		Name:     "Churn-Weighted Complexity",
		Summary:  fmt.Sprintf("%d functions analyzed | Top churn*complexity score: %s", len(results), formatTopScore(results)),
		Severity: sev,
		Findings: findings,
		Stats: map[string]any{
			"total_functions": len(results),
			"warnings":        warnCount,
		},
	}
}

func formatTopScore(results []FunctionChurn) string {
	if len(results) == 0 {
		return "N/A"
	}
	return strconv.FormatFloat(results[0].Score, 'f', 0, 64)
}
