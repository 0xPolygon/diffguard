package tsanalyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/0xPolygon/diffguard/internal/lang"
)

// testRunnerImpl implements lang.TestRunner for TypeScript using the
// project's configured test runner (vitest, jest, or `npm test`). Same
// temp-copy isolation as the Rust analyzer:
//
//  1. Per-file mutex so concurrent mutants on the same file serialize.
//  2. Back up original bytes, swap mutant in place, run tests, restore.
//  3. Timeout via context.WithTimeout.
//
// Test command selection is driven by package.json devDependencies. If
// neither vitest nor jest appears, we fall back to `npm test`.
type testRunnerImpl struct {
	// cmd / extraArgs override hooks used by tests. Normal production runs
	// leave them empty and buildCommand derives argv from package.json.
	cmd       string
	extraArgs []string

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// newTestRunner builds a fresh runner. Fields are filled at Run time from
// the repo under test; tests construct their own via the fakeRunner helper
// in testrunner_test.go.
func newTestRunner() *testRunnerImpl {
	return &testRunnerImpl{}
}

// fileLock returns the per-file mutex for the given path, lazily creating
// the entry on first access.
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
// nil) signals killed; (false, ..., nil) signals survived; (false, "", err)
// signals a runner failure.
func (r *testRunnerImpl) RunTest(cfg lang.TestRunConfig) (bool, string, error) {
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

	// Defer restore BEFORE writing so a panic between write and run can't
	// leave corrupt source behind.
	restore := func() {
		_ = os.WriteFile(cfg.OriginalFile, originalBytes, 0644)
	}
	defer restore()

	if err := os.WriteFile(cfg.OriginalFile, mutantBytes, 0644); err != nil {
		return false, "", fmt.Errorf("writing mutant over original: %w", err)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTSTestTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmdName, args := r.buildCommand(cfg)
	cmd := exec.CommandContext(ctx, cmdName, args...)
	cmd.Dir = cfg.RepoPath
	// CI=true suppresses interactive prompts from jest/vitest.
	cmd.Env = append(os.Environ(), "CI=true")
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined

	runErr := cmd.Run()
	output := combined.String()

	if ctx.Err() == context.DeadlineExceeded {
		return true, output, nil
	}
	if runErr != nil {
		return true, output, nil
	}
	return false, output, nil
}

// buildCommand returns the argv to execute for this RunTest call.
//
// Precedence:
//
//  1. If the runner has a hard-coded cmd/extraArgs (tests), use them.
//  2. Detect the configured runner by reading package.json:
//     vitest > jest > `npm test`.
//  3. Honor TestPattern by appending the runner's pattern flag.
func (r *testRunnerImpl) buildCommand(cfg lang.TestRunConfig) (string, []string) {
	if r.cmd != "" {
		return r.cmd, append([]string(nil), r.extraArgs...)
	}
	runner := detectTSRunner(cfg.RepoPath)
	switch runner {
	case "vitest":
		args := []string{"vitest", "run"}
		if cfg.TestPattern != "" {
			args = append(args, "-t", cfg.TestPattern)
		}
		return "npx", args
	case "jest":
		args := []string{"jest"}
		if cfg.TestPattern != "" {
			args = append(args, "--testNamePattern", cfg.TestPattern)
		}
		return "npx", args
	}
	// Fall back: plain `npm test`. Pattern handling isn't portable here,
	// so we just skip it and hope the suite is fast enough.
	return "npm", []string{"test"}
}

// detectTSRunner reads package.json's devDependencies / dependencies and
// returns "vitest", "jest", or "" for fall-back to npm test. The choice
// prefers vitest over jest per the design doc.
func detectTSRunner(repoPath string) string {
	data, err := os.ReadFile(filepath.Join(repoPath, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		DevDependencies map[string]string `json:"devDependencies"`
		Dependencies    map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return ""
	}
	has := func(name string) bool {
		if _, ok := pkg.DevDependencies[name]; ok {
			return true
		}
		_, ok := pkg.Dependencies[name]
		return ok
	}
	if has("vitest") {
		return "vitest"
	}
	if has("jest") || has("@jest/core") {
		return "jest"
	}
	return ""
}

// tsTestArgs is exposed to tests so they can assert the argv shape that
// would be sent to the detected runner when no overrides are in play.
func tsTestArgs(repoPath string, cfg lang.TestRunConfig) (string, []string) {
	r := &testRunnerImpl{}
	cfg.RepoPath = repoPath
	return r.buildCommand(cfg)
}
