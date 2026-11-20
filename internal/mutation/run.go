package mutation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/0xPolygon/diffguard/internal/lang"
)

// progressInterval is how often a status line is written while mutants run.
var progressInterval = 15000 * time.Millisecond

// executor tests the pos-th pending mutant of a run.
type executor interface {
	run(ctx context.Context, pos int) lang.MutantResult
	close()
}

// runMutantsParallel tests every mutant that has no outcome yet (cached
// results are left alone), capped at opts.workers() at a time. Mutants the
// budget or a cancellation never reached are marked not run rather than
// dropped.
func runMutantsParallel(repoPath string, mutants []Mutant, l lang.Language, opts Options, workDir string) error {
	ctx, cancel := opts.runContext()
	defer cancel()

	pending := pendingIndexes(mutants)
	exec, err := newExecutor(ctx, repoPath, mutants, pending, l, opts, workDir)
	if err != nil {
		return err
	}
	defer exec.close()

	run := &mutantRun{ctx: ctx, mutants: mutants, exec: exec, stop: newEarlyStop(opts), start: time.Now()}
	stopProgress := run.reportProgress(opts, len(pending))
	run.dispatch(pending, opts.workers())
	stopProgress()
	return nil
}

func pendingIndexes(mutants []Mutant) []int {
	var pending []int
	for i := range mutants {
		if mutants[i].Outcome == lang.OutcomeUnset {
			pending = append(pending, i)
		}
	}
	return pending
}

// mutantRun is the shared state of one scheduling pass.
type mutantRun struct {
	ctx     context.Context
	mutants []Mutant
	exec    executor
	stop    *earlyStop
	start   time.Time

	// done and killed feed the progress line.
	done   atomic.Int64
	killed atomic.Int64
}

func (r *mutantRun) dispatch(pending []int, workers int) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for pos, idx := range pending {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			r.runOne(pos, idx)
		}()
	}
	wg.Wait()
}

func (r *mutantRun) runOne(pos, idx int) {
	m := &r.mutants[idx]
	tier := operatorTier(m.Operator)
	res := lang.MutantResult{Outcome: r.skipReason(tier)}
	if res.Outcome == lang.OutcomeUnset {
		res = r.exec.run(r.ctx, pos)
	}
	m.setResult(res)
	r.stop.observe(tier, res.Outcome)

	r.done.Add(1)
	if m.Killed {
		r.killed.Add(1)
	}
}

// skipReason returns the outcome to record without testing the mutant, or
// OutcomeUnset if it should be tested.
func (r *mutantRun) skipReason(tier Tier) lang.MutantOutcome {
	if r.ctx.Err() != nil {
		return lang.OutcomeNotRun
	}
	if r.stop.isSettled(tier) {
		return lang.OutcomeStoppedEarly
	}
	// mutator-disable-next-line (OutcomeUnset is the zero value: zeroing the return changes nothing)
	return lang.OutcomeUnset
}

// reportProgress writes a status line every progressInterval until the
// returned func is called, which also writes the final line.
func (r *mutantRun) reportProgress(opts Options, total int) func() {
	if opts.Progress == nil {
		return func() {}
	}
	line := func() {
		fmt.Fprintf(opts.Progress, "mutation: %d/%d mutants done, %d killed, %s elapsed\n",
			r.done.Load(), total, r.killed.Load(), time.Since(r.start).Round(time.Second))
	}
	quit := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(progressInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				line()
			case <-quit:
				return
			}
		}
	}()
	return func() {
		close(quit)
		<-finished
		line()
	}
}

func newExecutor(ctx context.Context, repoPath string, mutants []Mutant, pending []int, l lang.Language, opts Options, workDir string) (executor, error) {
	sr, ok := l.(lang.SessionRunner)
	if !ok {
		return newLegacyExecutor(repoPath, mutants, pending, l, opts, workDir), nil
	}
	sites := make([]lang.MutantSite, len(pending))
	for pos, idx := range pending {
		sites[pos] = mutants[idx].site()
	}
	session, err := sr.NewMutationSession(ctx, lang.SessionConfig{
		RepoPath:    repoPath,
		WorkDir:     workDir,
		Timeout:     opts.timeout(),
		TestPattern: opts.TestPattern,
		Workers:     opts.workers(),
		Log:         opts.Progress,
	}, sites)
	if err != nil {
		return nil, fmt.Errorf("starting mutation session: %w", err)
	}
	return sessionExecutor{session}, nil
}

