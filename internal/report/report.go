package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Severity indicates the level of a finding.
type Severity string

const (
	SeverityPass Severity = "PASS"
	SeverityWarn Severity = "WARN"
	SeverityFail Severity = "FAIL"
)

// Finding represents a single metric violation or observation.
type Finding struct {
	File     string   `json:"file"`
	Line     int      `json:"line,omitempty"`
	Function string   `json:"function,omitempty"`
	Message  string   `json:"message"`
	Value    float64  `json:"value"`
	Limit    float64  `json:"limit,omitempty"`
	Severity Severity `json:"severity"`
}

// Section represents one metric category in the report.
type Section struct {
	Name     string    `json:"name"`
	Summary  string    `json:"summary"`
	Severity Severity  `json:"severity"`
	Findings []Finding `json:"findings,omitempty"`
	Stats    any       `json:"stats,omitempty"`
}

// Report is the top-level output.
type Report struct {
	Sections []Section `json:"sections"`
}

// WorstSeverity returns the worst severity across all sections.
func (r Report) WorstSeverity() Severity {
	worst := SeverityPass
	for _, s := range r.Sections {
		if s.Severity == SeverityFail {
			return SeverityFail
		}
		if s.Severity == SeverityWarn {
			worst = SeverityWarn
		}
	}
	return worst
}

// WriteText writes a human-readable text report.
func WriteText(w io.Writer, r Report) {
	for i, s := range r.Sections {
		if i > 0 {
			fmt.Fprintln(w)
		}
		writeSection(w, s)
	}
}

func writeSection(w io.Writer, s Section) {
	fmt.Fprintf(w, "=== %s ===\n", s.Name)
	fmt.Fprintf(w, "%s  [%s]\n", s.Summary, s.Severity)

	if len(s.Findings) == 0 {
		return
	}

	fails, warns := groupFindings(s.Findings)
	writeFindingGroup(w, "Violations:", fails)
	writeFindingGroup(w, "Warnings:", warns)
}

func groupFindings(findings []Finding) (fails, warns []Finding) {
	for _, f := range findings {
		switch f.Severity {
		case SeverityFail:
			fails = append(fails, f)
		case SeverityWarn:
			warns = append(warns, f)
		}
	}
	return
}

func writeFindingGroup(w io.Writer, header string, findings []Finding) {
	if len(findings) == 0 {
		return
	}
	fmt.Fprintln(w, header)
	for _, f := range findings {
		printFinding(w, f)
	}
}

func printFinding(w io.Writer, f Finding) {
	loc := f.File
	if f.Line > 0 {
		loc = fmt.Sprintf("%s:%d", f.File, f.Line)
	}
	if f.Function != "" {
		loc = fmt.Sprintf("%s:%s", loc, f.Function)
	}
	fmt.Fprintf(w, "  %-60s %s  [%s]\n", loc, f.Message, f.Severity)
}

// WriteJSON writes a machine-readable JSON report.
func WriteJSON(w io.Writer, r Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// TopFindings returns the top N findings sorted by value descending.
func TopFindings(findings []Finding, n int) []Finding {
	sorted := make([]Finding, len(findings))
	copy(sorted, findings)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Value > sorted[j].Value
	})
	if len(sorted) > n {
		sorted = sorted[:n]
	}
	return sorted
}

// Histogram produces a simple text histogram of value distribution.
func Histogram(values []float64, buckets []float64) string {
	counts := countBuckets(values, buckets)
	return formatHistogram(counts, buckets, values)
}

func countBuckets(values []float64, buckets []float64) []int {
	counts := make([]int, len(buckets)+1)
	for _, v := range values {
		counts[bucketIndex(v, buckets)]++
	}
	return counts
}

func bucketIndex(v float64, buckets []float64) int {
	for i, b := range buckets {
		if v <= b {
			return i
		}
	}
	return len(buckets)
}

func formatHistogram(counts []int, buckets []float64, values []float64) string {
	var sb strings.Builder
	for i, b := range buckets {
		if i == 0 {
			fmt.Fprintf(&sb, "  ≤%3.0f: %s (%d)\n", b, bar(counts[i], values), counts[i])
		} else {
			fmt.Fprintf(&sb, " %3.0f-%3.0f: %s (%d)\n", buckets[i-1]+1, b, bar(counts[i], values), counts[i])
		}
	}
	fmt.Fprintf(&sb, "  >%3.0f: %s (%d)\n", buckets[len(buckets)-1], bar(counts[len(buckets)], values), counts[len(buckets)])
	return sb.String()
}

func bar(count int, total []float64) string {
	if len(total) == 0 {
		return ""
	}
	width := 40
	n := (count * width) / len(total)
	if count > 0 && n == 0 {
		n = 1
	}
	return strings.Repeat("#", n)
}
