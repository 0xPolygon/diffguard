package mutation

import (
	"math"
	"strings"
	"testing"

	"github.com/0xPolygon/diffguard/internal/report"
)

func TestOperatorTier(t *testing.T) {
	tests := []struct {
		op   string
		want Tier
	}{
		{"negate_conditional", TierLogic},
		{"conditional_boundary", TierLogic},
		{"return_value", TierLogic},
		{"math_operator", TierLogic},
		{"boolean_substitution", TierSemantic},
		{"incdec", TierSemantic},
		{"statement_deletion", TierObservability},
		{"branch_removal", TierObservability},
		// Unknown defaults to TierSemantic so new operators don't silently
		// land in the noise-prone tier.
		{"unknown_operator", TierSemantic},
		{"", TierSemantic},
	}
	for _, tt := range tests {
		if got := operatorTier(tt.op); got != tt.want {
			t.Errorf("operatorTier(%q) = %v, want %v", tt.op, got, tt.want)
		}
	}
}

func TestComputeTierStats(t *testing.T) {
	mutants := []Mutant{
		// Tier 1: 2/3 killed = 66.67%
		{Operator: "negate_conditional", Killed: true},
		{Operator: "conditional_boundary", Killed: true},
		{Operator: "return_value", Killed: false},
		// Tier 2: 1/2 killed = 50%
		{Operator: "boolean_substitution", Killed: true},
		{Operator: "incdec", Killed: false},
		// Tier 3: 0/4 killed = 0%
		{Operator: "statement_deletion", Killed: false},
		{Operator: "statement_deletion", Killed: false},
		{Operator: "branch_removal", Killed: false},
		{Operator: "branch_removal", Killed: false},
	}

	stats := computeTierStats(mutants)

	if len(stats) != 3 {
		t.Fatalf("expected 3 tiers, got %d", len(stats))
	}

	// Order is TierLogic, TierSemantic, TierObservability.
	checkTier(t, stats[0], TierLogic, 3, 2, 1, 66.66)
	checkTier(t, stats[1], TierSemantic, 2, 1, 1, 50.0)
	checkTier(t, stats[2], TierObservability, 4, 0, 4, 0.0)
}

func TestComputeTierStats_EmptyTiers(t *testing.T) {
	// Only Tier-1 mutants — other tiers must still appear with zero counts.
	mutants := []Mutant{
		{Operator: "negate_conditional", Killed: true},
		{Operator: "negate_conditional", Killed: true},
	}
	stats := computeTierStats(mutants)

	if stats[0].Total != 2 || stats[0].Killed != 2 {
		t.Errorf("Tier 1 stats = %+v, want total=2 killed=2", stats[0])
	}
	if stats[1].Total != 0 || stats[2].Total != 0 {
		t.Errorf("expected zero totals for unused tiers, got T2=%+v T3=%+v", stats[1], stats[2])
	}
	if stats[1].Score != 0 || stats[2].Score != 0 {
		t.Errorf("expected zero scores when Total=0, got T2=%.1f T3=%.1f",
			stats[1].Score, stats[2].Score)
	}
}

