package mutation

import (
	"testing"

	"github.com/0xPolygon/diffguard/internal/report"
)

// TestBuildSection_HighScore confirms a fully-killed Tier-1 run reports
// PASS. This is the "100% kill rate ⇒ PASS" invariant the CI gate relies
// on.
func TestBuildSection_HighScore(t *testing.T) {
	mutants := []Mutant{
		{File: "a.go", Line: 1, Killed: true, Operator: "negate_conditional"},
		{File: "a.go", Line: 2, Killed: true, Operator: "negate_conditional"},
		{File: "a.go", Line: 3, Killed: true, Operator: "negate_conditional"},
		{File: "a.go", Line: 4, Killed: true, Operator: "negate_conditional"},
		{File: "a.go", Line: 5, Killed: true, Operator: "negate_conditional"},
	}
	s := buildSection(mutants, 5, Options{})
	if s.Severity != report.SeverityPass {
		t.Errorf("severity = %v, want PASS", s.Severity)
	}
}

// Low Tier-1 score fails the section because logic mutations surviving
// almost always indicate a real test gap.
func TestBuildSection_LowScore(t *testing.T) {
	mutants := []Mutant{
		{File: "a.go", Line: 1, Killed: true, Operator: "negate_conditional"},
		{File: "a.go", Line: 2, Killed: false, Description: "mut", Operator: "negate_conditional"},
		{File: "a.go", Line: 3, Killed: false, Description: "mut", Operator: "negate_conditional"},
		{File: "a.go", Line: 4, Killed: false, Description: "mut", Operator: "negate_conditional"},
		{File: "a.go", Line: 5, Killed: false, Description: "mut", Operator: "negate_conditional"},
	}
	s := buildSection(mutants, 1, Options{})
	if s.Severity != report.SeverityFail {
		t.Errorf("severity = %v, want FAIL", s.Severity)
	}
	if len(s.Findings) != 4 {
		t.Errorf("findings = %d, want 4", len(s.Findings))
	}
}

// Medium Tier-2 score produces a WARN but not FAIL.
func TestBuildSection_MediumScore(t *testing.T) {
	mutants := make([]Mutant, 10)
	killed := 6 // 60% — below default Tier-2 threshold of 70.
	for i := 0; i < killed; i++ {
		mutants[i] = Mutant{File: "a.go", Line: i, Killed: true, Operator: "boolean_substitution"}
	}
	for i := killed; i < 10; i++ {
		mutants[i] = Mutant{File: "a.go", Line: i, Killed: false, Description: "mut", Operator: "boolean_substitution"}
	}
	s := buildSection(mutants, killed, Options{})
	if s.Severity != report.SeverityWarn {
		t.Errorf("severity = %v, want WARN", s.Severity)
	}
}

func TestBuildSection_ZeroMutants(t *testing.T) {
	s := buildSection(nil, 0, Options{})
	if s.Severity != report.SeverityPass {
		t.Errorf("severity = %v, want PASS", s.Severity)
	}
	if s.Stats == nil {
		t.Error("expected non-nil stats")
	}
}
