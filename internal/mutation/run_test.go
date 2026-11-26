package mutation

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
	"github.com/0xPolygon/diffguard/internal/report"
)

// sessionLanguage is a fakeLanguage that also offers a mutation session and
// a fingerprint, the two optional capabilities the orchestrator probes for.
type sessionLanguage struct {
	fakeLanguage
	// outcome decides each mutant's result from its site.
	outcome func(lang.MutantSite) lang.MutantOutcome
	// onRun, if set, is called at the start of every Run.
	onRun       func(ctx context.Context)
	startErr    error
	fingerprint string
	fpErr       error

	runs   atomic.Int64
	closed atomic.Bool
	gotCfg lang.SessionConfig
}

func (l *sessionLanguage) NewMutationSession(_ context.Context, cfg lang.SessionConfig, sites []lang.MutantSite) (lang.MutationSession, error) {
	if l.startErr != nil {
		return nil, l.startErr
	}
	l.gotCfg = cfg
	return &fakeSession{lang: l, sites: sites}, nil
}

func (l *sessionLanguage) MutationFingerprint(string, string) (string, error) {
	return l.fingerprint, l.fpErr
}

type fakeSession struct {
	lang  *sessionLanguage
	sites []lang.MutantSite
}

func (s *fakeSession) Run(ctx context.Context, idx int) lang.MutantResult {
	s.lang.runs.Add(1)
	if s.lang.onRun != nil {
		s.lang.onRun(ctx)
	}
	return lang.MutantResult{Outcome: s.lang.outcome(s.sites[idx]), Output: "out"}
}

func (s *fakeSession) Close() { s.lang.closed.Store(true) }

func killedCount(mutants []Mutant) int {
	n := 0
	for _, m := range mutants {
		if m.Killed {
			n++
		}
	}
	return n
}

// byLine maps a mutant's line number to its outcome.
func byLine(outcomes ...lang.MutantOutcome) func(lang.MutantSite) lang.MutantOutcome {
	return func(s lang.MutantSite) lang.MutantOutcome { return outcomes[s.Line-1] }
}

func TestRunMutantsParallel_SessionOutcomes(t *testing.T) {
	l := &sessionLanguage{outcome: byLine(lang.OutcomeKilled, lang.OutcomeSurvived, lang.OutcomeTimedOut, lang.OutcomeBuildFailed)}
	mutants := numbered(4, "negate_conditional")
	err := runMutantsParallel("/repo", mutants, l, Options{Workers: 2, TestPattern: "TestX", TestTimeout: time.Minute}, "/work")
	killed := killedCount(mutants)
	if err != nil {
		t.Fatal(err)
	}
	if killed != 2 {
		t.Errorf("killed = %d, want 2 (a kill and a timeout)", killed)
	}
	if !mutants[0].Killed || mutants[0].TestOutput != "out" {
		t.Errorf("killed mutant should keep its output: %+v", mutants[0])
	}
	if mutants[1].Killed || mutants[1].TestOutput != "" {
		t.Errorf("survivor should carry no output: %+v", mutants[1])
	}
	if mutants[3].Outcome != lang.OutcomeBuildFailed || mutants[3].Killed {
		t.Errorf("build failure must not count as a kill: %+v", mutants[3])
	}
	if !l.closed.Load() {
		t.Error("the session must be closed")
	}
	want := lang.SessionConfig{RepoPath: "/repo", WorkDir: "/work", Timeout: time.Minute, TestPattern: "TestX", Workers: 2}
	if l.gotCfg != want {
		t.Errorf("session config = %+v, want %+v", l.gotCfg, want)
	}
}

func TestRunMutantsParallel_SkipsMutantsThatAlreadyHaveAnOutcome(t *testing.T) {
	l := &sessionLanguage{outcome: byLine(lang.OutcomeUnset, lang.OutcomeSurvived, lang.OutcomeUnset)}
	mutants := numbered(3, "negate_conditional")
	mutants[0].setResult(lang.MutantResult{Outcome: lang.OutcomeKilled})
	mutants[2].setResult(lang.MutantResult{Outcome: lang.OutcomeKilled})
	_ = runMutantsParallel("/repo", mutants, l, Options{Workers: 1}, t.TempDir())
	killed := killedCount(mutants)
	if l.runs.Load() != 1 {
		t.Errorf("ran %d mutants, want only the 1 without a cached outcome", l.runs.Load())
	}
	if killed != 2 || mutants[1].Outcome != lang.OutcomeSurvived {
		t.Errorf("killed = %d, mutants = %+v", killed, mutants)
	}
}

