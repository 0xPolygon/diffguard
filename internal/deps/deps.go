package deps

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/report"
)

// Graph represents the internal package dependency graph.
type Graph struct {
	Edges      map[string]map[string]bool
	ModulePath string
}

// PackageMetrics holds coupling and instability metrics for a package.
type PackageMetrics struct {
	Package     string
	Afferent    int
	Efferent    int
	Instability float64
}

// Cycle represents a circular dependency chain.
type Cycle []string

func (c Cycle) String() string {
	return strings.Join(c, " -> ") + " -> " + c[0]
}

// SDPViolation represents a Stable Dependencies Principle violation.
type SDPViolation struct {
	Package               string
	Dependency            string
	PackageInstability    float64
	DependencyInstability float64
}

// Analyze examines import changes in the diff, builds a dependency graph,
// and reports cycles, coupling, instability, and SDP violations.
func Analyze(repoPath string, d *diff.Result) (report.Section, error) {
	modulePath, err := detectModulePath(repoPath)
	if err != nil {
		return report.Section{
			Name:     "Dependency Structure",
			Summary:  fmt.Sprintf("Could not detect module path: %v", err),
			Severity: report.SeverityWarn,
		}, nil
	}

	g := &Graph{
		Edges:      make(map[string]map[string]bool),
		ModulePath: modulePath,
	}

	changedPkgs := d.ChangedPackages()
	for _, pkg := range changedPkgs {
		scanPackageImports(g, repoPath, pkg)
	}

	cycles := detectCycles(g)
	metrics := computeMetrics(g)
	sdpViolations := detectSDPViolations(g, metrics)

	return buildSection(g, cycles, metrics, sdpViolations, changedPkgs), nil
}

func scanPackageImports(g *Graph, repoPath, pkg string) {
	absDir := filepath.Join(repoPath, pkg)
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, absDir, nil, parser.ImportsOnly)
	if err != nil {
		return
	}

	pkgImportPath := g.ModulePath + "/" + pkg
	for _, p := range pkgs {
		if strings.HasSuffix(p.Name, "_test") {
			continue
		}
		collectImports(g, p, pkgImportPath)
	}
}

func collectImports(g *Graph, p *ast.Package, pkgImportPath string) {
	for _, f := range p.Files {
		for _, imp := range f.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(importPath, g.ModulePath) {
				continue
			}
			if g.Edges[pkgImportPath] == nil {
				g.Edges[pkgImportPath] = make(map[string]bool)
			}
			g.Edges[pkgImportPath][importPath] = true
		}
	}
}

func detectModulePath(repoPath string) (string, error) {
	goModPath := filepath.Join(repoPath, "go.mod")
	content, err := readFile(goModPath)
	if err != nil {
		return "", fmt.Errorf("reading go.mod: %w", err)
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
		}
	}
	return "", fmt.Errorf("no module directive found in go.mod")
}

// detectCycles finds all cycles in the dependency graph using DFS.
func detectCycles(g *Graph) []Cycle {
	var cycles []Cycle
	visited := make(map[string]bool)
	inStack := make(map[string]bool)
	var stack []string

	var dfs func(node string)
	dfs = func(node string) {
		visited[node] = true
		inStack[node] = true
		stack = append(stack, node)

		for dep := range g.Edges[node] {
			if !visited[dep] {
				dfs(dep)
			} else if inStack[dep] {
				cycles = append(cycles, extractCycle(stack, dep))
			}
		}

		stack = stack[:len(stack)-1]
		inStack[node] = false
	}

	for node := range g.Edges {
		if !visited[node] {
			dfs(node)
		}
	}

	return cycles
}

func extractCycle(stack []string, target string) Cycle {
	var cycle Cycle
	for i := len(stack) - 1; i >= 0; i-- {
		cycle = append([]string{stack[i]}, cycle...)
		if stack[i] == target {
			break
		}
	}
	return cycle
}

