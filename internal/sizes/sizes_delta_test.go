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
	section, err := Analyze(dir, d, funcThreshold, fileThreshold, goExtractor(t))
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
		name string
		h    lang.FunctionSize
		b    baseSizes
		want bool
	}{
		{"no_baseline_treated_as_grew", mk("f", 80), noBase, true},
		{"absent_at_base", mk("g", 80), withBase, true},
		{"head_higher", mk("f", 80), withBase, true},
		{"head_equal", mk("f", 60), withBase, false},
		{"head_lower", mk("f", 40), withBase, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := grewFunc(tc.h, tc.b); got != tc.want {
				t.Errorf("grewFunc = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGrewFile(t *testing.T) {
	withBase := baseSizes{file: 600, ok: true}
	noBase := baseSizes{}

	cases := []struct {
		name string
		h    lang.FileSize
		b    baseSizes
		want bool
	}{
		{"no_baseline_treated_as_grew", lang.FileSize{Path: "x.go", Lines: 700}, noBase, true},
		{"head_higher", lang.FileSize{Path: "x.go", Lines: 700}, withBase, true},
		{"head_equal", lang.FileSize{Path: "x.go", Lines: 600}, withBase, false},
		{"head_lower", lang.FileSize{Path: "x.go", Lines: 500}, withBase, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := grewFile(tc.h, tc.b); got != tc.want {
				t.Errorf("grewFile = %v, want %v", got, tc.want)
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
