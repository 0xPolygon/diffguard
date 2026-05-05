package complexity

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
	_ "github.com/0xPolygon/diffguard/internal/lang/goanalyzer"
	"github.com/0xPolygon/diffguard/internal/report"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "--initial-branch=main")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

func writeAndCommit(t *testing.T, dir, path, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", msg)
}

// complexBodyV1 / V2 carry the same cognitive complexity but differ on the
// inner-most line, so the diff lands *inside* the function body. Without
// this, OverlapsRange would skip the function entirely and the delta-gating
// code path wouldn't be exercised.
const complexBodyV1 = `	if x > 0 {
		if x > 10 {
			if x > 100 {
				if x > 1000 {
					if x > 10000 {
						if x > 100000 {
							_ = x // v1
						}
					}
				}
			}
		}
	}`

const complexBodyV2 = `	if x > 0 {
		if x > 10 {
			if x > 100 {
				if x > 1000 {
					if x > 10000 {
						if x > 100000 {
							_ = x // v2
						}
					}
				}
			}
		}
	}`

// parseAndAnalyze runs the same diff.Parse + complexity.Analyze flow main()
// uses, with delta-tolerance=0 so tests can pin down exact head/base
// boundaries without the production tolerance hiding small regressions.
func parseAndAnalyze(t *testing.T, dir, base string) report.Section {
	t.Helper()
	calc := goCalc(t)
	l, _ := lang.Get("go")
	d, err := diff.Parse(dir, base, diff.Filter{
		DiffGlobs: l.FileFilter().DiffGlobs,
		Includes: func(p string) bool {
			ff := l.FileFilter()
			if !slices.Contains(ff.Extensions, filepath.Ext(p)) {
				return false
			}
			return !ff.IsTestFile(p)
		},
	})
	if err != nil {
		t.Fatalf("diff.Parse: %v", err)
	}
	section, err := Analyze(dir, d, 10, 0, calc)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	return section
}

// TestDeltaGating_PreExistingViolationDropped covers the headline use case:
// a PR that touches a legacy complex function without making it worse should
// not be blamed for that function's complexity.
func TestDeltaGating_PreExistingViolationDropped(t *testing.T) {
	dir := initRepo(t)
	base := "package x\n\nfunc Complex(x int) {\n" + complexBodyV1 + "\n}\n"
	writeAndCommit(t, dir, "a.go", base, "base")

	runGit(t, dir, "checkout", "-q", "-b", "feature")
	// Diff lands *inside* the function (comment on a body line), so the
	// function is analyzed, but its cognitive score is identical to base.
	// Delta gating must drop the finding.
	feature := "package x\n\nfunc Complex(x int) {\n" + complexBodyV2 + "\n}\n"
	writeAndCommit(t, dir, "a.go", feature, "tweak inner comment")

	s := parseAndAnalyze(t, dir, "main")
	if s.Severity != report.SeverityPass {
		t.Errorf("severity = %v, want PASS (legacy complexity not worsened); findings=%+v", s.Severity, s.Findings)
	}
	if len(s.Findings) != 0 {
		t.Errorf("findings = %d, want 0", len(s.Findings))
	}
}

// TestDeltaGating_IncreasedComplexityKept covers the regression case: a PR
// that pushes a function's complexity higher must still fail.
func TestDeltaGating_IncreasedComplexityKept(t *testing.T) {
	dir := initRepo(t)
	// Baseline: complexity right at the threshold (10) — mild but not
	// flagged. The smaller body lets us add nesting in the feature commit
	// without rewriting it from scratch.
	baseBody := `	if x > 0 {
		if x > 10 {
			if x > 100 {
				if x > 1000 {
					_ = x
				}
			}
		}
	}`
	base := "package x\n\nfunc Complex(x int) {\n" + baseBody + "\n}\n"
	writeAndCommit(t, dir, "a.go", base, "base")

	runGit(t, dir, "checkout", "-q", "-b", "feature")
	// Feature: add two more levels of nesting → complexity climbs above
	// threshold. Delta gating must keep this finding.
	feature := "package x\n\nfunc Complex(x int) {\n" + complexBodyV1 + "\n}\n"
	writeAndCommit(t, dir, "a.go", feature, "deepen nesting")

	s := parseAndAnalyze(t, dir, "main")
	if s.Severity != report.SeverityFail {
		t.Errorf("severity = %v, want FAIL", s.Severity)
	}
	if len(s.Findings) != 1 || s.Findings[0].Function != "Complex" {
		t.Errorf("findings = %+v, want one for Complex", s.Findings)
	}
}