// computeMetrics calculates afferent/efferent coupling and instability.
func computeMetrics(g *Graph) map[string]*PackageMetrics {
	metrics := make(map[string]*PackageMetrics)

	getOrCreate := func(pkg string) *PackageMetrics {
		if m, ok := metrics[pkg]; ok {
			return m
		}
		m := &PackageMetrics{Package: pkg}
		metrics[pkg] = m
		return m
	}

	for pkg, imports := range g.Edges {
		m := getOrCreate(pkg)
		m.Efferent = len(imports)
		for dep := range imports {
			dm := getOrCreate(dep)
			dm.Afferent++
		}
	}

	for _, m := range metrics {
		total := m.Afferent + m.Efferent
		if total > 0 {
			m.Instability = float64(m.Efferent) / float64(total)
		}
	}

	return metrics
}

func detectSDPViolations(g *Graph, metrics map[string]*PackageMetrics) []SDPViolation {
	var violations []SDPViolation
	for pkg, imports := range g.Edges {
		pkgMetric := metrics[pkg]
		if pkgMetric == nil {
			continue
		}
		violations = append(violations, checkSDPForPackage(pkgMetric, imports, metrics)...)
	}
	return violations
}

func checkSDPForPackage(pkgMetric *PackageMetrics, imports map[string]bool, metrics map[string]*PackageMetrics) []SDPViolation {
	var violations []SDPViolation
	for dep := range imports {
		depMetric := metrics[dep]
		if depMetric == nil {
			continue
		}
		if depMetric.Instability > pkgMetric.Instability {
			violations = append(violations, SDPViolation{
				Package:               pkgMetric.Package,
				Dependency:            dep,
				PackageInstability:    pkgMetric.Instability,
				DependencyInstability: depMetric.Instability,
			})
		}
	}
	return violations
}

func buildSection(g *Graph, cycles []Cycle, metrics map[string]*PackageMetrics, sdpViolations []SDPViolation, changedPkgs []string) report.Section {
	findings, sev := buildDepsFindings(g, cycles, sdpViolations)

	summary := fmt.Sprintf("%d packages analyzed | %d cycles | %d SDP violations",
		len(changedPkgs), len(cycles), len(sdpViolations))

	return report.Section{
		Name:     "Dependency Structure",
		Summary:  summary,
		Severity: sev,
		Findings: findings,
		Stats:    buildDepsStats(changedPkgs, cycles, sdpViolations, metrics),
	}
}

func buildDepsFindings(g *Graph, cycles []Cycle, sdpViolations []SDPViolation) ([]report.Finding, report.Severity) {
	var findings []report.Finding
	sev := report.SeverityPass

	for _, c := range cycles {
		sev = report.SeverityFail
		findings = append(findings, report.Finding{
			File:     c[0],
			Message:  fmt.Sprintf("circular dependency: %s", c),
			Severity: report.SeverityFail,
		})
	}

	for _, v := range sdpViolations {
		if sev != report.SeverityFail {
			sev = report.SeverityWarn
		}
		findings = append(findings, report.Finding{
			File:     trimModule(v.Package, g.ModulePath),
			Message:  fmt.Sprintf("depends on less stable %s (%.2f > %.2f)", trimModule(v.Dependency, g.ModulePath), v.DependencyInstability, v.PackageInstability),
			Value:    v.DependencyInstability - v.PackageInstability,
			Severity: report.SeverityWarn,
		})
	}

	return findings, sev
}

func buildDepsStats(changedPkgs []string, cycles []Cycle, sdpViolations []SDPViolation, metrics map[string]*PackageMetrics) map[string]any {
	var metricsList []PackageMetrics
	for _, m := range metrics {
		metricsList = append(metricsList, *m)
	}
	sort.Slice(metricsList, func(i, j int) bool {
		return metricsList[i].Instability > metricsList[j].Instability
	})
	return map[string]any{
		"packages":       len(changedPkgs),
		"cycles":         len(cycles),
		"sdp_violations": len(sdpViolations),
		"metrics":        metricsList,
	}
}

func trimModule(pkg, modulePath string) string {
	return strings.TrimPrefix(pkg, modulePath+"/")
}

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
