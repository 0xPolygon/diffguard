package deps

import (
	"fmt"
	"sort"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
	"github.com/0xPolygon/diffguard/internal/report"
)

// Analyze examines import changes in the diff, builds a dependency graph
// via the supplied ImportResolver, and reports cycles, coupling,
// instability, and SDP violations.
func Analyze(repoPath string, d *diff.Result, resolver lang.ImportResolver) (report.Section, error) {
	modulePath, err := resolver.DetectModulePath(repoPath)
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
		edges := resolver.ScanPackageImports(repoPath, pkg, modulePath)
		mergeEdges(g.Edges, edges)
	}

	cycles := detectCycles(g)
	metrics := computeMetrics(g)
	sdpViolations := detectSDPViolations(g, metrics)

	return buildSection(g, cycles, metrics, sdpViolations, changedPkgs), nil
}

// mergeEdges folds the resolver's per-package adjacency map into the running
// graph. Resolvers typically return a single-entry map on each call, but
// the interface is broad enough that a resolver could return edges for
// sub-packages too — so merge instead of assign.
func mergeEdges(dst, src map[string]map[string]bool) {
	for from, tos := range src {
		if dst[from] == nil {
			dst[from] = make(map[string]bool)
		}
		for to := range tos {
			dst[from][to] = true
		}
	}
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