func TestRunMutantsParallel_SessionStartError(t *testing.T) {
	l := &sessionLanguage{startErr: errors.New("boom")}
	err := runMutantsParallel("/repo", numbered(1, "x"), l, Options{}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want the session error", err)
	}
}

// When the budget runs out, mutants that never started are reported as not
// run; they are neither dropped nor counted as survivors.
func TestRunMutantsParallel_BudgetMarksRestNotRun(t *testing.T) {
	l := &sessionLanguage{outcome: byLine(make([]lang.MutantOutcome, 6)...)}
	l.outcome = func(lang.MutantSite) lang.MutantOutcome { return lang.OutcomeKilled }
	l.onRun = func(ctx context.Context) { <-ctx.Done() }
	mutants := numbered(6, "negate_conditional")
	err := runMutantsParallel("/repo", mutants, l, Options{Workers: 1, Budget: 30 * time.Millisecond}, t.TempDir())
	killed := killedCount(mutants)
	if err != nil {
		t.Fatal(err)
	}
	if l.runs.Load() != 1 || killed != 1 {
		t.Errorf("runs = %d, killed = %d; want exactly the one mutant in flight", l.runs.Load(), killed)
	}
	for _, m := range mutants[1:] {
		if m.Outcome != lang.OutcomeNotRun {
			t.Errorf("mutant on line %d: outcome %v, want not_run", m.Line, m.Outcome)
		}
	}
}

func TestRunMutantsParallel_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	l := &sessionLanguage{outcome: func(lang.MutantSite) lang.MutantOutcome { return lang.OutcomeKilled }}
	mutants := numbered(3, "negate_conditional")
	_ = runMutantsParallel("/repo", mutants, l, Options{Context: ctx}, t.TempDir())
	killed := killedCount(mutants)
	if killed != 0 || l.runs.Load() != 0 {
		t.Errorf("killed = %d, runs = %d; a cancelled run tests nothing", killed, l.runs.Load())
	}
}

func TestRunMutantsParallel_EarlyStopSkipsSettledTier(t *testing.T) {
	l := &sessionLanguage{outcome: func(lang.MutantSite) lang.MutantOutcome { return lang.OutcomeKilled }}
	mutants := append(numbered(100, "negate_conditional"), Mutant{File: "a.go", Line: 500, Operator: "statement_deletion"})
	runMutantsParallel("/repo", mutants, l, Options{Workers: 1, Confidence: 0.95, Tier1Threshold: 80}, t.TempDir())
	if got := l.runs.Load(); got != earlyStopMinSamples+1 {
		t.Errorf("ran %d mutants, want %d tier-1 then the tier-3 one", got, earlyStopMinSamples+1)
	}
	if mutants[earlyStopMinSamples].Outcome != lang.OutcomeStoppedEarly {
		t.Errorf("outcome = %v, want stopped_early", mutants[earlyStopMinSamples].Outcome)
	}
	if mutants[100].Outcome != lang.OutcomeKilled {
		t.Error("the ungated tier still runs")
	}
}

func TestRunMutantsParallel_RespectsWorkerLimit(t *testing.T) {
	var mu sync.Mutex
	running, peak := 0, 0
	l := &sessionLanguage{outcome: func(lang.MutantSite) lang.MutantOutcome { return lang.OutcomeKilled }}
	l.onRun = func(context.Context) {
		mu.Lock()
		running++
		peak = max(peak, running)
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		mu.Lock()
		running--
		mu.Unlock()
	}
	runMutantsParallel("/repo", numbered(12, "x"), l, Options{Workers: 3}, t.TempDir())
	if peak != 3 {
		t.Errorf("peak concurrency = %d, want exactly the 3 workers", peak)
	}
}

