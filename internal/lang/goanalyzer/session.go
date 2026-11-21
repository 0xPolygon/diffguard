package goanalyzer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/0xPolygon/diffguard/internal/lang"
)

// slowTestThreshold splits the tests that reach a mutant into a fast group,
// run first, and a slow group run only if the fast one did not kill it.
const slowTestThreshold = time.Second

// goSession tests the mutants of one run. Each package is prepared once, in
// the background: its mutants are compiled into a single test binary per
// test package, and one traced run of every test records which mutants that
// test reaches. A mutant is then tested by running only those tests.
//
// A mutant that cannot be compiled into the shared binary, or a package
// whose shared build or baseline fails, is tested the slow way: one
// `go test -overlay` per mutant.
type goSession struct {
	cfg    lang.SessionConfig
	sites  []lang.MutantSite
	pkgOf  []*mutantPackage
	pkgs   []*mutantPackage
	slots  chan struct{}
	cancel context.CancelFunc
}

// NewMutationSession implements lang.SessionRunner.
func (*Language) NewMutationSession(ctx context.Context, cfg lang.SessionConfig, sites []lang.MutantSite) (lang.MutationSession, error) {
	ctx, cancel := context.WithCancel(ctx)
	s := &goSession{
		cfg:    cfg,
		sites:  sites,
		pkgOf:  make([]*mutantPackage, len(sites)),
		slots:  make(chan struct{}, max(cfg.Workers, 1)),
		cancel: cancel,
	}
	byDir := map[string]*mutantPackage{}
	for i, site := range sites {
		dir := filepath.Join(cfg.RepoPath, filepath.Dir(site.File))
		p := byDir[dir]
		if p == nil {
			p = &mutantPackage{session: s, dir: dir, work: filepath.Join(cfg.WorkDir, "pkg"+strconv.Itoa(len(s.pkgs))), ready: make(chan struct{})}
			byDir[dir] = p
			s.pkgs = append(s.pkgs, p)
		}
		p.sites = append(p.sites, i)
		s.pkgOf[i] = p
	}
	for _, p := range s.pkgs {
		go func() {
			defer close(p.ready)
			p.prepare(ctx)
		}()
	}
	return s, nil
}

// Run implements lang.MutationSession.
func (s *goSession) Run(ctx context.Context, idx int) lang.MutantResult {
	p := s.pkgOf[idx]
	select {
	case <-p.ready:
	case <-ctx.Done():
	}
	if ctx.Err() != nil {
		return lang.MutantResult{Outcome: lang.OutcomeNotRun}
	}
	if !p.embedded[idx] {
		return p.runOverlay(ctx, idx)
	}
	return p.runSchemata(ctx, idx+1)
}

// Close stops any preparation still running and waits for it to exit, so
// the work directory can be removed.
func (s *goSession) Close() {
	s.cancel()
	for _, p := range s.pkgs {
		<-p.ready
	}
}

func (s *goSession) logf(format string, args ...any) {
	if s.cfg.Log != nil {
		fmt.Fprintf(s.cfg.Log, "mutation: "+format+"\n", args...)
	}
}

// acquire takes a slot bounding the child processes preparation runs at
// once; the returned func releases it.
func (s *goSession) acquire() func() {
	s.slots <- struct{}{}
	return func() { <-s.slots }
}

// mutantPackage is the per-package state of a session.
type mutantPackage struct {
	session *goSession
	dir     string
	work    string
	sites   []int
	ready   chan struct{}

	// embedded marks the sites compiled into bins. Empty when the shared
	// build is unavailable, which routes every site to the overlay path.
	embedded map[int]bool
	bins     []*testBinary

	baselineOnce sync.Once
	baselineOK   bool
	baselineTook time.Duration
}

// runSchemata tests mutant id against every test that reaches it.
func (p *mutantPackage) runSchemata(ctx context.Context, id int) lang.MutantResult {
	covered := false
	for _, b := range p.bins {
		for _, g := range b.groupsReaching(id) {
			covered = true
			if res := b.runGroup(ctx, g, id, p.session.cfg); res.Outcome != lang.OutcomeSurvived {
				return res
			}
		}
	}
	if !covered {
		return lang.MutantResult{Outcome: lang.OutcomeNotCovered}
	}
	return lang.MutantResult{Outcome: lang.OutcomeSurvived}
}

// runOverlay tests one mutant with its own build: the mutated file replaces
// the original through a build overlay.
func (p *mutantPackage) runOverlay(ctx context.Context, idx int) lang.MutantResult {
	p.baselineOnce.Do(func() { p.overlayBaseline(ctx) })
	if !p.baselineOK {
		return lang.MutantResult{Outcome: lang.OutcomeNotRun}
	}
	cfg := p.session.cfg
	site := p.session.sites[idx]
	original := filepath.Join(cfg.RepoPath, site.File)
	mutated, _ := mutantApplierImpl{}.ApplyMutation(original, site)
	if mutated == nil {
		return lang.MutantResult{Outcome: lang.OutcomeNotApplied}
	}
	mutantFile := filepath.Join(cfg.WorkDir, fmt.Sprintf("m%d.go", idx))
	overlay := filepath.Join(cfg.WorkDir, fmt.Sprintf("m%d-overlay.json", idx))
	if os.WriteFile(mutantFile, mutated, 0o644) != nil || writeOverlayJSON(overlay, original, mutantFile) != nil {
		return lang.MutantResult{Outcome: lang.OutcomeNotApplied}
	}

	build := p.goCmd(ctx, time.Minute+cfg.Timeout, "build", "-overlay="+overlay, "-o", os.DevNull, ".")
	if build.err != nil {
		return classify(ctx, build, lang.OutcomeBuildFailed)
	}
	// The process deadline is the one timeout: go test's own is set past it
	// so that a hang is reported as a timeout, not as a failing test.
	timeout := overlayTimeout(p.baselineTook, cfg.Timeout)
	args := buildTestArgs(lang.TestRunConfig{Timeout: timeout + time.Minute, TestPattern: cfg.TestPattern}, overlay)
	return classify(ctx, p.goCmd(ctx, timeout, args...), lang.OutcomeKilled)
}

