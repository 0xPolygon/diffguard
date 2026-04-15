package churn

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/0xPolygon/diffguard/internal/diff"
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

// Analyze cross-references git log with cognitive complexity for changed functions.
func Analyze(repoPath string, d *diff.Result, complexityThreshold int) (report.Section, error) {
	fileCommits := collectFileCommits(repoPath, d.Files)
	results := collectChurnResults(repoPath, d.Files, fileCommits)
	return buildSection(results, complexityThreshold), nil
}

func collectFileCommits(repoPath string, files []diff.FileChange) map[string]int {
	commits := make(map[string]int)
	for _, fc := range files {
		commits[fc.Path] = countFileCommits(repoPath, fc.Path)
	}
	return commits
}

func collectChurnResults(repoPath string, files []diff.FileChange, fileCommits map[string]int) []FunctionChurn {
	var results []FunctionChurn
	for _, fc := range files {
		results = append(results, analyzeFileChurn(repoPath, fc, fileCommits[fc.Path])...)
	}
	return results
}

func analyzeFileChurn(repoPath string, fc diff.FileChange, commits int) []FunctionChurn {
	absPath := filepath.Join(repoPath, fc.Path)
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, absPath, nil, 0)
	if err != nil {
		return nil
	}

	var results []FunctionChurn
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}

		startLine := fset.Position(fn.Pos()).Line
		endLine := fset.Position(fn.End()).Line

		if !fc.OverlapsRange(startLine, endLine) {
			return false
		}

		complexity := computeComplexity(fn.Body)
		results = append(results, FunctionChurn{
			File:       fc.Path,
			Line:       startLine,
			Name:       funcName(fn),
			Commits:    commits,
			Complexity: complexity,
			Score:      float64(commits) * float64(complexity),
		})

		return false
	})
	return results
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

// computeComplexity is a simplified cognitive complexity counter.
func computeComplexity(body *ast.BlockStmt) int {
	if body == nil {
		return 0
	}
	var count int
	ast.Inspect(body, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.IfStmt:
			count++
		case *ast.ForStmt, *ast.RangeStmt:
			count++
		case *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
			count++
		case *ast.BinaryExpr:
			bin := n.(*ast.BinaryExpr)
			if bin.Op == token.LAND || bin.Op == token.LOR {
				count++
			}
		}
		return true
	})
	return count
}

func funcName(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		recv := fn.Recv.List[0]
		var typeName string
		switch t := recv.Type.(type) {
		case *ast.StarExpr:
			if ident, ok := t.X.(*ast.Ident); ok {
				typeName = ident.Name
			}
		case *ast.Ident:
			typeName = t.Name
		}
		return fmt.Sprintf("(%s).%s", typeName, fn.Name.Name)
	}
	return fn.Name.Name
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
