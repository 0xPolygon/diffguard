package sizes

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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

// bigFunc returns a Go function whose body has `lines` no-op statements,
// landing the FunctionExtractor's line count well above any threshold the
// tests use.
func bigFunc(name string, lines int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "func %s() {\n", name)
	for i := range lines {
		fmt.Fprintf(&sb, "\t_ = %d\n", i)
	}
	sb.WriteString("}\n")
	return sb.String()
}

func parseAndAnalyze(t *testing.T, dir, base string, funcThreshold, fileThreshold int) report.Section {
	t.Helper()
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
	section, err := Analyze(dir, d, funcThreshold, fileThreshold, DeltaTolerances{}, goExtractor(t))
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	return section
}

func TestGrewFunc(t *testing.T) {
	mk := func(name string, lines int) lang.FunctionSize {
		return lang.FunctionSize{
			FunctionInfo: lang.FunctionInfo{Name: name},
			Lines:        lines,
		}
	}
	withBase := baseSizes{funcs: map[string]int{"f": 60}, ok: true}
	noBase := baseSizes{}

	cases := []struct {
		name      string
		h         lang.FunctionSize
		b         baseSizes
		tolerance int
		want      bool
	}{
		{"no_baseline_treated_as_grew", mk("f", 80), noBase, 0, true},
		{"absent_at_base", mk("g", 80), withBase, 0, true},
		{"head_higher_no_tolerance", mk("f", 80), withBase, 0, true},
		{"head_equal_no_tolerance", mk("f", 60), withBase, 0, false},
		{"head_lower", mk("f", 40), withBase, 0, false},
		// Tolerance cases: head must exceed base by *more than* tolerance.
		// Default production setting is 5, so +1..+5 line bumps on a legacy
		// function get forgiven; +6 is the first regression.
		{"within_tolerance_lower_bound", mk("f", 61), withBase, 5, false},
		{"within_tolerance_at_bound", mk("f", 65), withBase, 5, false},
		{"just_over_tolerance", mk("f", 66), withBase, 5, true},
		{"new_function_tolerance_irrelevant", mk("g", 80), withBase, 100, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := grewFunc(tc.h, tc.b, tc.tolerance); got != tc.want {
				t.Errorf("grewFunc = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGrewFile(t *testing.T) {
	mk := func(path string, lines int) lang.FileSize {
		return lang.FileSize{Path: path, Lines: lines}
	}
	withBase := baseSizes{file: 600, ok: true}
	noBase := baseSizes{}

	cases := []struct {
		name       string
		h          lang.FileSize
		b          baseSizes
		pct, floor int
		want       bool
	}{
		{"no_baseline_treated_as_grew", mk("x.go", 700), noBase, 0, 0, true},
		{"head_higher_no_tolerance", mk("x.go", 700), withBase, 0, 0, true},
		{"head_equal", mk("x.go", 600), withBase, 0, 0, false},
		{"head_lower", mk("x.go", 500), withBase, 0, 0, false},
		// 5% of 600 = 30. Floor 10. max(30, 10) = 30. Growth must exceed 30.
		{"within_pct_below_floor", mk("x.go", 605), withBase, 5, 10, false},
		{"within_pct", mk("x.go", 625), withBase, 5, 10, false},
		{"at_pct_boundary", mk("x.go", 630), withBase, 5, 10, false},
		{"just_over_pct", mk("x.go", 631), withBase, 5, 10, true},
		// Floor dominates on small files. 5% of 100 = 5; floor=10 wins.
		// Growth ≤10 → drop; >10 → flag.
		{"floor_dominates_drops_at_floor", mk("y.go", 110), baseSizes{file: 100, ok: true}, 5, 10, false},
		{"floor_dominates_flags_just_over", mk("y.go", 111), baseSizes{file: 100, ok: true}, 5, 10, true},
		// Big files: pct dominates so a fixed flat tolerance doesn't
		// rubber-stamp huge additions.
		{"large_file_pct_dominates", mk("z.go", 5050), baseSizes{file: 5000, ok: true}, 5, 10, false},
		{"large_file_just_over_pct", mk("z.go", 5251), baseSizes{file: 5000, ok: true}, 5, 10, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := grewFile(tc.h, tc.b, tc.pct, tc.floor); got != tc.want {
				t.Errorf("grewFile = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFileGrowthTolerance(t *testing.T) {
	cases := []struct {
		name       string
		base       int
		pct, floor int
		want       int
	}{
		{"floor_only_when_pct_zero", 1000, 0, 50, 50},
		{"pct_dominates_large_base", 1000, 5, 10, 50},
		{"floor_dominates_small_base", 100, 5, 10, 10},
		{"pct_at_exact_match_to_floor", 200, 5, 10, 10},
		{"zero_base_collapses_to_floor", 0, 5, 10, 10},
		{"negative_pct_treated_as_zero", 1000, -1, 25, 25},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fileGrowthTolerance(tc.base, tc.pct, tc.floor); got != tc.want {
				t.Errorf("fileGrowthTolerance(%d, %d, %d) = %d, want %d", tc.base, tc.pct, tc.floor, got, tc.want)
			}
		})
	}
}

func TestFormatFuncSizeMsg(t *testing.T) {
	mk := func(file, name string, lines int) lang.FunctionSize {
		return lang.FunctionSize{
			FunctionInfo: lang.FunctionInfo{File: file, Name: name},
			Lines:        lines,
		}
	}
	cases := []struct {
		name   string
		fn     lang.FunctionSize
		deltas map[string]map[string]int
		want   string
	}{
		{
			name:   "no_baseline_bare_message",
			fn:     mk("a.go", "f", 80),
			deltas: nil,
			want:   "function=80 lines",
		},
		{
			name:   "no_entry_for_function",
			fn:     mk("a.go", "f", 80),
			deltas: map[string]map[string]int{"a.go": {"other": 5}},
			want:   "function=80 lines",
		},
		{
			name:   "renders_positive_delta",
			fn:     mk("a.go", "f", 80),
			deltas: map[string]map[string]int{"a.go": {"f": 70}},
			want:   "function=80 lines (+10 vs base)",
		},
		{
			// Lock subtraction direction (head - base).
			name:   "subtraction_not_addition",
			fn:     mk("a.go", "f", 100),
			deltas: map[string]map[string]int{"a.go": {"f": 95}},
			want:   "function=100 lines (+5 vs base)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatFuncSizeMsg(tc.fn, tc.deltas); got != tc.want {
				t.Errorf("formatFuncSizeMsg = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatFileSizeMsg(t *testing.T) {
	mk := func(path string, lines int) lang.FileSize {
		return lang.FileSize{Path: path, Lines: lines}
	}
	cases := []struct {
		name   string
		f      lang.FileSize
		deltas map[string]int
		want   string
	}{
		{
			name:   "no_baseline_bare_message",
			f:      mk("big.go", 600),
			deltas: nil,
			want:   "file=600 lines",
		},
		{
			name:   "no_entry_for_path",
			f:      mk("big.go", 600),
			deltas: map[string]int{"other.go": 100},
			want:   "file=600 lines",
		},
		{
			name:   "renders_positive_delta",
			f:      mk("big.go", 600),
			deltas: map[string]int{"big.go": 580},
			want:   "file=600 lines (+20 vs base)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatFileSizeMsg(tc.f, tc.deltas); got != tc.want {
				t.Errorf("formatFileSizeMsg = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSizesDelta_FunctionUnchangedDropped: legacy oversized function, only a
// comment inside is touched by the PR. Line count unchanged → drop.
func TestSizesDelta_FunctionUnchangedDropped(t *testing.T) {
	dir := initRepo(t)
	body := bigFunc("Big", 80) // 80+2 = ~82 lines, well over funcThreshold=50.
	base := "package x\n\n// v1\n" + body
	writeAndCommit(t, dir, "a.go", base, "base")

	runGit(t, dir, "checkout", "-q", "-b", "feature")
	feature := strings.Replace(base, "_ = 0\n", "_ = 0 // touched\n", 1)
	writeAndCommit(t, dir, "a.go", feature, "comment tweak")

	s := parseAndAnalyze(t, dir, "main", 50, 500)
	for _, f := range s.Findings {
		if f.Function == "Big" {
			t.Errorf("legacy function size finding leaked through delta gate: %+v", f)
		}
	}
}

// TestSizesDelta_FunctionGrewKept: function grew past the threshold during
// this PR — must still be flagged.
func TestSizesDelta_FunctionGrewKept(t *testing.T) {
	dir := initRepo(t)
	base := "package x\n\n" + bigFunc("Grow", 30)
	writeAndCommit(t, dir, "a.go", base, "base")

	runGit(t, dir, "checkout", "-q", "-b", "feature")
	feature := "package x\n\n" + bigFunc("Grow", 80)
	writeAndCommit(t, dir, "a.go", feature, "grow Grow")

	s := parseAndAnalyze(t, dir, "main", 50, 500)
	found := false
	for _, f := range s.Findings {
		if f.Function == "Grow" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected finding for Grow, got %+v", s.Findings)
	}
}

// TestSizesDelta_FileTouchedNotGrownDropped: 4000-line legacy file, PR
// changes one line without growing the file. File-size finding must be
// dropped.
func TestSizesDelta_FileTouchedNotGrownDropped(t *testing.T) {
	dir := initRepo(t)
	var sb strings.Builder
	sb.WriteString("package x\n\n")
	for i := range 600 {
		fmt.Fprintf(&sb, "var V%d = %d\n", i, i)
	}
	base := sb.String()
	writeAndCommit(t, dir, "big.go", base, "base")

	runGit(t, dir, "checkout", "-q", "-b", "feature")
	// Same line count, different content on a single line.
	feature := strings.Replace(base, "var V0 = 0", "var V0 = 1", 1)
	writeAndCommit(t, dir, "big.go", feature, "tweak V0")

	s := parseAndAnalyze(t, dir, "main", 50, 500)
	for _, f := range s.Findings {
		if f.File == "big.go" && f.Function == "" {
			t.Errorf("legacy file size finding leaked: %+v", f)
		}
	}
}

// TestSizesDelta_FileGrewKept: oversized file grew further. Must still flag.
func TestSizesDelta_FileGrewKept(t *testing.T) {
	dir := initRepo(t)
	var sb strings.Builder
	sb.WriteString("package x\n\n")
	for i := range 600 {
		fmt.Fprintf(&sb, "var V%d = %d\n", i, i)
	}
	base := sb.String()
	writeAndCommit(t, dir, "big.go", base, "base")

	runGit(t, dir, "checkout", "-q", "-b", "feature")
	feature := base + "var Extra = 1\nvar Extra2 = 2\n"
	writeAndCommit(t, dir, "big.go", feature, "grow")

	s := parseAndAnalyze(t, dir, "main", 50, 500)
	found := false
	for _, f := range s.Findings {
		if f.File == "big.go" && f.Function == "" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected file-size finding for big.go, got %+v", s.Findings)
	}
}
