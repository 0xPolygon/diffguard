package mutation

// Tier classifies mutation operators by how much signal a surviving mutant
// carries.
//
// The raw "score" metric across all operators is misleading on Go codebases
// because observability-heavy code (log.*, metrics.*) generates many
// statement_deletion and branch_removal survivors that tests cannot observe
// by design. Tiering lets CI gate strictly on the operators that almost
// always indicate a real test gap, while reporting the noisier operators
// separately.
type Tier int

const (
	// TierLogic (1) covers operators where a surviving mutant almost always
	// indicates a real test gap: negated comparisons, flipped conditional
	// boundaries, zeroed return values, and arithmetic swaps.
	TierLogic Tier = 1

	// TierSemantic (2) covers operators that usually indicate a gap but
	// have legitimate equivalent-mutant cases (e.g. bool substitutions on
	// default values, inc/dec on cosmetic counters). Unknown operators
	// default here so a newly-added operator doesn't silently land in the
	// noise-prone observability tier.
	TierSemantic Tier = 2

	// TierObservability (3) covers operators dominated by untestable
	// side-effect removal (log/metric calls, log-only branches). Surfaced
	// for manual review but not meant to gate CI.
	TierObservability Tier = 3
)

// String returns a short human label for the tier.
func (t Tier) String() string {
	switch t {
	case TierLogic:
		return "logic"
	case TierSemantic:
		return "semantic"
	case TierObservability:
		return "observability"
	}
	return "unknown"
}

// operatorTier maps a mutation operator name (as set on Mutant.Operator) to
// its tier. Unknown operators default to TierSemantic so a new operator
// doesn't silently become report-only noise.
//
// Language-specific operators are listed alongside the canonical ones — the
// tier reflects the signal quality of a surviving mutant, which is a
// property of what the mutation encodes rather than which language it
// targets. Rust's `unwrap_removal` and `some_to_none` strip error-handling
// that well-tested code almost always exercises, so both sit in Tier 1
// alongside negate_conditional. Rust's `question_mark_removal` also
// disables error propagation but equivalent-mutant rate is higher (early
// returns can be substituted by the caller's own match), so it lands in
// Tier 2.
func operatorTier(op string) Tier {
	switch op {
	case "negate_conditional", "conditional_boundary", "return_value", "math_operator",
		"unwrap_removal", "some_to_none":
		return TierLogic
	case "boolean_substitution", "incdec",
		"question_mark_removal":
		return TierSemantic
	case "statement_deletion", "branch_removal":
		return TierObservability
	default:
		return TierSemantic
	}
}

// TierStats aggregates mutant counts for a single tier.
type TierStats struct {
	Tier     Tier    `json:"tier"`
	Total    int     `json:"total"`
	Killed   int     `json:"killed"`
	Survived int     `json:"survived"`
	// Score is killed / total * 100; 0 when total == 0.
	Score float64 `json:"score"`
}

// computeTierStats groups mutants by tier and computes per-tier totals.
// The returned slice is ordered TierLogic, TierSemantic, TierObservability.
// operatorTier always returns one of those three, so every mutant lands in
// a bucket.
func computeTierStats(mutants []Mutant) []TierStats {
	order := []Tier{TierLogic, TierSemantic, TierObservability}
	stats := make(map[Tier]*TierStats, len(order))
	for _, t := range order {
		stats[t] = &TierStats{Tier: t}
	}

	for _, m := range mutants {
		s := stats[operatorTier(m.Operator)]
		s.Total++
		if m.Killed {
			s.Killed++
		} else {
			s.Survived++
		}
	}

	out := make([]TierStats, 0, len(order))
	for _, t := range order {
		s := stats[t]
		if s.Total > 0 {
			s.Score = float64(s.Killed) / float64(s.Total) * 100
		}
		out = append(out, *s)
	}
	return out
}
