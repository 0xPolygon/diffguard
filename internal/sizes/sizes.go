package sizes

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/report"
)

// FunctionSize holds size info for a single function.
type FunctionSize struct {
	File  string
	Line  int
	Name  string
	Lines int
}

// FileSize holds size info for a single file.
type FileSize struct {
	Path  string
	Lines int
}

// Analyze measures lines of code for changed functions and files.
func Analyze(repoPath string, d *diff.Result, funcThreshold, fileThreshold int) (report.Section, error) {
	var funcResults []FunctionSize
	var fileResults []FileSize

	for _, fc := range d.Files {
		funcs, fileSize := analyzeFile(repoPath, fc)
		funcResults = append(funcResults, funcs...)
		if fileSize != nil {
			fileResults = append(fileResults, *fileSize)
		}
	}

	return buildSection(funcResults, fileResults, funcThreshold, fileThreshold), nil
}

func analyzeFile(repoPath string, fc diff.FileChange) ([]FunctionSize, *FileSize) {
	absPath := filepath.Join(repoPath, fc.Path)
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, absPath, nil, 0)
	if err != nil {
		return nil, nil
	}

	var fileSize *FileSize
	file := fset.File(f.Pos())
	if file != nil {
		fileSize = &FileSize{Path: fc.Path, Lines: file.LineCount()}
	}

	return collectFunctionSizes(fset, f, fc), fileSize
}

func collectFunctionSizes(fset *token.FileSet, f *ast.File, fc diff.FileChange) []FunctionSize {
	var results []FunctionSize
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
		results = append(results, FunctionSize{
			File:  fc.Path,
			Line:  startLine,
			Name:  funcName(fn),
			Lines: endLine - startLine + 1,
		})
		return false
	})
	return results
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

func checkFuncSizes(funcs []FunctionSize, threshold int) []report.Finding {
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

func checkFileSizes(files []FileSize, threshold int) []report.Finding {
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

func buildSection(funcs []FunctionSize, files []FileSize, funcThreshold, fileThreshold int) report.Section {
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
			"total_files":       len(files),
			"violations":        len(findings),
			"function_threshold": funcThreshold,
			"file_threshold":    fileThreshold,
		},
	}
}
