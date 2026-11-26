package goanalyzer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
)

const sampleSource = `package p

import "errors"

var calls int

func note() { calls++ }

func Cmp(a, b int) bool { return a > b }

func Eq(a, b int) bool { return a == b }

func Add(a, b int) int { return a + b }

func Flag() bool { return true }

func Count(n int) int {
	c := 0
	for range n {
		c++
	}
	return c
}

func Guard(x int) (int, error) {
	if x < 0 {
		return 0, errors.New("negative")
	}
	note()
	return x, nil
}

func Join(a string) string { return a + "!" }

func Untested(a, b int) bool { return a != b }

func Loose(a int) int {
	if a > 100 {
		println()
	}
	return a
}
`

const sampleTests = `package p

import "testing"

func TestCmp(t *testing.T) {
	if Cmp(1, 1) || !Cmp(2, 1) {
		t.Fatal("Cmp")
	}
}

func TestEq(t *testing.T) {
	if !Eq(1, 1) || Eq(1, 2) {
		t.Fatal("Eq")
	}
}

func TestAdd(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Fatal("Add")
	}
}

func TestFlag(t *testing.T) {
	if !Flag() {
		t.Fatal("Flag")
	}
}

func TestCount(t *testing.T) {
	if Count(3) != 3 {
		t.Fatal("Count")
	}
}

func TestGuard(t *testing.T) {
	if _, err := Guard(-1); err == nil {
		t.Fatal("Guard(-1)")
	}
	calls = 0
	if v, err := Guard(0); v != 0 || err != nil || calls != 1 {
		t.Fatal("Guard(0)")
	}
	if v, _ := Guard(7); v != 7 {
		t.Fatal("Guard(7)")
	}
}

func TestJoin(t *testing.T) {
	if Join("a") != "a!" {
		t.Fatal("Join")
	}
}

func TestLoose(t *testing.T) {
	if Loose(1) != 1 {
		t.Fatal("Loose")
	}
}
`

// writeModule creates a throwaway module with one package "p" and returns
// the module root.
func writeModule(t *testing.T, source, tests string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":      "module example.com/sample\n\ngo 1.24\n",
		"p/p.go":      source,
		"p/p_test.go": tests,
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func sitesOf(t *testing.T, root, file string) []lang.MutantSite {
	t.Helper()
	fc := diff.FileChange{Path: file, Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: 1 << 20}}}
	sites, err := mutantGeneratorImpl{}.GenerateMutants(filepath.Join(root, file), fc, nil)
	if err != nil {
		t.Fatal(err)
	}
	return sites
}

// runSession tests every mutant of p/p.go and returns the outcome of each,
// keyed by "line operator".
func runSession(t *testing.T, root string, cfg lang.SessionConfig) (map[string]lang.MutantOutcome, *goSession) {
	t.Helper()
	sites := sitesOf(t, root, "p/p.go")
	cfg.RepoPath, cfg.WorkDir, cfg.Workers = root, t.TempDir(), 4
	if cfg.Timeout == 0 {
		cfg.Timeout = time.Minute
	}
	session, err := (&Language{}).NewMutationSession(context.Background(), cfg, sites)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	got := map[string]lang.MutantOutcome{}
	for i, s := range sites {
		got[lineOp(root, s)] = session.Run(context.Background(), i).Outcome
	}
	return got, session.(*goSession)
}

// lineOp names a site by the text of its source line and its operator, so
// expectations survive edits to the sample.
func lineOp(root string, s lang.MutantSite) string {
	data, _ := os.ReadFile(filepath.Join(root, s.File))
	return strings.TrimSpace(strings.Split(string(data), "\n")[s.Line-1]) + " | " + s.Operator
}

