package rustanalyzer

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/0xPolygon/diffguard/internal/lang"
)

// testRunnerImpl implements lang.TestRunner for Rust using `cargo test`.
// Unlike Go's overlay-based runner, Cargo has no build-time file
// substitution, so we use a temp-copy isolation strategy:
//
//  1. Acquire a per-file mutex so concurrent mutants on the same file
//     serialize. Different files run in parallel.
//  2. Back the original up.
//  3. Copy the mutant bytes over the original in place.
//  4. Run `cargo test` with a timeout.
//  5. Restore the original from the backup — always, via defer — even
//     if cargo panics or we panic.
type testRunnerImpl struct {
	// cmd is the executable to run. Normally "cargo"; tests override this
	// with a fake binary that exercises the kill / survive / timeout paths
	// without needing a real Cargo toolchain.
	cmd string
	// extraArgs are prepended before the normal cargo test args. Tests use
	// this to swap in a no-op command ("sh -c 'exit 0'") by setting
	// cmd="sh" and extraArgs=["-c","..."].
	extraArgs []string

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// newTestRunner builds a fresh runner. All fields are zero-value except
// the cmd which defaults to "cargo". Tests construct their own via
// newTestRunnerWithCommand.
func newTestRunner() *testRunnerImpl {
	return &testRunnerImpl{cmd: "cargo"}
}

// fileLock returns the per-file mutex for the given path, lazily
// initializing the entry on first access. The outer lock (r.mu) guards
// only the map; the returned mutex is what the caller actually holds
// while mutating the source file.
func (r *testRunnerImpl) fileLock(path string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.locks == nil {
		r.locks = map[string]*sync.Mutex{}
	}
	m, ok := r.locks[path]
	if !ok {
		m = &sync.Mutex{}
		r.locks[path] = m
	}
	return m
}

// RunTest implements the lang.TestRunner contract. Returning (true, ...,
// nil) signals the mutant was killed (test exit != 0); (false, ..., nil)
// signals survived (tests passed); (false, "", err) signals the runner
// itself couldn't run.
func (r *testRunnerImpl) RunTest(cfg lang.TestRunConfig) (bool, string, error) {
	// Per-file serialization: two concurrent mutants on the same file
	// would race on the in-place swap below.
	lock := r.fileLock(cfg.OriginalFile)
	lock.Lock()
	defer lock.Unlock()

	restore, err := swapInMutant(cfg)
	if err != nil {
		return false, "", err
	}
	defer restore()

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultRustTestTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	output, runErr := r.runCargoTest(ctx, cfg)
	// A timeout or a non-zero exit both count as killed — the mutant
	// either broke tests or made them so slow they couldn't finish.
	killed := ctx.Err() == context.DeadlineExceeded || runErr != nil
	return killed, output, nil
}

// swapInMutant reads the mutant and original, writes the mutant over
// the original, and returns a restore closure that puts the original
// back. Deferring the restore before any test run ensures a panic
// mid-test still leaves a clean working copy.
func swapInMutant(cfg lang.TestRunConfig) (func(), error) {
	mutantBytes, err := os.ReadFile(cfg.MutantFile)
	if err != nil {
		return nil, fmt.Errorf("reading mutant file: %w", err)
	}
	originalBytes, err := os.ReadFile(cfg.OriginalFile)
	if err != nil {
		return nil, fmt.Errorf("reading original file: %w", err)
	}
	if err := os.WriteFile(cfg.OriginalFile, mutantBytes, 0644); err != nil {
		return nil, fmt.Errorf("writing mutant over original: %w", err)
	}
	return func() { _ = os.WriteFile(cfg.OriginalFile, originalBytes, 0644) }, nil
}

// runCargoTest spawns `cargo test` under the caller's context, returns
// combined stdout+stderr, and the run error. A non-nil error may be a
// cargo failure or context cancellation; callers disambiguate via
// ctx.Err().
func (r *testRunnerImpl) runCargoTest(ctx context.Context, cfg lang.TestRunConfig) (string, error) {
	cmd := exec.CommandContext(ctx, r.cmd, r.buildArgs(cfg)...)
	configureProcessKill(cmd)
	cmd.Dir = cfg.RepoPath
	cmd.Env = append(os.Environ(), "CARGO_INCREMENTAL=0")
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	err := cmd.Run()
	return combined.String(), err
}

// buildArgs returns the argv after the command name. When the caller
// supplied extraArgs (tests), we honor those; otherwise we build a normal
// `cargo test` invocation with the pattern as a positional filter.
func (r *testRunnerImpl) buildArgs(cfg lang.TestRunConfig) []string {
	if len(r.extraArgs) > 0 {
		return append([]string(nil), r.extraArgs...)
	}
	args := []string{"test"}
	if cfg.TestPattern != "" {
		args = append(args, cfg.TestPattern)
	}
	return args
}

// cargoTestArgs is exposed to tests so they can assert the argv we'd send
// to cargo when no overrides are in play.
func cargoTestArgs(cfg lang.TestRunConfig) []string {
	r := &testRunnerImpl{}
	return r.buildArgs(cfg)
}

