package rustanalyzer

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	mutantBytes, err := os.ReadFile(cfg.MutantFile)
	if err != nil {
		return false, "", fmt.Errorf("reading mutant file: %w", err)
	}
	originalBytes, err := os.ReadFile(cfg.OriginalFile)
	if err != nil {
		return false, "", fmt.Errorf("reading original file: %w", err)
	}

	// Defer restore BEFORE writing the mutant so a panic between the
	// write and the test run can't leave a corrupt source file behind.
	restore := func() {
		// Best-effort restore; we don't have a sane way to report an
		// error here and the harness is expected to panic-safely run.
		_ = os.WriteFile(cfg.OriginalFile, originalBytes, 0644)
	}
	defer restore()

	if err := os.WriteFile(cfg.OriginalFile, mutantBytes, 0644); err != nil {
		return false, "", fmt.Errorf("writing mutant over original: %w", err)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultRustTestTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	args := r.buildArgs(cfg)
	cmd := exec.CommandContext(ctx, r.cmd, args...)
	cmd.Dir = cfg.RepoPath
	cmd.Env = append(os.Environ(), "CARGO_INCREMENTAL=0")
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined

	runErr := cmd.Run()
	output := combined.String()

	// A timeout is reported as "killed" — the mutant made tests so slow
	// they couldn't finish within the allotted window, which is a
	// meaningful signal in line with the Go analyzer's treatment.
	if ctx.Err() == context.DeadlineExceeded {
		return true, output, nil
	}
	if runErr != nil {
		return true, output, nil
	}
	return false, output, nil
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

// backupAndRestore is exposed for tests that want to verify the
// restore-on-panic guarantee without actually invoking cargo.
//
// It writes `mutantBytes` over `path`, runs `work`, and restores
// `originalBytes` via defer. Returns the original unmodified bytes so the
// caller can assert restoration.
//
//nolint:unused // used by testrunner_test.go
func backupAndRestore(path string, originalBytes, mutantBytes []byte, work func()) (restored []byte, err error) {
	defer func() {
		_ = os.WriteFile(path, originalBytes, 0644)
		restored, err = os.ReadFile(path)
	}()
	if err := os.WriteFile(path, mutantBytes, 0644); err != nil {
		return nil, err
	}
	work()
	return nil, nil
}

// AtomicCopy copies src to dst; used to build a file-level "backup"
// location if a caller prefers backing up to a sibling path rather than
// holding bytes in memory. We don't use this from RunTest (in-memory is
// cheap for source files) but leave it here for future runners that may
// need on-disk backups.
//
//nolint:unused
func AtomicCopy(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(dst), ".diffguard-backup-tmp")
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