func TestRunMutantsParallel_Progress(t *testing.T) {
	old := progressInterval
	progressInterval = time.Millisecond
	defer func() { progressInterval = old }()

	var buf syncBuffer
	l := &sessionLanguage{outcome: byLine(lang.OutcomeKilled, lang.OutcomeSurvived)}
	l.onRun = func(context.Context) { time.Sleep(20 * time.Millisecond) }
	runMutantsParallel("/repo", numbered(2, "x"), l, Options{Workers: 1, Progress: &buf}, t.TempDir())
	out := buf.String()
	if !strings.Contains(out, "mutation: 0/2 mutants done, 0 killed") {
		t.Errorf("missing a periodic line while mutants run:\n%s", out)
	}
	if !strings.HasSuffix(out, "mutation: 2/2 mutants done, 1 killed, 0s elapsed\n") {
		t.Errorf("missing the final line:\n%s", out)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type bytesApplier struct{}

func (bytesApplier) ApplyMutation(string, lang.MutantSite) ([]byte, error) {
	return []byte("mutant"), nil
}

type nilApplier struct{}

func (nilApplier) ApplyMutation(string, lang.MutantSite) ([]byte, error) { return nil, nil }

type scriptedRunner struct {
	killed bool
	err    error
}

func (r scriptedRunner) RunTest(lang.TestRunConfig) (bool, string, error) {
	return r.killed, "out", r.err
}

func TestLegacyExecutor_Outcomes(t *testing.T) {
	cases := []struct {
		name    string
		applier lang.MutantApplier
		runner  scriptedRunner
		want    lang.MutantOutcome
	}{
		{"applier declines", nilApplier{}, scriptedRunner{killed: true}, lang.OutcomeNotApplied},
		{"killed", bytesApplier{}, scriptedRunner{killed: true}, lang.OutcomeKilled},
		{"survived", bytesApplier{}, scriptedRunner{}, lang.OutcomeSurvived},
		{"runner error", bytesApplier{}, scriptedRunner{killed: true, err: errors.New("x")}, lang.OutcomeNotRun},
	}
	for _, c := range cases {
		dir := t.TempDir()
		mutants := numbered(1, "negate_conditional")
		l := &fakeLanguage{applier: c.applier, runner: c.runner}
		runMutantsParallel(dir, mutants, l, Options{Workers: 1}, dir)
		if mutants[0].Outcome != c.want {
			t.Errorf("%s: outcome = %v, want %v", c.name, mutants[0].Outcome, c.want)
		}
	}
}

func TestOptionsSeedAndRunContext(t *testing.T) {
	if (Options{}).seed() != 1 || (Options{Seed: 9}).seed() != 9 {
		t.Error("seed defaults to 1 and honors an explicit value")
	}
	ctx, cancel := Options{}.runContext()
	if _, bounded := ctx.Deadline(); bounded {
		t.Error("no budget means no deadline")
	}
	cancel()
	ctx, cancel = Options{Budget: time.Hour}.runContext()
	defer cancel()
	if _, bounded := ctx.Deadline(); !bounded {
		t.Error("a budget sets a deadline")
	}
}

func TestBuildSection_ExcludesUnscoredOutcomes(t *testing.T) {
	mutants := numbered(6, "negate_conditional")
	for i, o := range []lang.MutantOutcome{lang.OutcomeKilled, lang.OutcomeTimedOut, lang.OutcomeSurvived, lang.OutcomeNotCovered, lang.OutcomeBuildFailed, lang.OutcomeNotApplied} {
		mutants[i].setResult(lang.MutantResult{Outcome: o})
	}
	mutants[0].Cached = true
	s := buildSection(mutants, Options{Tier1Threshold: 50})
	stats := s.Stats.(map[string]any)
	if stats["total"] != 4 || stats["killed"] != 2 || stats["survived"] != 2 || stats["score"] != 50.0 {
		t.Errorf("stats = %v; want 2 killed of 4 scored", stats)
	}
	outcomes := stats["outcomes"].(map[string]int)
	if stats["generated"] != 6 || stats["cached"] != 1 || outcomes["build_failed"] != 1 || outcomes["timed_out"] != 1 || outcomes["killed"] != 1 {
		t.Errorf("stats = %v", stats)
	}
	for _, want := range []string{"Score: 50.0% (2/4 killed, 2 survived)", "1 timed out", "1 not covered", "1 do not compile (excluded)", "1 not applied (excluded)", "1 from cache"} {
		if !strings.Contains(s.Summary, want) {
			t.Errorf("summary %q is missing %q", s.Summary, want)
		}
	}
	if strings.Contains(s.Summary, "NOT RUN") || strings.Contains(s.Summary, "settled early") {
		t.Errorf("summary mentions outcomes that did not occur: %q", s.Summary)
	}
	if s.Severity != report.SeverityPass {
		t.Errorf("severity = %v, want PASS at exactly the threshold", s.Severity)
	}
	if len(s.Findings) != 2 || !strings.HasPrefix(s.Findings[0].Message, "SURVIVED:") || !strings.HasPrefix(s.Findings[1].Message, "NOT COVERED:") {
		t.Errorf("findings = %+v", s.Findings)
	}
}

func TestBuildSection_FindingsSortedByFileThenLine(t *testing.T) {
	mutants := []Mutant{
		{File: "b.go", Line: 9, Operator: "statement_deletion"},
		{File: "a.go", Line: 7, Operator: "negate_conditional"},
		{File: "a.go", Line: 2, Operator: "statement_deletion"},
	}
	f := buildSection(mutants, Options{}).Findings
	if f[0].Line != 2 || f[1].Line != 7 || f[2].File != "b.go" {
		t.Errorf("findings out of order: %+v", f)
	}
}

// A gating tier with mutants that never ran cannot pass silently, but an
// unfinished report-only tier, or a tier stopped early on purpose, can.
func TestBuildSection_IncompleteGatingTierWarns(t *testing.T) {
	section := func(operator string, o lang.MutantOutcome) report.Section {
		mutants := numbered(2, operator)
		mutants[0].setResult(lang.MutantResult{Outcome: lang.OutcomeKilled})
		mutants[1].setResult(lang.MutantResult{Outcome: o})
		return buildSection(mutants, Options{})
	}
	if s := section("negate_conditional", lang.OutcomeNotRun); s.Severity != report.SeverityWarn || !strings.Contains(s.Summary, "1 NOT RUN") {
		t.Errorf("tier 1 not run: %v %q", s.Severity, s.Summary)
	}
	if s := section("boolean_substitution", lang.OutcomeNotRun); s.Severity != report.SeverityWarn {
		t.Errorf("tier 2 not run: severity = %v, want WARN", s.Severity)
	}
	if s := section("statement_deletion", lang.OutcomeNotRun); s.Severity != report.SeverityPass {
		t.Errorf("tier 3 not run: severity = %v, want PASS", s.Severity)
	}
	if s := section("negate_conditional", lang.OutcomeStoppedEarly); s.Severity != report.SeverityPass || !strings.Contains(s.Summary, "1 skipped (verdict settled early)") {
		t.Errorf("stopped early: %v %q", s.Severity, s.Summary)
	}
}

// Mutants whose tests fail without any mutation were never judged. A gating
// tier holding any of them fails, and says which file they are in.
func TestBuildSection_BaselineFailedFailsTheGate(t *testing.T) {
	section := func(operator string) report.Section {
		mutants := numbered(3, operator)
		mutants[0].setResult(lang.MutantResult{Outcome: lang.OutcomeKilled})
		mutants[1].setResult(lang.MutantResult{Outcome: lang.OutcomeBaselineFailed})
		mutants[2].setResult(lang.MutantResult{Outcome: lang.OutcomeBaselineFailed})
		return buildSection(mutants, Options{})
	}
	for _, operator := range []string{"negate_conditional", "boolean_substitution"} {
		s := section(operator)
		if s.Severity != report.SeverityFail || !strings.Contains(s.Summary, "2 NOT TESTED") {
			t.Errorf("%s: %v %q", operator, s.Severity, s.Summary)
		}
		if len(s.Findings) != 1 || !strings.Contains(s.Findings[0].Message, "NOT TESTED: 2 mutant(s)") || s.Findings[0].File != "a.go" {
			t.Errorf("%s: findings = %+v", operator, s.Findings)
		}
		if total := s.Stats.(map[string]any)["total"]; total != 1 {
			t.Errorf("%s: untested mutants must stay out of the score, total = %v", operator, total)
		}
	}
	if s := section("statement_deletion"); s.Severity != report.SeverityPass {
		t.Errorf("tier 3 is report-only: severity = %v, want PASS", s.Severity)
	}
}

func TestTierSeverity_FailBeatsIncomplete(t *testing.T) {
	ts := TierStats{Tier: TierLogic, Total: 10, Killed: 1, Score: 10, NotRun: 5}
	if got := tierSeverity(ts, Options{}); got != report.SeverityFail {
		t.Errorf("severity = %v, want FAIL", got)
	}
	empty := TierStats{Tier: TierLogic, NotRun: 3}
	if got := tierSeverity(empty, Options{}); got != report.SeverityWarn {
		t.Errorf("nothing scored, some not run: severity = %v, want WARN", got)
	}
}

func TestMutantOutcomeDerivedFromKilled(t *testing.T) {
	if (Mutant{Killed: true}).outcome() != lang.OutcomeKilled || (Mutant{}).outcome() != lang.OutcomeSurvived {
		t.Error("a mutant without an explicit outcome derives it from Killed")
	}
	if (Mutant{Killed: true, Outcome: lang.OutcomeBuildFailed}).outcome() != lang.OutcomeBuildFailed {
		t.Error("an explicit outcome wins")
	}
}

func TestBuildSection_NothingScored(t *testing.T) {
	mutants := numbered(1, "negate_conditional")
	mutants[0].setResult(lang.MutantResult{Outcome: lang.OutcomeBuildFailed})
	s := buildSection(mutants, Options{})
	if score := s.Stats.(map[string]any)["score"]; score != 0.0 {
		t.Errorf("score = %v, want 0 when no mutant was scored", score)
	}
	if s.Severity != report.SeverityPass {
		t.Errorf("severity = %v, want PASS", s.Severity)
	}
}

func TestTierSeverity_PassValues(t *testing.T) {
	for _, ts := range []TierStats{
		{Tier: TierObservability, Total: 10, Score: 0, NotRun: 4},
		{Tier: TierLogic, Total: 10, Killed: 10, Score: 100},
		{Tier: TierSemantic},
	} {
		if got := tierSeverity(ts, Options{}); got != report.SeverityPass {
			t.Errorf("tierSeverity(%+v) = %q, want PASS", ts, got)
		}
	}
	if got := tierSeverity(TierStats{Tier: TierSemantic, Total: 10, Score: 10}, Options{}); got != report.SeverityWarn {
		t.Errorf("tier 2 below threshold = %q, want WARN", got)
	}
}

func TestGatingThreshold(t *testing.T) {
	opts := Options{Tier1Threshold: 81, Tier2Threshold: 62}
	if th, ok := gatingThreshold(TierLogic, opts); th != 81 || !ok {
		t.Errorf("tier 1: %v %v", th, ok)
	}
	if th, ok := gatingThreshold(TierSemantic, opts); th != 62 || !ok {
		t.Errorf("tier 2: %v %v", th, ok)
	}
	if _, ok := gatingThreshold(TierObservability, opts); ok {
		t.Error("tier 3 is not gated")
	}
}

// analyzableLanguage adds a generator and annotation scanner to
// sessionLanguage so Analyze can run end to end.
type analyzableLanguage struct {
	sessionLanguage
	sites []lang.MutantSite
}

func (l *analyzableLanguage) MutantGenerator() lang.MutantGenerator     { return l }
func (l *analyzableLanguage) AnnotationScanner() lang.AnnotationScanner { return l }

func (l *analyzableLanguage) ScanAnnotations(string) (map[int]bool, error) { return nil, nil }

func (l *analyzableLanguage) GenerateMutants(string, diff.FileChange, map[int]bool) ([]lang.MutantSite, error) {
	return l.sites, nil
}

func TestAnalyze(t *testing.T) {
	d := &diff.Result{Files: []diff.FileChange{{Path: "a.go"}}}
	l := &analyzableLanguage{sites: []lang.MutantSite{
		{File: "a.go", Line: 1, Offset: 5, Operator: "negate_conditional", Description: "== -> !="},
		{File: "a.go", Line: 2, Offset: 9, Operator: "negate_conditional", Description: "== -> !="},
		{File: "a.go", Line: 2, Offset: 9, Operator: "negate_conditional", Description: "== -> !="},
	}}
	l.fingerprint = "v1"
	l.outcome = byLine(lang.OutcomeKilled, lang.OutcomeSurvived)
	cacheDir := t.TempDir()

	s, err := Analyze("/repo", d, l, Options{SampleRate: 100, CacheDir: cacheDir})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s.Summary, "Score: 50.0% (1/2 killed, 1 survived)") || s.Severity != report.SeverityFail {
		t.Errorf("first run: %q [%s]; want the duplicate site dropped and 1 of 2 killed", s.Summary, s.Severity)
	}
	if l.gotCfg.WorkDir == "" {
		t.Error("the session needs a work directory")
	}
	if _, err := os.Stat(l.gotCfg.WorkDir); !os.IsNotExist(err) {
		t.Error("the work directory must be removed after the run")
	}

	again, err := Analyze("/repo", d, l, Options{SampleRate: 100, CacheDir: cacheDir})
	if err != nil {
		t.Fatal(err)
	}
	if l.runs.Load() != 2 || !strings.Contains(again.Summary, "2 from cache") {
		t.Errorf("second run should come from the cache: runs = %d, summary %q", l.runs.Load(), again.Summary)
	}

	l.startErr = errors.New("boom")
	if _, err := Analyze("/repo", d, l, Options{}); err == nil {
		t.Error("a session that cannot start is an error")
	}

	none, err := Analyze("/repo", d, &analyzableLanguage{}, Options{})
	if err != nil || none.Summary != "No mutants generated from changed code" || none.Severity != report.SeverityPass {
		t.Errorf("no mutants: %+v, %v", none, err)
	}
}
