package deps

import (
	"testing"

	"github.com/0xPolygon/diffguard/internal/report"
)

func TestExtractCycle(t *testing.T) {
	stack := []string{"a", "b", "c", "d"}
	cycle := extractCycle(stack, "b")
	if len(cycle) != 3 {
		t.Fatalf("cycle length = %d, want 3", len(cycle))
	}
	if cycle[0] != "b" || cycle[1] != "c" || cycle[2] != "d" {
		t.Errorf("cycle = %v, want [b c d]", cycle)
	}
}

func TestExtractCycle_SelfCycle(t *testing.T) {
	stack := []string{"a"}
	cycle := extractCycle(stack, "a")
	if len(cycle) != 1 {
		t.Fatalf("cycle length = %d, want 1", len(cycle))
	}
	if cycle[0] != "a" {
		t.Errorf("cycle = %v, want [a]", cycle)
	}
}

func TestCheckSDPForPackage_NoViolation(t *testing.T) {
	// Package with instability 1.0 depending on something with instability 0.5
	// is fine (unstable depends on stable)
	pkgMetric := &PackageMetrics{Package: "unstable", Instability: 1.0}
	imports := map[string]bool{"stable": true}
	metrics := map[string]*PackageMetrics{
		"stable": {Package: "stable", Instability: 0.5},
	}

	violations := checkSDPForPackage(pkgMetric, imports, metrics)
	if len(violations) != 0 {
		t.Errorf("expected no violations, got %d", len(violations))
	}
}

func TestCheckSDPForPackage_WithViolation(t *testing.T) {
	// Stable package depending on unstable — violation
	pkgMetric := &PackageMetrics{Package: "stable", Instability: 0.2}
	imports := map[string]bool{"unstable": true}
	metrics := map[string]*PackageMetrics{
		"unstable": {Package: "unstable", Instability: 0.8},
	}

	violations := checkSDPForPackage(pkgMetric, imports, metrics)
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(violations))
	}
	if violations[0].Package != "stable" {
		t.Errorf("violation package = %q, want stable", violations[0].Package)
	}
}

func TestCheckSDPForPackage_EqualInstability(t *testing.T) {
	// Equal instability is NOT a violation (> not >=)
	pkgMetric := &PackageMetrics{Package: "a", Instability: 0.5}
	imports := map[string]bool{"b": true}
	metrics := map[string]*PackageMetrics{
		"b": {Package: "b", Instability: 0.5},
	}

	violations := checkSDPForPackage(pkgMetric, imports, metrics)
	if len(violations) != 0 {
		t.Errorf("expected no violations for equal instability, got %d", len(violations))
	}
}

func TestCheckSDPForPackage_MissingMetric(t *testing.T) {
	pkgMetric := &PackageMetrics{Package: "a", Instability: 0.5}
	imports := map[string]bool{"unknown": true}
	metrics := map[string]*PackageMetrics{} // unknown not in metrics

	violations := checkSDPForPackage(pkgMetric, imports, metrics)
	if len(violations) != 0 {
		t.Errorf("expected no violations for missing metric, got %d", len(violations))
	}
}

func TestDetectSDPViolations_MissingPkgMetric(t *testing.T) {
	g := &Graph{
		Edges: map[string]map[string]bool{
			"a": {"b": true},
		},
	}
	// metrics doesn't include "a"
	metrics := map[string]*PackageMetrics{
		"b": {Package: "b", Instability: 0.5},
	}

	violations := detectSDPViolations(g, metrics)
	if len(violations) != 0 {
		t.Errorf("expected no violations when pkg metric missing, got %d", len(violations))
	}
}

func TestBuildDepsFindings_NoCycles_NoSDP(t *testing.T) {
	g := &Graph{ModulePath: "example.com/mod"}
	findings, sev := buildDepsFindings(g, nil, nil)
	if sev != report.SeverityPass {
		t.Errorf("severity = %v, want PASS", sev)
	}
	if len(findings) != 0 {
		t.Errorf("findings = %d, want 0", len(findings))
	}
}

func TestBuildDepsFindings_WithCycles(t *testing.T) {
	g := &Graph{ModulePath: "example.com/mod"}
	cycles := []Cycle{{"a", "b"}}
	findings, sev := buildDepsFindings(g, cycles, nil)
	if sev != report.SeverityFail {
		t.Errorf("severity = %v, want FAIL", sev)
	}
	if len(findings) != 1 {
		t.Errorf("findings = %d, want 1", len(findings))
	}
}

func TestBuildDepsFindings_WithSDP(t *testing.T) {
	g := &Graph{ModulePath: "example.com/mod"}
	sdp := []SDPViolation{{
		Package:               "example.com/mod/core",
		Dependency:            "example.com/mod/unstable",
		PackageInstability:    0.2,
		DependencyInstability: 0.8,
	}}
	findings, sev := buildDepsFindings(g, nil, sdp)
	if sev != report.SeverityWarn {
		t.Errorf("severity = %v, want WARN", sev)
	}
	if len(findings) != 1 {
		t.Errorf("findings = %d, want 1", len(findings))
	}
}

func TestBuildDepsFindings_CyclesOverrideSDP(t *testing.T) {
	g := &Graph{ModulePath: "example.com/mod"}
	cycles := []Cycle{{"a", "b"}}
	sdp := []SDPViolation{{
		Package:               "example.com/mod/core",
		Dependency:            "example.com/mod/unstable",
		PackageInstability:    0.2,
		DependencyInstability: 0.8,
	}}
	_, sev := buildDepsFindings(g, cycles, sdp)
	if sev != report.SeverityFail {
		t.Errorf("severity = %v, want FAIL (cycles override SDP)", sev)
	}
}

func TestBuildDepsStats(t *testing.T) {
	metrics := map[string]*PackageMetrics{
		"a": {Package: "a", Instability: 0.5},
		"b": {Package: "b", Instability: 0.8},
	}
	stats := buildDepsStats([]string{"a", "b"}, nil, nil, metrics)

	if stats["packages"] != 2 {
		t.Errorf("packages = %v, want 2", stats["packages"])
	}
	if stats["cycles"] != 0 {
		t.Errorf("cycles = %v, want 0", stats["cycles"])
	}
}

func TestTrimModule(t *testing.T) {
	got := trimModule("example.com/mod/internal/pkg", "example.com/mod")
	if got != "internal/pkg" {
		t.Errorf("trimModule = %q, want internal/pkg", got)
	}

	got = trimModule("other/pkg", "example.com/mod")
	if got != "other/pkg" {
		t.Errorf("trimModule = %q, want other/pkg (no prefix match)", got)
	}
}

func TestComputeMetrics_ZeroCoupling(t *testing.T) {
	g := &Graph{
		Edges: map[string]map[string]bool{
			"isolated": {},
		},
	}

	metrics := computeMetrics(g)
	m := metrics["isolated"]
	if m == nil {
		t.Fatal("expected metrics for isolated package")
	}
	if m.Instability != 0 {
		t.Errorf("instability = %f, want 0 (no deps)", m.Instability)
	}
}

func TestBuildSection(t *testing.T) {
	g := &Graph{ModulePath: "example.com/mod"}
	metrics := map[string]*PackageMetrics{}
	s := buildSection(g, nil, metrics, nil, []string{"a"})

	if s.Name != "Dependency Structure" {
		t.Errorf("name = %q", s.Name)
	}
	if s.Severity != report.SeverityPass {
		t.Errorf("severity = %v, want PASS", s.Severity)
	}
}
