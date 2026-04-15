package report

import (
	"bytes"
	"strings"
	"testing"
)

func TestWorstSeverity(t *testing.T) {
	tests := []struct {
		name     string
		sections []Section
		want     Severity
	}{
		{"empty", nil, SeverityPass},
		{"all pass", []Section{{Severity: SeverityPass}}, SeverityPass},
		{"has warn", []Section{{Severity: SeverityPass}, {Severity: SeverityWarn}}, SeverityWarn},
		{"has fail", []Section{{Severity: SeverityWarn}, {Severity: SeverityFail}}, SeverityFail},
		{"fail first", []Section{{Severity: SeverityFail}, {Severity: SeverityPass}}, SeverityFail},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Report{Sections: tt.sections}
			if got := r.WorstSeverity(); got != tt.want {
				t.Errorf("WorstSeverity() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWriteText_WithFindings(t *testing.T) {
	r := Report{
		Sections: []Section{
			{
				Name:     "Test Section",
				Summary:  "1 issue found",
				Severity: SeverityFail,
				Findings: []Finding{
					{File: "a.go", Line: 10, Function: "Foo", Message: "too complex", Severity: SeverityFail},
					{File: "b.go", Line: 20, Message: "warning here", Severity: SeverityWarn},
				},
			},
		},
	}

	var buf bytes.Buffer
	WriteText(&buf, r)
	out := buf.String()

	if !strings.Contains(out, "=== Test Section ===") {
		t.Error("missing section header")
	}
	if !strings.Contains(out, "Violations:") {
		t.Error("missing Violations header")
	}
	if !strings.Contains(out, "Warnings:") {
		t.Error("missing Warnings header")
	}
	if !strings.Contains(out, "a.go:10:Foo") {
		t.Error("missing finding location")
	}
}

func TestWriteText_NoFindings(t *testing.T) {
	r := Report{
		Sections: []Section{
			{Name: "Empty", Summary: "ok", Severity: SeverityPass},
		},
	}

	var buf bytes.Buffer
	WriteText(&buf, r)
	out := buf.String()

	if strings.Contains(out, "Violations:") {
		t.Error("should not have Violations for empty findings")
	}
	if strings.Contains(out, "Warnings:") {
		t.Error("should not have Warnings for empty findings")
	}
}

func TestWriteText_MultipleSections(t *testing.T) {
	r := Report{
		Sections: []Section{
			{Name: "First", Summary: "ok", Severity: SeverityPass},
			{Name: "Second", Summary: "also ok", Severity: SeverityPass},
		},
	}

	var buf bytes.Buffer
	WriteText(&buf, r)
	out := buf.String()

	if !strings.Contains(out, "=== First ===") {
		t.Error("missing first section")
	}
	if !strings.Contains(out, "=== Second ===") {
		t.Error("missing second section")
	}
}

func TestWriteText_OnlyFails(t *testing.T) {
	r := Report{
		Sections: []Section{
			{
				Name:     "Fails Only",
				Summary:  "bad",
				Severity: SeverityFail,
				Findings: []Finding{
					{File: "a.go", Message: "fail", Severity: SeverityFail},
				},
			},
		},
	}

	var buf bytes.Buffer
	WriteText(&buf, r)
	out := buf.String()

	if !strings.Contains(out, "Violations:") {
		t.Error("missing Violations")
	}
	if strings.Contains(out, "Warnings:") {
		t.Error("should not have Warnings when no warns exist")
	}
}

func TestWriteText_OnlyWarns(t *testing.T) {
	r := Report{
		Sections: []Section{
			{
				Name:     "Warns Only",
				Summary:  "meh",
				Severity: SeverityWarn,
				Findings: []Finding{
					{File: "a.go", Message: "warn", Severity: SeverityWarn},
				},
			},
		},
	}

	var buf bytes.Buffer
	WriteText(&buf, r)
	out := buf.String()

	if strings.Contains(out, "Violations:") {
		t.Error("should not have Violations when no fails exist")
	}
	if !strings.Contains(out, "Warnings:") {
		t.Error("missing Warnings")
	}
}

func TestGroupFindings(t *testing.T) {
	findings := []Finding{
		{Message: "fail1", Severity: SeverityFail},
		{Message: "warn1", Severity: SeverityWarn},
		{Message: "fail2", Severity: SeverityFail},
		{Message: "pass1", Severity: SeverityPass},
	}

	fails, warns := groupFindings(findings)
	if len(fails) != 2 {
		t.Errorf("fails = %d, want 2", len(fails))
	}
	if len(warns) != 1 {
		t.Errorf("warns = %d, want 1", len(warns))
	}
}

func TestWriteFindingGroup_Empty(t *testing.T) {
	var buf bytes.Buffer
	writeFindingGroup(&buf, "Header:", nil)
	if buf.Len() != 0 {
		t.Error("expected empty output for nil findings")
	}
}

func TestWriteFindingGroup_NonEmpty(t *testing.T) {
	var buf bytes.Buffer
	writeFindingGroup(&buf, "Issues:", []Finding{
		{File: "a.go", Line: 5, Message: "problem", Severity: SeverityFail},
	})
	out := buf.String()
	if !strings.Contains(out, "Issues:") {
		t.Error("missing header")
	}
	if !strings.Contains(out, "a.go:5") {
		t.Error("missing finding")
	}
}

func TestPrintFinding_WithLineAndFunction(t *testing.T) {
	var buf bytes.Buffer
	printFinding(&buf, Finding{File: "a.go", Line: 10, Function: "Foo", Message: "msg", Severity: SeverityFail})
	if !strings.Contains(buf.String(), "a.go:10:Foo") {
		t.Errorf("output = %q, missing location", buf.String())
	}
}

func TestPrintFinding_FileOnly(t *testing.T) {
	var buf bytes.Buffer
	printFinding(&buf, Finding{File: "b.go", Message: "msg", Severity: SeverityWarn})
	out := buf.String()
	if !strings.Contains(out, "b.go") {
		t.Error("missing file")
	}
	if strings.Contains(out, "b.go:0") {
		t.Error("should not show :0 for no line")
	}
}

func TestTopFindings(t *testing.T) {
	findings := []Finding{
		{Value: 1}, {Value: 5}, {Value: 3}, {Value: 2}, {Value: 4},
	}

	top := TopFindings(findings, 3)
	if len(top) != 3 {
		t.Fatalf("TopFindings(5, 3) = %d entries, want 3", len(top))
	}
	if top[0].Value != 5 || top[1].Value != 4 || top[2].Value != 3 {
		t.Errorf("TopFindings order wrong: got %v %v %v", top[0].Value, top[1].Value, top[2].Value)
	}

	all := TopFindings(findings, 10)
	if len(all) != 5 {
		t.Errorf("TopFindings(5, 10) = %d entries, want 5", len(all))
	}
}

func TestHistogram(t *testing.T) {
	values := []float64{1, 3, 5, 7, 10, 15, 25}
	buckets := []float64{5, 10, 20}

	result := Histogram(values, buckets)
	if !strings.Contains(result, "≤") {
		t.Error("histogram missing ≤ bucket label")
	}
	if !strings.Contains(result, ">") {
		t.Error("histogram missing > bucket label")
	}
	// Should have 4 lines (3 buckets + overflow)
	lines := strings.Split(strings.TrimSpace(result), "\n")
	if len(lines) != 4 {
		t.Errorf("histogram has %d lines, want 4", len(lines))
	}
}

func TestBucketIndex(t *testing.T) {
	buckets := []float64{5, 10, 20}

	tests := []struct {
		v    float64
		want int
	}{
		{3, 0},
		{5, 0},   // exactly at boundary: should be in bucket 0 (<=5)
		{5.1, 1}, // just over: should be in bucket 1
		{10, 1},
		{15, 2},
		{20, 2},
		{25, 3}, // overflow bucket
	}

	for _, tt := range tests {
		if got := bucketIndex(tt.v, buckets); got != tt.want {
			t.Errorf("bucketIndex(%.1f) = %d, want %d", tt.v, got, tt.want)
		}
	}
}

func TestWriteJSON(t *testing.T) {
	r := Report{
		Sections: []Section{
			{Name: "Test", Summary: "ok", Severity: SeverityPass},
		},
	}

	var buf bytes.Buffer
	if err := WriteJSON(&buf, r); err != nil {
		t.Fatalf("WriteJSON error: %v", err)
	}
	if !strings.Contains(buf.String(), `"name": "Test"`) {
		t.Error("JSON output missing section name")
	}
}

func TestBar(t *testing.T) {
	if got := bar(5, nil); got != "" {
		t.Errorf("bar with empty total = %q, want empty", got)
	}

	result := bar(10, make([]float64, 20))
	if len(result) == 0 {
		t.Error("bar should produce non-empty string for non-zero count")
	}

	// Small count should get minimum 1 char
	result = bar(1, make([]float64, 1000))
	if len(result) != 1 {
		t.Errorf("bar minimum = %d chars, want 1", len(result))
	}

	// Zero count should produce empty string
	result = bar(0, make([]float64, 10))
	if len(result) != 0 {
		t.Errorf("bar zero count = %d chars, want 0", len(result))
	}
}