func TestSession_ClassifiesEveryOutcome(t *testing.T) {
	root := writeModule(t, sampleSource, sampleTests)
	got, session := runSession(t, root, lang.SessionConfig{})

	want := map[string]lang.MutantOutcome{
		"func note() { calls++ } | incdec":                                lang.OutcomeKilled,
		"func Cmp(a, b int) bool { return a > b } | conditional_boundary": lang.OutcomeKilled,
		"func Cmp(a, b int) bool { return a > b } | return_value":         lang.OutcomeKilled,
		"func Eq(a, b int) bool { return a == b } | negate_conditional":   lang.OutcomeKilled,
		"func Eq(a, b int) bool { return a == b } | return_value":         lang.OutcomeKilled,
		"func Add(a, b int) int { return a + b } | math_operator":         lang.OutcomeKilled,
		"func Add(a, b int) int { return a + b } | return_value":          lang.OutcomeKilled,
		"func Flag() bool { return true } | boolean_substitution":         lang.OutcomeKilled,
		"func Flag() bool { return true } | return_value":                 lang.OutcomeKilled,
		"c++ | incdec":                                      lang.OutcomeKilled,
		"return c | return_value":                           lang.OutcomeKilled,
		"if x < 0 { | branch_removal":                       lang.OutcomeKilled,
		"if x < 0 { | conditional_boundary":                 lang.OutcomeKilled,
		"return 0, errors.New(\"negative\") | return_value": lang.OutcomeKilled,
		"note() | statement_deletion":                       lang.OutcomeKilled,
		"return x, nil | return_value":                      lang.OutcomeKilled,
		// string - string does not compile.
		"func Join(a string) string { return a + \"!\" } | math_operator":     lang.OutcomeBuildFailed,
		"func Join(a string) string { return a + \"!\" } | return_value":      lang.OutcomeKilled,
		"func Untested(a, b int) bool { return a != b } | negate_conditional": lang.OutcomeNotCovered,
		"func Untested(a, b int) bool { return a != b } | return_value":       lang.OutcomeNotCovered,
		"if a > 100 { | branch_removal":                                       lang.OutcomeNotCovered, // the guard sits inside the branch, which no test enters
		"println() | statement_deletion":                                      lang.OutcomeNotCovered,
		"if a > 100 { | conditional_boundary":                                 lang.OutcomeSurvived,
		"return a | return_value":                                             lang.OutcomeKilled,
	}
	if !reflect.DeepEqual(got, want) {
		for k, w := range want {
			if got[k] != w {
				t.Errorf("%s: got %v, want %v", k, got[k], w)
			}
		}
		for k := range got {
			if _, ok := want[k]; !ok {
				t.Errorf("unexpected mutant %s = %v", k, got[k])
			}
		}
	}

	p := session.pkgs[0]
	if len(p.bins) != 1 || p.bins[0].whole != nil || len(p.bins[0].tests) != 8 {
		t.Fatalf("want one binary traced per test (8 tests), got %+v", p.bins)
	}
	embedded := 0
	for _, ok := range p.embedded {
		if ok {
			embedded++
		}
	}
	if embedded != len(want)-1 {
		t.Errorf("embedded %d mutants, want all but the one that cannot compile (%d)", embedded, len(want)-1)
	}
}

// Tests that only pass when run together still get their mutants tested,
// through a single trace of the whole suite.
func TestSession_InterdependentTestsUseWholeSuite(t *testing.T) {
	tests := `package p

import "testing"

var ready bool

func TestA(t *testing.T) { ready = true }

func TestB(t *testing.T) {
	if !ready || Add(2, 3) != 5 {
		t.Fatal("B")
	}
}
`
	root := writeModule(t, "package p\n\nfunc Add(a, b int) int { return a + b }\n\nfunc Other(a int) int { return a * 2 }\n", tests)
	got, session := runSession(t, root, lang.SessionConfig{})
	if b := session.pkgs[0].bins[0]; b.whole == nil || b.tests != nil {
		t.Fatalf("want whole-suite mode, got %+v", b)
	}
	if got["func Add(a, b int) int { return a + b } | math_operator"] != lang.OutcomeKilled {
		t.Errorf("Add mutant: %v", got)
	}
	if got["func Other(a int) int { return a * 2 } | math_operator"] != lang.OutcomeNotCovered {
		t.Errorf("Other mutant: %v", got)
	}
}