// overlayBaseline runs the package's unmutated tests once. If they fail,
// every result would read as a kill, so the package's mutants are not run.
func (p *mutantPackage) overlayBaseline(ctx context.Context) {
	args := []string{"test", "-count=1"}
	if pattern := p.session.cfg.TestPattern; pattern != "" {
		args = append(args, "-run", pattern)
	}
	res := p.goCmd(ctx, baselineTimeout(p.session.cfg), append(args, "./...")...)
	p.baselineOK = res.err == nil
	p.baselineTook = res.duration
	if !p.baselineOK && ctx.Err() == nil {
		p.session.logf("tests fail without any mutation in %s; its mutants were not run\n%s", p.dir, res.output)
	}
}

func (p *mutantPackage) goCmd(ctx context.Context, timeout time.Duration, args ...string) procResult {
	return proc{dir: p.dir, timeout: timeout, name: "go", args: args, keep: 1 << 20}.run(ctx)
}

// classify maps a finished child process to an outcome; onFailure is the
// outcome of a non-zero exit.
func classify(ctx context.Context, res procResult, onFailure lang.MutantOutcome) lang.MutantResult {
	switch {
	case ctx.Err() != nil:
		return lang.MutantResult{Outcome: lang.OutcomeNotRun}
	case res.timedOut:
		return lang.MutantResult{Outcome: lang.OutcomeTimedOut, Output: tail([]byte(res.output), 0)}
	case res.err != nil:
		return lang.MutantResult{Outcome: onFailure, Output: tail([]byte(res.output), 0)}
	}
	return lang.MutantResult{Outcome: lang.OutcomeSurvived}
}

// overlayTimeout bounds one mutant's `go test` on the overlay path, whose
// unmutated run took baseline: the configured ceiling, raised if a healthy
// run could not finish within it.
func overlayTimeout(baseline, ceiling time.Duration) time.Duration {
	return max(ceiling, 3*baseline+30*time.Second)
}

// baselineTimeout bounds an unmutated test run, which has no earlier
// measurement to derive a tighter limit from.
func baselineTimeout(cfg lang.SessionConfig) time.Duration {
	return max(cfg.Timeout, 10*time.Minute)
}

// mutantTimeout bounds a mutant's run of tests that took baseline without
// the mutation. A mutant that loops forever should cost a small multiple of
// the real run, not the configured ceiling; but the ceiling must never be
// so low that a healthy run cannot finish.
func mutantTimeout(baseline, ceiling time.Duration) time.Duration {
	limit := 4*baseline + 5*time.Second
	return min(limit, max(ceiling, 2*baseline+5*time.Second))
}

// testBinary is one compiled test package with the mutants built in.
type testBinary struct {
	path string
	dir  string
	// tests holds one traced run per test when every test passes on its
	// own; otherwise whole holds a single traced run of the full suite.
	tests []tracedTest
	whole *tracedTest
}

// tracedTest is an unmutated test run and the mutants it reached.
type tracedTest struct {
	name     string
	duration time.Duration
	reach    map[int]bool
}

// testGroup is a set of tests to run against one mutant. A nil names slice
// means the whole suite.
type testGroup struct {
	names    []string
	baseline time.Duration
}

// groupsReaching returns the test groups that execute mutant id, cheapest
// first.
func (b *testBinary) groupsReaching(id int) []testGroup {
	if b.whole != nil {
		if !b.whole.reach[id] {
			return nil
		}
		return []testGroup{{baseline: b.whole.duration}}
	}
	fast, slow := b.testsReaching(id)
	var groups []testGroup
	for _, g := range []testGroup{fast, slow} {
		if g.names != nil {
			groups = append(groups, g)
		}
	}
	return groups
}

// testsReaching splits the tests that execute mutant id into those faster
// than slowTestThreshold and the rest.
func (b *testBinary) testsReaching(id int) (fast, slow testGroup) {
	for _, t := range b.tests {
		switch {
		case !t.reach[id]:
		case t.duration < slowTestThreshold:
			fast.add(t)
		default:
			slow.add(t)
		}
	}
	return fast, slow
}

func (g *testGroup) add(t tracedTest) {
	g.names = append(g.names, t.name)
	g.baseline += t.duration
}

func (b *testBinary) runGroup(ctx context.Context, g testGroup, id int, cfg lang.SessionConfig) lang.MutantResult {
	args := append([]string{"-test.count=1", "-test.failfast"}, runFilter(g.names, cfg.TestPattern)...)
	res := proc{
		dir:     b.dir,
		env:     []string{mutantEnv + "=" + strconv.Itoa(id)},
		timeout: mutantTimeout(g.baseline, cfg.Timeout),
		name:    b.path,
		args:    args,
	}.run(ctx)
	return classify(ctx, res, lang.OutcomeKilled)
}

// runFilter returns the -test.run flag selecting names, or the user's
// pattern when names is nil.
func runFilter(names []string, pattern string) []string {
	if names == nil {
		if pattern == "" {
			return nil
		}
		return []string{"-test.run", pattern}
	}
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = regexp.QuoteMeta(n)
	}
	return []string{"-test.run", "^(" + strings.Join(quoted, "|") + ")$"}
}
