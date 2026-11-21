package mutation

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/0xPolygon/diffguard/internal/lang"
	"github.com/0xPolygon/diffguard/internal/report"
)

// buildSection formats the run. Scores cover only mutants that compiled and
// were tested: a mutant that cannot build says nothing about the tests, and
// one that never ran is reported as missing rather than guessed at.
func buildSection(mutants []Mutant, opts Options) report.Section {
	counts := countOutcomes(mutants)
	killed := counts[lang.OutcomeKilled] + counts[lang.OutcomeTimedOut]
	survived := counts[lang.OutcomeSurvived] + counts[lang.OutcomeNotCovered]
	total := killed + survived

	score := 0.0
	if total > 0 {
		score = float64(killed) / float64(total) * 100
	}

	tiers := computeTierStats(mutants)
	stats := map[string]any{
		"total":           total,
		"killed":          killed,
		"survived":        survived,
		"score":           score,
		"tiers":           tiers,
		"tier1_threshold": opts.tier1Threshold(),
		"tier2_threshold": opts.tier2Threshold(),
		"generated":       len(mutants),
		"cached":          countCached(mutants),
		"outcomes":        outcomeStats(counts),
	}

	return report.Section{
		Name:     "Mutation Testing",
		Summary:  buildTieredSummary(score, killed, total, survived, tiers) + outcomeNotes(counts, countCached(mutants)),
		Severity: tieredSeverity(tiers, opts),
		Findings: survivedFindings(mutants),
		Stats:    stats,
	}
}

func countOutcomes(mutants []Mutant) map[lang.MutantOutcome]int {
	counts := map[lang.MutantOutcome]int{}
	for _, m := range mutants {
		counts[m.outcome()]++
	}
	return counts
}

// outcomeStats keys the per-outcome counts by name for the JSON report.
func outcomeStats(counts map[lang.MutantOutcome]int) map[string]int {
	out := make(map[string]int, len(counts))
	for o, n := range counts {
		out[o.String()] = n
	}
	return out
}

func countCached(mutants []Mutant) int {
	n := 0
	for _, m := range mutants {
		if m.Cached {
			n++
		}
	}
	return n
}

// outcomeNotes lists the outcomes that are not plain kills or survivals, so
// a reader can see what the score leaves out.
func outcomeNotes(counts map[lang.MutantOutcome]int, cached int) string {
	notes := []struct {
		n     int
		label string
	}{
		{counts[lang.OutcomeTimedOut], "timed out"},
		{counts[lang.OutcomeNotCovered], "not covered"},
		{counts[lang.OutcomeBuildFailed], "do not compile (excluded)"},
		{counts[lang.OutcomeNotApplied], "not applied (excluded)"},
		{counts[lang.OutcomeNotRun], "NOT RUN (budget or cancelled)"},
		{counts[lang.OutcomeStoppedEarly], "skipped (verdict settled early)"},
		{cached, "from cache"},
	}
	var sb strings.Builder
	for _, note := range notes {
		if note.n > 0 {
			fmt.Fprintf(&sb, " | %d %s", note.n, note.label)
		}
	}
	return sb.String()
}

// tieredSeverity classifies the run using per-tier thresholds:
//   - Tier 1 below threshold → FAIL (logic gaps are real bugs-in-waiting).
//   - Tier 2 below threshold → WARN (semantic gaps are often but not always
//     real, and equivalent mutants are possible).
//   - Tier 3 is informational only — its score is reported but does not
//     contribute to severity, because log/metric mutations are untestable
//     by design in most Go code.
//   - A gating tier with mutants that never ran → at least WARN: the gate
//     did not see everything it was asked to judge.
//
// Tiers with zero mutants are treated as passing (nothing to gate on).
func tieredSeverity(tiers []TierStats, opts Options) report.Severity {
	sev := report.SeverityPass
	for _, ts := range tiers {
		switch tierSeverity(ts, opts) {
		case report.SeverityFail:
			return report.SeverityFail
		case report.SeverityWarn:
			sev = report.SeverityWarn
		}
	}
	return sev
}

// tierSeverity returns the severity contribution of a single tier: FAIL for
// Tier 1 below its threshold, WARN for Tier 2 below its threshold or for an
// incomplete gating tier, PASS otherwise (including Tier 3, which is
// report-only).
func tierSeverity(ts TierStats, opts Options) report.Severity {
	threshold, gated := gatingThreshold(ts.Tier, opts)
	if !gated {
		return report.SeverityPass
	}
	if ts.Total > 0 && ts.Score < threshold {
		if ts.Tier == TierLogic {
			return report.SeverityFail
		}
		return report.SeverityWarn
	}
	if ts.NotRun > 0 {
		return report.SeverityWarn
	}
	return report.SeverityPass
}

// buildTieredSummary formats the one-line summary with overall score plus
// per-tier scores. Tiers with zero mutants are omitted to keep the line
// readable on small diffs.
func buildTieredSummary(score float64, killed, total, survived int, tiers []TierStats) string {
	parts := []string{
		fmt.Sprintf("Score: %.1f%% (%d/%d killed, %d survived)",
			score, killed, total, survived),
	}
	for _, ts := range tiers {
		if ts.Total == 0 {
			continue
		}
		parts = append(parts,
			fmt.Sprintf("T%d %s: %.1f%% (%d/%d)",
				int(ts.Tier), ts.Tier, ts.Score, ts.Killed, ts.Total))
	}
	return strings.Join(parts, " | ")
}

func survivedFindings(mutants []Mutant) []report.Finding {
	var findings []report.Finding
	for _, m := range mutants {
		label := "SURVIVED"
		switch m.outcome() {
		case lang.OutcomeSurvived:
		case lang.OutcomeNotCovered:
			label = "NOT COVERED"
		default:
			continue
		}
		findings = append(findings, report.Finding{
			File:     m.File,
			Line:     m.Line,
			Message:  fmt.Sprintf("%s: %s (%s)", label, m.Description, m.Operator),
			Severity: report.SeverityFail,
		})
	}
	slices.SortStableFunc(findings, func(a, b report.Finding) int {
		return cmp.Or(strings.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line))
	})
	return findings
}