// If the tests fail before any mutation, every mutant would look killed.
// They must be reported as baseline-failed instead.
func TestSession_FailingBaselineRunsNothing(t *testing.T) {
	root := writeModule(t, "package p\n\nfunc Add(a, b int) int { return a + b }\n",
		"package p\n\nimport \"testing\"\n\nfunc TestRed(t *testing.T) { t.Fatal(Add(1, 1)) }\n")
	var log bytes.Buffer
	got, _ := runSession(t, root, lang.SessionConfig{Log: &log})
	for k, o := range got {
		if o != lang.OutcomeBaselineFailed {
			t.Errorf("%s: got %v, want baseline_failed", k, o)
		}
	}
	if !strings.Contains(log.String(), "tests fail without any mutation") {
		t.Errorf("the failing baseline must be reported, log:\n%s", log.String())
	}
}

// One test that fails without any mutation must not cost the package its
// other tests: it is left out and named, the mutants the passing tests reach
// are tested, and a mutant only the failing test reaches is baseline-failed.
func TestSession_FailingTestIsLeftOut(t *testing.T) {
	source := `package p

func Add(a, b int) int { return a + b }

func Twice(a int) int { return a * 2 }

func Unused(a int) int { return a - 1 }
`
	tests := `package p

import "testing"

func TestAdd(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Fatal("Add")
	}
}

func TestRed(t *testing.T) {
	if Add(2, 3) != 5 || Twice(2) != 4 {
		t.Fatal("unreachable")
	}
	t.Fatal("always red")
}
`
	root := writeModule(t, source, tests)
	var log bytes.Buffer
	got, session := runSession(t, root, lang.SessionConfig{Log: &log})
	want := map[string]lang.MutantOutcome{
		"func Add(a, b int) int { return a + b } | math_operator": lang.OutcomeKilled,
		"func Twice(a int) int { return a * 2 } | math_operator":  lang.OutcomeBaselineFailed,
		"func Unused(a int) int { return a - 1 } | math_operator": lang.OutcomeNotCovered,
	}
	for k, o := range want {
		if got[k] != o {
			t.Errorf("%s: got %v, want %v", k, got[k], o)
		}
	}
	b := session.pkgs[0].bins[0]
	if len(b.tests) != 1 || b.tests[0].name != "TestAdd" || b.whole != nil {
		t.Errorf("want only TestAdd kept, got %+v", b)
	}
	if !strings.Contains(log.String(), "1 test(s) fail without any mutation and were left out: TestRed") {
		t.Errorf("the left-out test must be named, log:\n%s", log.String())
	}
}

func TestSession_TestPatternLimitsTheTests(t *testing.T) {
	root := writeModule(t, sampleSource, sampleTests)
	got, session := runSession(t, root, lang.SessionConfig{TestPattern: "TestAdd"})
	if n := len(session.pkgs[0].bins[0].tests); n != 1 {
		t.Fatalf("traced %d tests, want only TestAdd", n)
	}
	if got["func Add(a, b int) int { return a + b } | math_operator"] != lang.OutcomeKilled {
		t.Error("TestAdd still kills the Add mutant")
	}
	if got["func Cmp(a, b int) bool { return a > b } | conditional_boundary"] != lang.OutcomeNotCovered {
		t.Error("with TestCmp filtered out, nothing covers the Cmp mutant")
	}
}

func TestSession_PackageWithoutTests(t *testing.T) {
	root := writeModule(t, "package p\n\nfunc Add(a, b int) int { return a + b }\n", "package p\n")
	os.Remove(filepath.Join(root, "p/p_test.go"))
	got, _ := runSession(t, root, lang.SessionConfig{})
	for k, o := range got {
		if o != lang.OutcomeNotCovered {
			t.Errorf("%s: got %v, want not_covered", k, o)
		}
	}
}

func TestSession_CancelledRunReportsNotRun(t *testing.T) {
	root := writeModule(t, sampleSource, sampleTests)
	sites := sitesOf(t, root, "p/p.go")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	session, _ := (&Language{}).NewMutationSession(ctx, lang.SessionConfig{RepoPath: root, WorkDir: t.TempDir(), Workers: 2, Timeout: time.Minute}, sites)
	defer session.Close()
	if got := session.Run(ctx, 0).Outcome; got != lang.OutcomeNotRun {
		t.Errorf("outcome = %v, want not_run", got)
	}
}

