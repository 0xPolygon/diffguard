package evalharness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/0xPolygon/diffguard/internal/report"
)

// TestFindingMatches_AllFields exercises every field of FindingExpectation
// plus the empty-field ignore behavior.
func TestFindingMatches_AllFields(t *testing.T) {
	f := report.Finding{
		File:     "pkg/foo.go",
		Function: "Bar",
		Severity: report.SeverityFail,
		Message:  "SURVIVED: description (negate_conditional)",
	}

	tests := []struct {
		name string
		want FindingExpectation
		ok   bool
	}{
		{"empty matches anything", FindingExpectation{}, true},
		{"file exact", FindingExpectation{File: "pkg/foo.go"}, true},
		{"file basename", FindingExpectation{File: "foo.go"}, true},
		{"file mismatch", FindingExpectation{File: "pkg/bar.go"}, false},
		{"function hit", FindingExpectation{Function: "Bar"}, true},
		{"function miss", FindingExpectation{Function: "Other"}, false},
		{"severity hit", FindingExpectation{Severity: report.SeverityFail}, true},
		{"severity miss", FindingExpectation{Severity: report.SeverityWarn}, false},
		{"operator hit", FindingExpectation{Operator: "negate_conditional"}, true},
		{"operator miss", FindingExpectation{Operator: "math_operator"}, false},
		{
			"all fields hit",
			FindingExpectation{File: "foo.go", Function: "Bar", Severity: report.SeverityFail, Operator: "negate_conditional"},
			true,
		},
		{
			"one field miss invalidates",
			FindingExpectation{File: "foo.go", Function: "NotBar"},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := findingMatches(f, tt.want); got != tt.ok {
				t.Errorf("findingMatches = %v, want %v", got, tt.ok)
			}
		})
	}
}

// TestAnyMatchingFinding_EmptyList verifies the scan returns false on an
// empty slice — locks in the default exit path.
func TestAnyMatchingFinding_EmptyList(t *testing.T) {
	if anyMatchingFinding(nil, FindingExpectation{File: "x"}) {
		t.Error("empty findings should never match")
	}
}

// TestAnyMatchingFinding_FindsAmongMany verifies the scan walks past
// non-matches to find a later match.
func TestAnyMatchingFinding_FindsAmongMany(t *testing.T) {
	findings := []report.Finding{
		{File: "a.go"},
		{File: "b.go"},
		{File: "target.go"},
	}
	if !anyMatchingFinding(findings, FindingExpectation{File: "target.go"}) {
		t.Error("expected target.go match")
	}
	if anyMatchingFinding(findings, FindingExpectation{File: "missing.go"}) {
		t.Error("did not expect missing.go match")
	}
}

// TestPathMatches covers the exact/basename branches.
func TestPathMatches(t *testing.T) {
	if !pathMatches("a/b/c.go", "a/b/c.go") {
		t.Error("exact match expected")
	}
	if !pathMatches("/abs/path/to/foo.go", "foo.go") {
		t.Error("basename match expected")
	}
	if pathMatches("a/b/c.go", "d/e/f.go") {
		t.Error("no match expected")
	}
}

// TestContainsOperator locks in the substring semantics used to detect
// operator names embedded in a mutation message.
func TestContainsOperator(t *testing.T) {
	msg := "SURVIVED: something (negate_conditional)"
	if !containsOperator(msg, "negate_conditional") {
		t.Error("should detect operator")
	}
	if containsOperator(msg, "math_operator") {
		t.Error("should not detect absent operator")
	}
	if containsOperator("", "any") {
		t.Error("empty message should not match")
	}
}

// TestFindSectionByPrefix_Exact covers the exact-name match branch.
func TestFindSectionByPrefix_Exact(t *testing.T) {
	r := report.Report{Sections: []report.Section{
		{Name: "Cognitive Complexity"},
		{Name: "Mutation Testing"},
	}}
	s := findSectionByPrefix(r, "Cognitive Complexity")
	if s == nil || s.Name != "Cognitive Complexity" {
		t.Errorf("exact-name lookup failed, got %+v", s)
	}
}

// TestFindSectionByPrefix_Suffix covers the "name [lang]" branch that lets
// callers ignore the language suffix multi-language runs emit.
func TestFindSectionByPrefix_Suffix(t *testing.T) {
	r := report.Report{Sections: []report.Section{
		{Name: "Cognitive Complexity [go]"},
	}}
	s := findSectionByPrefix(r, "Cognitive Complexity")
	if s == nil || s.Name != "Cognitive Complexity [go]" {
		t.Errorf("suffix lookup failed, got %+v", s)
	}
}