func TestTieredSeverity(t *testing.T) {
	tests := []struct {
		name   string
		tiers  []TierStats
		opts   Options
		want   report.Severity
	}{
		{
			name: "all tiers passing",
			tiers: []TierStats{
				{Tier: TierLogic, Total: 10, Killed: 10, Score: 100},
				{Tier: TierSemantic, Total: 5, Killed: 5, Score: 100},
				{Tier: TierObservability, Total: 20, Killed: 0, Score: 0},
			},
			want: report.SeverityPass,
		},
		{
			name: "tier 1 below threshold → FAIL",
			tiers: []TierStats{
				{Tier: TierLogic, Total: 10, Killed: 5, Score: 50},
				{Tier: TierSemantic, Total: 5, Killed: 5, Score: 100},
			},
			want: report.SeverityFail,
		},
		{
			name: "tier 2 below threshold → WARN",
			tiers: []TierStats{
				{Tier: TierLogic, Total: 10, Killed: 10, Score: 100},
				{Tier: TierSemantic, Total: 5, Killed: 2, Score: 40},
			},
			want: report.SeverityWarn,
		},
		{
			name: "tier 3 0% does not gate",
			tiers: []TierStats{
				{Tier: TierLogic, Total: 10, Killed: 10, Score: 100},
				{Tier: TierObservability, Total: 50, Killed: 0, Score: 0},
			},
			want: report.SeverityPass,
		},
		{
			name: "tier 1 wins over tier 2 (FAIL trumps WARN)",
			tiers: []TierStats{
				{Tier: TierLogic, Total: 10, Killed: 5, Score: 50},
				{Tier: TierSemantic, Total: 5, Killed: 2, Score: 40},
			},
			want: report.SeverityFail,
		},
		{
			name: "custom thresholds respected",
			tiers: []TierStats{
				{Tier: TierLogic, Total: 10, Killed: 7, Score: 70},
			},
			opts: Options{Tier1Threshold: 50},
			want: report.SeverityPass, // 70 >= 50, passes custom threshold
		},
		{
			name: "custom threshold triggers FAIL",
			tiers: []TierStats{
				{Tier: TierLogic, Total: 10, Killed: 9, Score: 90},
			},
			opts: Options{Tier1Threshold: 95},
			want: report.SeverityFail,
		},
		{
			name: "empty tier does not gate",
			tiers: []TierStats{
				{Tier: TierLogic, Total: 0},
				{Tier: TierSemantic, Total: 0},
			},
			want: report.SeverityPass,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tieredSeverity(tt.tiers, tt.opts)
			if got != tt.want {
				t.Errorf("tieredSeverity = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildSection_TierSummaryIncluded(t *testing.T) {
	mutants := []Mutant{
		{Operator: "negate_conditional", Killed: true},
		{Operator: "boolean_substitution", Killed: false, Description: "true -> false"},
	}
	s := buildSection(mutants, 1, Options{})

	// Summary should include both the overall score and per-tier breakdowns.
	for _, want := range []string{"Score:", "T1 logic:", "T2 semantic:"} {
		if !strings.Contains(s.Summary, want) {
			t.Errorf("summary missing %q: %s", want, s.Summary)
		}
	}
}

// TestTieredSeverity_ExactThreshold locks in that `score == threshold` passes
// (the comparison is strict `<`). Easy to break when refactoring.
func TestTieredSeverity_ExactThreshold(t *testing.T) {
	tests := []struct {
		name  string
		tiers []TierStats
		opts  Options
		want  report.Severity
	}{
		{
			name: "tier 1 exactly at default threshold (90)",
			tiers: []TierStats{
				{Tier: TierLogic, Total: 10, Killed: 9, Score: 90},
			},
			want: report.SeverityPass,
		},
		{
			name: "tier 1 one point below default threshold",
			tiers: []TierStats{
				{Tier: TierLogic, Total: 10, Killed: 8, Score: 89},
			},
			want: report.SeverityFail,
		},
		{
			name: "tier 2 exactly at default threshold (70)",
			tiers: []TierStats{
				{Tier: TierSemantic, Total: 10, Killed: 7, Score: 70},
			},
			want: report.SeverityPass,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tieredSeverity(tt.tiers, tt.opts); got != tt.want {
				t.Errorf("tieredSeverity = %v, want %v", got, tt.want)
			}
		})
	}
}

func checkTier(t *testing.T, got TierStats, wantTier Tier, wantTotal, wantKilled, wantSurvived int, wantScoreApprox float64) {
	t.Helper()
	if got.Tier != wantTier {
		t.Errorf("Tier = %v, want %v", got.Tier, wantTier)
	}
	if got.Total != wantTotal {
		t.Errorf("Total = %d, want %d", got.Total, wantTotal)
	}
	if got.Killed != wantKilled {
		t.Errorf("Killed = %d, want %d", got.Killed, wantKilled)
	}
	if got.Survived != wantSurvived {
		t.Errorf("Survived = %d, want %d", got.Survived, wantSurvived)
	}
	if math.Abs(got.Score-wantScoreApprox) > 0.5 {
		t.Errorf("Score = %.2f, want ~%.2f", got.Score, wantScoreApprox)
	}
}