// A test in a package below the mutated one counts, as it did when every
// mutant ran `go test ./...` from the mutated package's directory.
func TestSession_SubpackageTestsKillMutants(t *testing.T) {
	root := writeModule(t, "package p\n\nfunc Add(a, b int) int { return a + b }\n", "package p\n")
	os.Remove(filepath.Join(root, "p/p_test.go"))
	sub := filepath.Join(root, "p/sub")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(sub, "sub_test.go"), []byte("package sub\n\nimport (\n\t\"testing\"\n\n\t\"example.com/sample/p\"\n)\n\nfunc TestAdd(t *testing.T) {\n\tif p.Add(2, 3) != 5 {\n\t\tt.Fatal()\n\t}\n}\n"), 0o644)
	os.WriteFile(filepath.Join(sub, "sub.go"), []byte("package sub\n"), 0o644)
	other := filepath.Join(root, "p/unrelated")
	os.MkdirAll(other, 0o755)
	os.WriteFile(filepath.Join(other, "u_test.go"), []byte("package unrelated\n\nimport \"testing\"\n\nfunc TestU(t *testing.T) {}\n"), 0o644)

	got, session := runSession(t, root, lang.SessionConfig{})
	if got["func Add(a, b int) int { return a + b } | math_operator"] != lang.OutcomeKilled {
		t.Errorf("got %v", got)
	}
	if n := len(session.pkgs[0].bins); n != 1 {
		t.Errorf("built %d test binaries, want 1: the unrelated package does not link p", n)
	}
}

