// Package deps runs dependency-structure analysis on the files changed in a
// diff. It relies on a language-supplied lang.ImportResolver to turn source
// files into the adjacency map the graph algorithms operate on.
//
// graph.go contains the pure-math primitives: cycle detection, coupling,
// instability, SDP violation detection. deps.go wires them up to an
// ImportResolver and builds a report.Section. Splitting the two makes the
// graph algorithms reusable for any language without dragging the
// orchestration (module-path detection, section formatting) along.
package deps

import "strings"

// Graph represents an internal package dependency graph. Nodes are
// package-level identifiers (typically the module path plus the package
// directory, e.g. "example.com/mod/internal/foo"). Edges point from
// importer to importee.
type Graph struct {
	Edges      map[string]map[string]bool
	ModulePath string
}

// PackageMetrics holds coupling and instability metrics for a package.
// Afferent = how many other packages import this one ("fan-in").
// Efferent = how many other packages this one imports ("fan-out").
// Instability = Efferent / (Afferent + Efferent), range [0,1].
type PackageMetrics struct {
	Package     string
	Afferent    int
	Efferent    int
	Instability float64
}

// Cycle represents a circular dependency chain.
type Cycle []string

// String formats the cycle as "a -> b -> c -> a" (closing back to the
// start). Used in report findings.
func (c Cycle) String() string {
	return strings.Join(c, " -> ") + " -> " + c[0]
}

// SDPViolation represents a Stable Dependencies Principle violation: a
// package with low instability (stable) imports a package with higher
// instability (unstable).
type SDPViolation struct {
	Package               string
	Dependency            string
	PackageInstability    float64
	DependencyInstability float64
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

// detectSDPViolations returns the package->dependency edges that violate
// the Stable Dependencies Principle (a package depending on something less
// stable than itself).
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

// trimModule strips the module prefix from a package path for display.
func trimModule(pkg, modulePath string) string {
	return strings.TrimPrefix(pkg, modulePath+"/")
}