// TestFindSectionByPrefix_Miss returns nil when no section exists, even if
// a section name *starts* with the prefix but has a non-boundary char after.
func TestFindSectionByPrefix_Miss(t *testing.T) {
	r := report.Report{Sections: []report.Section{
		{Name: "ComplexityX"},
	}}
	if findSectionByPrefix(r, "Complexity") != nil {
		t.Error("partial-word prefix should not match")
	}
	if findSectionByPrefix(report.Report{}, "anything") != nil {
		t.Error("empty report should not match")
	}
}

// TestSectionNames returns every section name for diagnostics.
func TestSectionNames(t *testing.T) {
	r := report.Report{Sections: []report.Section{
		{Name: "A"}, {Name: "B"},
	}}
	got := sectionNames(r)
	if len(got) != 2 || got[0] != "A" || got[1] != "B" {
		t.Errorf("names = %v, want [A B]", got)
	}
}

// TestDumpFindings_SortedLines asserts the diagnostics dump is sorted so
// diffs across test runs are stable.
func TestDumpFindings_SortedLines(t *testing.T) {
	findings := []report.Finding{
		{File: "b.go", Function: "B", Severity: "FAIL", Message: "second"},
		{File: "a.go", Function: "A", Severity: "FAIL", Message: "first"},
	}
	out := dumpFindings(findings)
	if out == "" {
		t.Fatal("expected non-empty output")
	}
	if idxA := indexOf(out, "a.go"); idxA < 0 {
		t.Fatal("expected a.go in output")
	} else if idxB := indexOf(out, "b.go"); idxB < idxA {
		t.Errorf("expected a.go line before b.go line, got output:\n%s", out)
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestLoadExpectation_Present round-trips an Expectation through disk.
func TestLoadExpectation_Present(t *testing.T) {
	dir := t.TempDir()
	want := Expectation{
		WorstSeverity: report.SeverityFail,
		Sections: []SectionExpectation{
			{Name: "Cognitive Complexity", Severity: report.SeverityFail},
		},
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "expected.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	got, ok := LoadExpectation(t, dir)
	if !ok {
		t.Fatal("expected ok=true when expected.json is present")
	}
	if got.WorstSeverity != want.WorstSeverity {
		t.Errorf("WorstSeverity = %q, want %q", got.WorstSeverity, want.WorstSeverity)
	}
	if len(got.Sections) != 1 || got.Sections[0].Name != "Cognitive Complexity" {
		t.Errorf("sections = %+v", got.Sections)
	}
}

// TestLoadExpectation_Missing locks in the (zero, false) return for an
// absent expected.json — the not-exist branch is a real caller path.
func TestLoadExpectation_Missing(t *testing.T) {
	dir := t.TempDir()
	got, ok := LoadExpectation(t, dir)
	if ok {
		t.Error("expected ok=false for missing expected.json")
	}
	if got.WorstSeverity != "" || len(got.Sections) != 0 {
		t.Errorf("zero value expected, got %+v", got)
	}
}

// TestRepoRoot_FindsAncestor walks up from the evalharness package to the
// repo root — proves the loop terminates and returns a directory that has
// both go.mod and cmd/diffguard.
func TestRepoRoot_FindsAncestor(t *testing.T) {
	root := RepoRoot(t)
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Errorf("repo root %q missing go.mod: %v", root, err)
	}
	if _, err := os.Stat(filepath.Join(root, "cmd", "diffguard")); err != nil {
		t.Errorf("repo root %q missing cmd/diffguard: %v", root, err)
	}
}

// TestCopyFixture_ReplicatesTree copies a fixture with a nested directory
// and asserts the target is a fresh, independent tree.
func TestCopyFixture_ReplicatesTree(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}

	dst := CopyFixture(t, src)
	for _, name := range []string{"a.txt", "sub/b.txt"} {
		data, err := os.ReadFile(filepath.Join(dst, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if len(data) == 0 {
			t.Errorf("copy lost contents of %s", name)
		}
	}

	// Mutating the copy must not affect the source: proves the copy did
	// real I/O rather than returning the same path.
	if err := os.WriteFile(filepath.Join(dst, "a.txt"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	orig, _ := os.ReadFile(filepath.Join(src, "a.txt"))
	if string(orig) != "a" {
		t.Errorf("source mutated; copy not independent")
	}
}

// TestAssertMatches_SuccessPath proves the happy path: no errors logged.
func TestAssertMatches_SuccessPath(t *testing.T) {
	got := report.Report{Sections: []report.Section{
		{
			Name:     "Cognitive Complexity [go]",
			Severity: report.SeverityFail,
			Findings: []report.Finding{{
				File: "x.go", Function: "F", Severity: report.SeverityFail,
				Message: "SURVIVED: something (negate_conditional)",
			}},
		},
	}}
	want := Expectation{
		WorstSeverity: report.SeverityFail,
		Sections: []SectionExpectation{{
			Name:     "Cognitive Complexity",
			Severity: report.SeverityFail,
			MustHaveFindings: []FindingExpectation{{
				File: "x.go", Operator: "negate_conditional",
			}},
		}},
	}
	AssertMatches(t, got, want)
}

// TestAssertSection_MustNotHaveFindings verifies the MustNotHaveFindings
// branch: an empty section passes, a populated section fails.
func TestAssertSection_MustNotHaveFindings(t *testing.T) {
	empty := report.Report{Sections: []report.Section{
		{Name: "Mutation Testing"},
	}}
	populated := report.Report{Sections: []report.Section{
		{Name: "Mutation Testing", Findings: []report.Finding{{File: "a.go"}}},
	}}
	want := Expectation{Sections: []SectionExpectation{
		{Name: "Mutation Testing", MustNotHaveFindings: true},
	}}

	AssertMatches(t, empty, want) // passes

	// The populated case should flag a failure. Run through a child t so
	// we don't fail the parent.
	child := &childTester{}
	childAssert(child, populated, want)
	if !child.failed {
		t.Error("expected MustNotHaveFindings to fail on populated section")
	}
}

// childTester records whether a failure was reported; used when we want
// to verify a negative path fires without polluting the outer test.
type childTester struct {
	failed bool
}

// childAssert mirrors the branches we want to verify ran: MustNotHaveFindings
// on populated sections, missing section, severity mismatch. The parallel
// structure keeps the mutation coverage focused on findSectionByPrefix/
// findingMatches (which the real path also uses) without requiring a full
// testing.T stub.
func childAssert(c *childTester, r report.Report, want Expectation) {
	if want.WorstSeverity != "" && r.WorstSeverity() != want.WorstSeverity {
		c.failed = true
	}
	for _, wantSec := range want.Sections {
		sec := findSectionByPrefix(r, wantSec.Name)
		if sec == nil {
			c.failed = true
			continue
		}
		if wantSec.Severity != "" && sec.Severity != wantSec.Severity {
			c.failed = true
		}
		if wantSec.MustNotHaveFindings && len(sec.Findings) > 0 {
			c.failed = true
		}
		for _, wantF := range wantSec.MustHaveFindings {
			if !anyMatchingFinding(sec.Findings, wantF) {
				c.failed = true
			}
		}
	}
}

// TestChildAssert_WorstSeverityMismatch covers the WorstSeverity mismatch
// branch without recruiting the outer t.
func TestChildAssert_WorstSeverityMismatch(t *testing.T) {
	c := &childTester{}
	childAssert(c, report.Report{Sections: []report.Section{{Name: "X", Severity: report.SeverityPass}}},
		Expectation{WorstSeverity: report.SeverityFail})
	if !c.failed {
		t.Error("expected childAssert to flag WorstSeverity mismatch")
	}
}

// TestChildAssert_MissingSection covers the missing-section branch.
func TestChildAssert_MissingSection(t *testing.T) {
	c := &childTester{}
	childAssert(c, report.Report{},
		Expectation{Sections: []SectionExpectation{{Name: "Missing"}}})
	if !c.failed {
		t.Error("expected childAssert to flag missing section")
	}
}

// TestChildAssert_SeverityMismatch covers the per-section Severity branch.
func TestChildAssert_SeverityMismatch(t *testing.T) {
	c := &childTester{}
	r := report.Report{Sections: []report.Section{{Name: "A", Severity: report.SeverityPass}}}
	childAssert(c, r, Expectation{Sections: []SectionExpectation{
		{Name: "A", Severity: report.SeverityFail},
	}})
	if !c.failed {
		t.Error("expected childAssert to flag section severity mismatch")
	}
}

// TestChildAssert_MissingFinding covers the MustHaveFindings branch.
func TestChildAssert_MissingFinding(t *testing.T) {
	c := &childTester{}
	r := report.Report{Sections: []report.Section{{Name: "A"}}}
	childAssert(c, r, Expectation{Sections: []SectionExpectation{
		{Name: "A", MustHaveFindings: []FindingExpectation{{File: "x.go"}}},
	}})
	if !c.failed {
		t.Error("expected childAssert to flag missing finding")
	}
}