// sessionExecutor adapts a language session, whose sites are the pending
// mutants in order.
type sessionExecutor struct{ session lang.MutationSession }

func (e sessionExecutor) run(ctx context.Context, pos int) lang.MutantResult {
	return e.session.Run(ctx, pos)
}

func (e sessionExecutor) close() { e.session.Close() }

// legacyExecutor drives languages without a SessionRunner in two phases:
//
//  1. Prepare: ApplyMutation for every mutant and write the mutated bytes
//     to a temp file. Runs before any RunTest so ApplyMutation always sees
//     pristine source on disk — critical for temp-copy runners (Rust, TS)
//     that swap the mutant over the real file during RunTest. If we
//     interleaved apply + test, a concurrent worker's ApplyMutation could
//     read a file that another worker's RunTest had temporarily mutated,
//     produce the wrong mutant (or fail to locate its target), and get
//     silently reported as SURVIVED.
//  2. Test: RunTest for each prepared mutant. The TestRunner is responsible
//     for per-file isolation (non-Go runners serialize same-file mutants
//     via a mutex).
type legacyExecutor struct {
	repoPath string
	pending  []Mutant
	prepared []string // mutant file per pending mutant; "" = not applied
	runner   lang.TestRunner
	opts     Options
	workDir  string
}

func newLegacyExecutor(repoPath string, mutants []Mutant, pending []int, l lang.Language, opts Options, workDir string) *legacyExecutor {
	e := &legacyExecutor{repoPath: repoPath, runner: l.TestRunner(), opts: opts, workDir: workDir}
	for _, idx := range pending {
		e.pending = append(e.pending, mutants[idx])
	}
	e.prepared = prepareMutants(repoPath, e.pending, l.MutantApplier(), workDir, opts.workers())
	return e
}

func (e *legacyExecutor) close() {}

func (e *legacyExecutor) run(_ context.Context, pos int) lang.MutantResult {
	if e.prepared[pos] == "" {
		return lang.MutantResult{Outcome: lang.OutcomeNotApplied}
	}
	killed, output, err := e.runner.RunTest(lang.TestRunConfig{
		RepoPath:     e.repoPath,
		MutantFile:   e.prepared[pos],
		OriginalFile: filepath.Join(e.repoPath, e.pending[pos].File),
		Timeout:      e.opts.timeout(),
		TestPattern:  e.opts.TestPattern,
		WorkDir:      e.workDir,
		Index:        pos,
	})
	switch {
	case err != nil:
		return lang.MutantResult{Outcome: lang.OutcomeNotRun}
	case killed:
		return lang.MutantResult{Outcome: lang.OutcomeKilled, Output: output}
	}
	return lang.MutantResult{Outcome: lang.OutcomeSurvived}
}

// prepareMutants applies every mutation to pristine source and stores the
// mutated bytes in per-mutant temp files. Returns a parallel slice of
// mutantFile paths; an empty string marks a skipped mutant (the applier
// declined to produce bytes — parse error, no target, equivalence, …).
//
// Apply is parallelized across mutants because no RunTest has fired yet,
// so the source files on disk are still pristine and safe for concurrent
// reads.
func prepareMutants(repoPath string, mutants []Mutant, applier lang.MutantApplier, workDir string, workers int) []string {
	prepared := make([]string, len(mutants))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for i := range mutants {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			prepared[idx] = prepareMutant(repoPath, mutants[idx], applier, workDir, idx)
		}(i)
	}
	wg.Wait()
	return prepared
}

// prepareMutant runs ApplyMutation and writes the resulting bytes to a
// mutant-specific temp file. Returns the temp file path on success, or
// "" if the mutant was skipped (apply error / nil bytes / write error).
func prepareMutant(repoPath string, m Mutant, applier lang.MutantApplier, workDir string, idx int) string {
	absPath := filepath.Join(repoPath, m.File)

	mutated, err := applier.ApplyMutation(absPath, m.site())
	if err != nil || mutated == nil {
		return ""
	}

	mutantFile := filepath.Join(workDir, fmt.Sprintf("m%d%s", idx, filepath.Ext(absPath)))
	if err := os.WriteFile(mutantFile, mutated, 0644); err != nil {
		return ""
	}
	return mutantFile
}