func TestParseTestPackages(t *testing.T) {
	out := strings.Join([]string{
		"example.com/m/p|/src/p|fmt,os",
		"example.com/m/p.test|/src/p|example.com/m/p [example.com/m/p.test],testing",
		"example.com/m/p [example.com/m/p.test]|/src/p|fmt",
		"example.com/m/p/sub.test|/src/p/sub|example.com/m/p,testing",
		"example.com/m/p/far.test|/src/p/far|example.com/m/pp,testing",
		"garbage line",
		"",
	}, "\n")
	got := parseTestPackages("example.com/m/p", out)
	want := []testPackage{{dir: "/src/p", own: true}, {dir: "/src/p/sub"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestDependsOn(t *testing.T) {
	deps := "a/b [a/b.test],a/bc,x"
	if !dependsOn(deps, "a/b") || !dependsOn(deps, "x") {
		t.Error("plain and test-variant dependencies both count")
	}
	if dependsOn(deps, "a") || dependsOn("a/bc", "a/b") {
		t.Error("a path prefix is not a dependency")
	}
}

func TestRunFilter(t *testing.T) {
	if got := runFilter(nil, ""); got != nil {
		t.Errorf("whole suite, no pattern: %v", got)
	}
	if got := runFilter(nil, "TestX"); !reflect.DeepEqual(got, []string{"-test.run", "TestX"}) {
		t.Errorf("whole suite with pattern: %v", got)
	}
	if got := runFilter([]string{"TestA", "Test.B"}, "ignored"); !reflect.DeepEqual(got, []string{"-test.run", `^(TestA|Test\.B)$`}) {
		t.Errorf("named tests: %v", got)
	}
}

func TestGroupsReaching(t *testing.T) {
	b := &testBinary{tests: []tracedTest{
		{name: "TestFast", duration: 10 * time.Millisecond, reach: map[int]bool{1: true, 2: true}},
		{name: "TestSlow", duration: slowTestThreshold, reach: map[int]bool{1: true}},
		{name: "TestOther", duration: time.Millisecond, reach: map[int]bool{3: true}},
		{name: "TestFast2", duration: 5 * time.Millisecond, reach: map[int]bool{1: true}},
	}}
	got := b.groupsReaching(1)
	want := []testGroup{
		{names: []string{"TestFast", "TestFast2"}, baseline: 15 * time.Millisecond},
		{names: []string{"TestSlow"}, baseline: slowTestThreshold},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want fast tests first, then slow: %+v", got, want)
	}
	if got := b.groupsReaching(2); len(got) != 1 || len(got[0].names) != 1 {
		t.Errorf("mutant 2: %+v", got)
	}
	if b.groupsReaching(9) != nil {
		t.Error("an unreached mutant has no groups")
	}

	whole := &testBinary{whole: &tracedTest{duration: time.Second, reach: map[int]bool{4: true}}}
	if got := whole.groupsReaching(4); len(got) != 1 || got[0].names != nil || got[0].baseline != time.Second {
		t.Errorf("whole mode: %+v", got)
	}
	if whole.groupsReaching(5) != nil {
		t.Error("whole mode: unreached mutant")
	}
}

func TestMutantTimeout(t *testing.T) {
	cases := []struct{ baseline, ceiling, want time.Duration }{
		{0, time.Minute, 5 * time.Second},
		{time.Second, time.Minute, 9 * time.Second},
		{20 * time.Second, time.Minute, time.Minute},            // capped by the ceiling
		{60 * time.Second, 30 * time.Second, 125 * time.Second}, // ceiling too low for a healthy run
	}
	for _, c := range cases {
		if got := mutantTimeout(c.baseline, c.ceiling); got != c.want {
			t.Errorf("mutantTimeout(%v, %v) = %v, want %v", c.baseline, c.ceiling, got, c.want)
		}
	}
}

func TestBaselineTimeout(t *testing.T) {
	if got := baselineTimeout(lang.SessionConfig{Timeout: time.Second}); got != 10*time.Minute {
		t.Errorf("got %v, want the 10m floor", got)
	}
	if got := baselineTimeout(lang.SessionConfig{Timeout: time.Hour}); got != time.Hour {
		t.Errorf("got %v, want the configured timeout", got)
	}
}

func TestClassify(t *testing.T) {
	failed := errors.New("exit 1")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		ctx  context.Context
		res  procResult
		want lang.MutantOutcome
	}{
		{context.Background(), procResult{}, lang.OutcomeSurvived},
		{context.Background(), procResult{err: failed}, lang.OutcomeBuildFailed},
		{context.Background(), procResult{err: failed, timedOut: true}, lang.OutcomeTimedOut},
		{cancelled, procResult{err: failed, timedOut: true}, lang.OutcomeNotRun},
	}
	for _, c := range cases {
		if got := classify(c.ctx, c.res, lang.OutcomeBuildFailed).Outcome; got != c.want {
			t.Errorf("classify(%+v) = %v, want %v", c.res, got, c.want)
		}
	}
	if got := classify(context.Background(), procResult{err: failed, output: "why"}, lang.OutcomeKilled); got.Output != "why" {
		t.Errorf("a failure keeps its output, got %q", got.Output)
	}
}

func TestReadTrace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace")
	os.WriteFile(path, []byte("3\n17\nnoise\n3\n"), 0o644)
	if got := readTrace(path); !reflect.DeepEqual(got, map[int]bool{3: true, 17: true}) {
		t.Errorf("got %v", got)
	}
	if got := readTrace(path + ".missing"); len(got) != 0 {
		t.Errorf("missing file: got %v, want empty", got)
	}
}

func TestProcRun(t *testing.T) {
	ok := proc{timeout: time.Minute, name: "go", args: []string{"version"}}.run(context.Background())
	if ok.err != nil || ok.timedOut || !strings.Contains(ok.output, "go version") {
		t.Errorf("go version: %+v", ok)
	}
	bad := proc{timeout: time.Minute, name: "go", args: []string{"no-such-command"}}.run(context.Background())
	if bad.err == nil || bad.timedOut {
		t.Errorf("failing command: %+v", bad)
	}
	slow := proc{timeout: time.Nanosecond, name: "go", args: []string{"version"}}.run(context.Background())
	if slow.err == nil || !slow.timedOut {
		t.Errorf("a run past its timeout must report timedOut: %+v", slow)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	gone := proc{timeout: time.Minute, name: "go", args: []string{"version"}}.run(ctx)
	if gone.err == nil || gone.timedOut {
		t.Errorf("a cancelled run is not a timeout: %+v", gone)
	}
	env := proc{timeout: time.Minute, name: "go", args: []string{"env", "GOFLAGS"}, env: []string{"GOFLAGS=-mod=mod"}}.run(context.Background())
	if strings.TrimSpace(env.output) != "-mod=mod" {
		t.Errorf("env not passed to the child: %q", env.output)
	}
}

func TestTail(t *testing.T) {
	if got := tail([]byte("abcdef"), 3); got != "def" {
		t.Errorf("got %q", got)
	}
	if got := tail([]byte("abc"), 3); got != "abc" {
		t.Errorf("got %q", got)
	}
	long := bytes.Repeat([]byte("x"), outputTail+10)
	if got := tail(long, 0); len(got) != outputTail {
		t.Errorf("default keep = %d, want %d", len(got), outputTail)
	}
}

func TestOverlayTimeout(t *testing.T) {
	if got := overlayTimeout(time.Second, time.Minute); got != time.Minute {
		t.Errorf("got %v, want the configured ceiling", got)
	}
	if got := overlayTimeout(time.Minute, time.Minute); got != 210*time.Second {
		t.Errorf("got %v, want 3x the baseline plus 30s", got)
	}
}

// newTestSession prepares a session over the given sites and waits for
// every package to be ready.
func newTestSession(t *testing.T, root string, cfg lang.SessionConfig, sites []lang.MutantSite) *goSession {
	t.Helper()
	cfg.RepoPath, cfg.WorkDir, cfg.Workers, cfg.Timeout = root, t.TempDir(), 2, time.Minute
	session, err := (&Language{}).NewMutationSession(context.Background(), cfg, sites)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)
	s := session.(*goSession)
	for _, p := range s.pkgs {
		<-p.ready
	}
	return s
}

// A site the applier cannot locate is reported as not applied, not as a
// surviving mutant.
func TestSession_UnlocatableSiteIsNotApplied(t *testing.T) {
	root := writeModule(t, sampleSource, sampleTests)
	bogus := lang.MutantSite{File: "p/p.go", Line: 1, Offset: 3, Operator: "incdec", Description: "++ -> --"}
	s := newTestSession(t, root, lang.SessionConfig{}, []lang.MutantSite{bogus})
	if got := s.Run(context.Background(), 0).Outcome; got != lang.OutcomeNotApplied {
		t.Errorf("outcome = %v, want not_applied", got)
	}
}

// The user's test pattern scopes the unmutated run too: a failing test
// outside the pattern must not stop the package's mutants from running.
func TestSession_TestPatternScopesTheBaseline(t *testing.T) {
	tests := sampleTests + "\nfunc TestRed(t *testing.T) { t.Fatal(\"red\") }\n"
	root := writeModule(t, sampleSource, tests)
	got, _ := runSession(t, root, lang.SessionConfig{TestPattern: "TestJoin"})
	if o := got["func Join(a string) string { return a + \"!\" } | return_value"]; o != lang.OutcomeKilled {
		t.Errorf("schemata path: got %v, want killed", o)
	}
	// This mutant cannot share the build, so it takes the overlay path and
	// its own baseline run.
	if o := got["func Join(a string) string { return a + \"!\" } | math_operator"]; o != lang.OutcomeBuildFailed {
		t.Errorf("overlay path: got %v, want build_failed", o)
	}
}

func TestSession_TestsThatDoNotCompileRunNothing(t *testing.T) {
	root := writeModule(t, "package p\n\nfunc Add(a, b int) int { return a + b }\n",
		"package p\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { undefined() }\n")
	var log bytes.Buffer
	got, _ := runSession(t, root, lang.SessionConfig{Log: &log})
	for k, o := range got {
		if o != lang.OutcomeBaselineFailed {
			t.Errorf("%s: got %v, want baseline_failed", k, o)
		}
	}
	if !strings.Contains(log.String(), "its instrumented tests do not build; testing its 2 mutants one build at a time") {
		t.Errorf("the fallback must be explained, log:\n%s", log.String())
	}
}

func TestSession_CancelledPreparationLogsNothing(t *testing.T) {
	root := writeModule(t, sampleSource, sampleTests)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var log bytes.Buffer
	session, _ := (&Language{}).NewMutationSession(ctx, lang.SessionConfig{RepoPath: root, WorkDir: t.TempDir(), Workers: 2, Timeout: time.Minute, Log: &log}, sitesOf(t, root, "p/p.go"))
	session.Close()
	if log.Len() != 0 {
		t.Errorf("a cancelled run is not a package problem, log:\n%s", log.String())
	}
}

func TestTestPackages_FallsBackToThePackageItself(t *testing.T) {
	dir := t.TempDir() // not inside any module, so go list fails
	p := &mutantPackage{session: &goSession{cfg: lang.SessionConfig{Timeout: time.Minute}}, dir: dir}
	got := p.testPackages(context.Background())
	if want := []testPackage{{dir: dir, own: true}}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestInstrumentation_WriteErrors(t *testing.T) {
	root := writeModule(t, sampleSource, sampleTests)
	sites := sitesOf(t, root, "p/p.go")
	s := &goSession{cfg: lang.SessionConfig{RepoPath: root}, sites: sites}
	p := &mutantPackage{session: s, dir: filepath.Join(root, "p"), work: filepath.Join(t.TempDir(), "missing"), sites: []int{0}}
	if err := newInstrumentation(p).write(); err == nil {
		t.Error("want an error when the work directory does not exist")
	}
	os.WriteFile(filepath.Join(root, "p/p.go"), []byte("package"), 0o644)
	p.work = t.TempDir()
	if err := newInstrumentation(p).write(); err == nil {
		t.Error("want an error when a mutated file does not parse")
	}
}

func TestInstrumentation_DropBlamed(t *testing.T) {
	root := writeModule(t, sampleSource, sampleTests)
	sites := sitesOf(t, root, "p/p.go")
	all := make([]int, len(sites))
	for i := range all {
		all[i] = i
	}
	s := &goSession{cfg: lang.SessionConfig{RepoPath: root}, sites: sites}
	p := &mutantPackage{session: s, dir: filepath.Join(root, "p"), work: t.TempDir(), sites: all}
	in := newInstrumentation(p)
	if err := in.write(); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(p.work, "f0.go")
	line, col := lineCol(string(in.sources[out]), "a, \"!\")")
	id, err := strconv.Atoi(strings.Split(strings.Split(string(in.sources[out]), "__dgAdd(")[2], ",")[0])
	if err != nil {
		t.Fatal(err)
	}

	diag := fmt.Sprintf("# example.com/sample/p\n%s:%d:%d: string does not satisfy __dgNum\nother.go:1:1: unrelated\n", out, line, col)
	if !in.dropBlamed(diag) {
		t.Fatal("the blamed mutant should be dropped")
	}
	if len(in.excluded) != 1 || !in.excluded[id-1] {
		t.Errorf("excluded = %v, want only site %d", in.excluded, id-1)
	}
	if in.dropBlamed(diag) {
		t.Error("blaming an already dropped mutant is not progress")
	}
	if in.dropBlamed("no diagnostics here") {
		t.Error("nothing to blame")
	}

	// A path relative to the package directory resolves to the same file.
	rel, _ := filepath.Rel(p.dir, out)
	fresh := newInstrumentation(p)
	fresh.write()
	if !fresh.dropBlamed(fmt.Sprintf("%s:%d:%d: msg\n", rel, line, col)) {
		t.Error("a relative diagnostic path should resolve against the package directory")
	}

	if err := in.write(); err != nil {
		t.Fatal(err)
	}
	if in.embedded[id-1] {
		t.Error("an excluded mutant must not be embedded again")
	}
}

func TestHashPackageDir(t *testing.T) {
	if err := hashPackageDir(io.Discard, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("want an error for an unreadable directory")
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "testdata/deep"), 0o755)
	os.MkdirAll(filepath.Join(dir, "other"), 0o755)
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "testdata/deep/x.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "other/y.txt"), []byte("y"), 0o644)
	var buf bytes.Buffer
	if err := hashPackageDir(&buf, dir); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, "a.go\x00package a\n\x0010\x00") {
		t.Errorf("a file contributes its path, content and size: %q", got)
	}
	if !strings.Contains(got, "x.txt") || strings.Contains(got, "y.txt") {
		t.Errorf("testdata is hashed recursively, other subdirectories are not: %q", got)
	}
	buf.Reset()
	hashFile(&buf, filepath.Join(dir, "gone"))
	if !strings.HasSuffix(buf.String(), "gone\x00") {
		t.Errorf("an unreadable file contributes only its path: %q", buf.String())
	}
}