// TestDeltaGating_NewFunctionKept covers the "new debt" case: a brand-new
// over-threshold function in the diff has no baseline, so it stays flagged.
func TestDeltaGating_NewFunctionKept(t *testing.T) {
	dir := initRepo(t)
	writeAndCommit(t, dir, "a.go", "package x\n", "base")

	runGit(t, dir, "checkout", "-q", "-b", "feature")
	feature := "package x\n\nfunc Complex(x int) {\n" + complexBodyV1 + "\n}\n"
	writeAndCommit(t, dir, "a.go", feature, "add Complex")

	s := parseAndAnalyze(t, dir, "main")
	if s.Severity != report.SeverityFail {
		t.Errorf("severity = %v, want FAIL", s.Severity)
	}
	if len(s.Findings) != 1 || s.Findings[0].Function != "Complex" {
		t.Errorf("findings = %+v, want one for Complex", s.Findings)
	}
}

func TestWorsened(t *testing.T) {
	mk := func(name string, c int) lang.FunctionComplexity {
		return lang.FunctionComplexity{
			FunctionInfo: lang.FunctionInfo{Name: name},
			Complexity:   c,
		}
	}
	cases := []struct {
		name      string
		h         lang.FunctionComplexity
		base      map[string]int
		tolerance int
		want      bool
	}{
		{"absent_at_base", mk("f", 12), map[string]int{}, 0, true},
		{"nil_base_map", mk("f", 12), nil, 0, true},
		{"head_higher_no_tolerance", mk("f", 13), map[string]int{"f": 12}, 0, true},
		{"head_equal_no_tolerance", mk("f", 12), map[string]int{"f": 12}, 0, false},
		{"head_lower", mk("f", 8), map[string]int{"f": 12}, 0, false},
		// Tolerance-aware cases: head must exceed base by *more than*
		// tolerance to count as worsened. tolerance=3 means +1, +2, +3 are
		// all forgiven; +4 is the first regression that fails.
		{"within_tolerance_lower_bound", mk("f", 13), map[string]int{"f": 12}, 3, false},
		{"within_tolerance_at_bound", mk("f", 15), map[string]int{"f": 12}, 3, false},
		{"just_over_tolerance", mk("f", 16), map[string]int{"f": 12}, 3, true},
		{"new_function_tolerance_irrelevant", mk("g", 8), map[string]int{"f": 12}, 100, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := worsened(tc.h, tc.base, tc.tolerance); got != tc.want {
				t.Errorf("worsened = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDeltaGating_WithinToleranceDropped covers the production default: a
// small (+3 or less) regression on a legacy hot function should not block
// the PR. The function is over the absolute threshold both before and after,
// but the delta is within tolerance.
func TestDeltaGating_WithinToleranceDropped(t *testing.T) {
	dir := initRepo(t)
	// Base: complexity=21 (six nested ifs).
	base := "package x\n\nfunc Complex(x int) {\n" + complexBodyV1 + "\n}\n"
	writeAndCommit(t, dir, "a.go", base, "base")

	runGit(t, dir, "checkout", "-q", "-b", "feature")
	// Feature: tack one logical operator onto the outermost condition.
	// Cognitive complexity goes up by exactly 1 (op-type-change counter)
	// without adding a new branch — well within the production tolerance.
	feature := `package x

func Complex(x int) {
	if x > 0 && x < 1<<31 {
		if x > 10 {
			if x > 100 {
				if x > 1000 {
					if x > 10000 {
						if x > 100000 {
							_ = x
						}
					}
				}
			}
		}
	}
}
`
	writeAndCommit(t, dir, "a.go", feature, "tiny bump")

	calc := goCalc(t)
	l, _ := lang.Get("go")
	d, err := diff.Parse(dir, "main", diff.Filter{
		DiffGlobs: l.FileFilter().DiffGlobs,
		Includes: func(p string) bool {
			ff := l.FileFilter()
			if !slices.Contains(ff.Extensions, filepath.Ext(p)) {
				return false
			}
			return !ff.IsTestFile(p)
		},
	})
	if err != nil {
		t.Fatalf("diff.Parse: %v", err)
	}
	// With tolerance=3: +1 regression is forgiven → PASS.
	s, err := Analyze(dir, d, 10, 3, calc)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if s.Severity != report.SeverityPass {
		t.Errorf("severity = %v, want PASS (within tolerance); findings=%+v", s.Severity, s.Findings)
	}
	// Same data, tolerance=0: same +1 regression now fails. Locks in the
	// invariant that tolerance-3 is the only thing softening the gate here.
	s, err = Analyze(dir, d, 10, 0, calc)
	if err != nil {
		t.Fatalf("Analyze (tol=0): %v", err)
	}
	if s.Severity != report.SeverityFail {
		t.Errorf("tol=0: severity = %v, want FAIL", s.Severity)
	}
}

func TestFormatComplexityMsg(t *testing.T) {
	mk := func(file, name string, c int) lang.FunctionComplexity {
		return lang.FunctionComplexity{
			FunctionInfo: lang.FunctionInfo{File: file, Name: name},
			Complexity:   c,
		}
	}
	cases := []struct {
		name   string
		fn     lang.FunctionComplexity
		deltas map[string]map[string]int
		want   string
	}{
		{
			name:   "no_baseline_bare_message",
			fn:     mk("a.go", "f", 12),
			deltas: nil,
			want:   "complexity=12",
		},
		{
			name:   "no_entry_for_function",
			fn:     mk("a.go", "f", 12),
			deltas: map[string]map[string]int{"a.go": {"other": 5}},
			want:   "complexity=12",
		},
		{
			name:   "renders_positive_delta",
			fn:     mk("a.go", "f", 17),
			deltas: map[string]map[string]int{"a.go": {"f": 12}},
			want:   "complexity=17 (+5 vs base)",
		},
		{
			// Locks the subtraction direction: head - base, not the other
			// way around. (Mutation testing flips operands; this catches it.)
			name:   "subtraction_not_addition",
			fn:     mk("a.go", "f", 30),
			deltas: map[string]map[string]int{"a.go": {"f": 28}},
			want:   "complexity=30 (+2 vs base)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatComplexityMsg(tc.fn, tc.deltas); got != tc.want {
				t.Errorf("formatComplexityMsg = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDeltaGating_NewFileKept covers the "the whole file is new" case: when
// `git show base:path` returns nothing, fall back to absolute thresholds.
func TestDeltaGating_NewFileKept(t *testing.T) {
	dir := initRepo(t)
	writeAndCommit(t, dir, "other.go", "package x\n", "base")

	runGit(t, dir, "checkout", "-q", "-b", "feature")
	feature := "package x\n\nfunc Complex(x int) {\n" + complexBodyV1 + "\n}\n"
	writeAndCommit(t, dir, "newfile.go", feature, "add new file")

	s := parseAndAnalyze(t, dir, "main")
	if s.Severity != report.SeverityFail {
		t.Errorf("severity = %v, want FAIL (new file gated by absolute threshold)", s.Severity)
	}
}
