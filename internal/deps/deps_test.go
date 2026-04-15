package deps

import (
	"testing"
)

func TestDetectCycles_NoCycle(t *testing.T) {
	g := &Graph{
		Edges: map[string]map[string]bool{
			"a": {"b": true},
			"b": {"c": true},
			"c": {},
		},
	}

	cycles := detectCycles(g)
	if len(cycles) != 0 {
		t.Errorf("expected no cycles, got %d: %v", len(cycles), cycles)
	}
}

func TestDetectCycles_SimpleCycle(t *testing.T) {
	g := &Graph{
		Edges: map[string]map[string]bool{
			"a": {"b": true},
			"b": {"c": true},
			"c": {"a": true},
		},
	}

	cycles := detectCycles(g)
	if len(cycles) == 0 {
		t.Error("expected at least one cycle, got none")
	}
}

func TestDetectCycles_SelfCycle(t *testing.T) {
	g := &Graph{
		Edges: map[string]map[string]bool{
			"a": {"a": true},
		},
	}

	cycles := detectCycles(g)
	if len(cycles) == 0 {
		t.Error("expected self-cycle, got none")
	}
}

func TestComputeMetrics(t *testing.T) {
	g := &Graph{
		Edges: map[string]map[string]bool{
			"a": {"b": true, "c": true}, // efferent=2
			"b": {"c": true},            // efferent=1
			"c": {},                      // efferent=0
		},
	}

	metrics := computeMetrics(g)

	// Package a: efferent=2, afferent=0, instability=1.0
	if m, ok := metrics["a"]; ok {
		if m.Efferent != 2 {
			t.Errorf("a.Efferent = %d, want 2", m.Efferent)
		}
		if m.Afferent != 0 {
			t.Errorf("a.Afferent = %d, want 0", m.Afferent)
		}
		if m.Instability != 1.0 {
			t.Errorf("a.Instability = %f, want 1.0", m.Instability)
		}
	} else {
		t.Error("missing metrics for package a")
	}

	// Package b: efferent=1, afferent=1, instability=0.5
	if m, ok := metrics["b"]; ok {
		if m.Efferent != 1 {
			t.Errorf("b.Efferent = %d, want 1", m.Efferent)
		}
		if m.Afferent != 1 {
			t.Errorf("b.Afferent = %d, want 1", m.Afferent)
		}
		if m.Instability != 0.5 {
			t.Errorf("b.Instability = %f, want 0.5", m.Instability)
		}
	} else {
		t.Error("missing metrics for package b")
	}

	// Package c: efferent=0, afferent=2, instability=0.0
	if m, ok := metrics["c"]; ok {
		if m.Efferent != 0 {
			t.Errorf("c.Efferent = %d, want 0", m.Efferent)
		}
		if m.Afferent != 2 {
			t.Errorf("c.Afferent = %d, want 2", m.Afferent)
		}
		if m.Instability != 0.0 {
			t.Errorf("c.Instability = %f, want 0.0", m.Instability)
		}
	} else {
		t.Error("missing metrics for package c")
	}
}

func TestDetectSDPViolations(t *testing.T) {
	// Setup: "core" is stable (many depend on it, low instability).
	// "unstable" has high instability (depends on many, nothing depends on it).
	// "core" depends on "unstable" — this violates SDP because a stable
	// package depends on an unstable one.
	//
	// Metrics:
	//   core:     afferent=2, efferent=1, I=0.33
	//   unstable: afferent=1, efferent=1, I=0.5
	//   leaf:     afferent=1, efferent=0, I=0.0
	//   userA:    afferent=0, efferent=1, I=1.0
	//   userB:    afferent=0, efferent=1, I=1.0
	g := &Graph{
		Edges: map[string]map[string]bool{
			"core":     {"unstable": true},
			"unstable": {"leaf": true},
			"leaf":     {},
			"userA":    {"core": true},
			"userB":    {"core": true},
		},
	}

	metrics := computeMetrics(g)

	// Verify our setup: core should be more stable than unstable
	if metrics["core"].Instability >= metrics["unstable"].Instability {
		t.Fatalf("test setup wrong: core I=%.2f should be < unstable I=%.2f",
			metrics["core"].Instability, metrics["unstable"].Instability)
	}

	violations := detectSDPViolations(g, metrics)

	found := false
	for _, v := range violations {
		if v.Package == "core" && v.Dependency == "unstable" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected SDP violation for core->unstable, got violations: %+v", violations)
	}
}

func TestCycleString(t *testing.T) {
	c := Cycle{"a", "b", "c"}
	expected := "a -> b -> c -> a"
	if got := c.String(); got != expected {
		t.Errorf("Cycle.String() = %q, want %q", got, expected)
	}
}
