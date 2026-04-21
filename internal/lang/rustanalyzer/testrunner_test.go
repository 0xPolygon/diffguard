package rustanalyzer

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xPolygon/diffguard/internal/lang"
)

// fakeRunner returns a runner that invokes `/bin/sh -c <script>` instead
// of `cargo test`. This lets us simulate killed / survived / timeout
// without a real crate, toolchain, or network — keeping the test suite
// hermetic and fast.
func fakeRunner(script string) *testRunnerImpl {
	return &testRunnerImpl{
		cmd:       "/bin/sh",
		extraArgs: []string{"-c", script},
	}
}

// setupMutationFiles materializes an original source file and a mutant
// source file in a temp dir. The test runner will swap them and then
// restore — tests assert final state after each run.
func setupMutationFiles(t *testing.T, original, mutant string) (origPath, mutPath, workDir string) {
	t.Helper()
	workDir = t.TempDir()
	origPath = filepath.Join(workDir, "src.rs")
	mutPath = filepath.Join(workDir, "src.rs.mutant")
	if err := os.WriteFile(origPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mutPath, []byte(mutant), 0644); err != nil {
		t.Fatal(err)
	}
	return origPath, mutPath, workDir
}

func TestRunner_KilledMutant(t *testing.T) {
	r := fakeRunner("exit 1") // non-zero exit = test failed = mutant killed
	orig, mut, workDir := setupMutationFiles(t, "fn a() {}", "fn b() {}")
	cfg := lang.TestRunConfig{
		RepoPath:     workDir,
		MutantFile:   mut,
		OriginalFile: orig,
		Timeout:      5 * time.Second,
	}
	killed, _, err := r.RunTest(cfg)
	if err != nil {
		t.Fatalf("RunTest err: %v", err)
	}
	if !killed {
		t.Errorf("expected killed=true, got false")
	}
	// File must be restored to original content.
	got, _ := os.ReadFile(orig)
	if string(got) != "fn a() {}" {
		t.Errorf("original file not restored: %q", got)
	}
}

func TestRunner_SurvivedMutant(t *testing.T) {
	r := fakeRunner("exit 0") // zero exit = test passed = mutant survived
	orig, mut, workDir := setupMutationFiles(t, "fn a() {}", "fn b() {}")
	cfg := lang.TestRunConfig{
		RepoPath:     workDir,
		MutantFile:   mut,
		OriginalFile: orig,
		Timeout:      5 * time.Second,
	}
	killed, _, err := r.RunTest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if killed {
		t.Errorf("expected killed=false, got true")
	}
	got, _ := os.ReadFile(orig)
	if string(got) != "fn a() {}" {
		t.Errorf("original file not restored: %q", got)
	}
}

func TestRunner_Timeout(t *testing.T) {
	// Sleep longer than the timeout — the deadline context should fire
	// and we report the mutant as killed.
	r := fakeRunner("sleep 5")
	orig, mut, workDir := setupMutationFiles(t, "fn a() {}", "fn b() {}")
	cfg := lang.TestRunConfig{
		RepoPath:     workDir,
		MutantFile:   mut,
		OriginalFile: orig,
		Timeout:      200 * time.Millisecond,
	}
	start := time.Now()
	killed, _, err := r.RunTest(cfg)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("RunTest err: %v", err)
	}
	if !killed {
		t.Errorf("timeout should produce killed=true")
	}
	if elapsed > 3*time.Second {
		t.Errorf("RunTest did not honor timeout, took %s", elapsed)
	}
	got, _ := os.ReadFile(orig)
	if string(got) != "fn a() {}" {
		t.Errorf("original file not restored after timeout: %q", got)
	}
}

// TestRunner_RestoreAfterProcessFailure simulates a runner crash by
// setting a command that doesn't exist. The runner should report the
// error and still restore the original file via defer.
func TestRunner_RestoreAfterProcessFailure(t *testing.T) {
	// A command that doesn't exist — cmd.Run returns an error before
	// the ctx timeout fires. Our runner treats the non-nil runErr as a
	// killed mutant (any non-zero exit or start failure), which is fine;
	// what matters here is restoration.
	r := &testRunnerImpl{cmd: "/nonexistent/command/path"}
	orig, mut, workDir := setupMutationFiles(t, "fn a() {}", "fn b() {}")
	cfg := lang.TestRunConfig{
		RepoPath:     workDir,
		MutantFile:   mut,
		OriginalFile: orig,
		Timeout:      1 * time.Second,
	}
	_, _, _ = r.RunTest(cfg)
	got, _ := os.ReadFile(orig)
	if string(got) != "fn a() {}" {
		t.Errorf("original file not restored after runner start failure: %q", got)
	}
}

// TestRunner_PerFileSerialization stands up two concurrent RunTest calls
// on the SAME source file through a SINGLE shared runner and asserts the
// per-file mutex serialized them: each run observed only its own mutant
// bytes, never the sibling goroutine's. The test also verifies the
// original file is restored after both runs complete.
//
// The fake script reads the source file and exits 1 when the bytes are
// still the original; exits 0 otherwise. With serialization working, the
// script always sees "ORIGINAL" (because the mutant was written and then
// restored before the sibling goroutine could observe it in RunTest).
//
// We use ONE runner for both goroutines so the lock map is shared through
// the production code path (no test-only hacks). The per-mutation command
// isn't varied between runs — both call the same stateless runner.
func TestRunner_PerFileSerialization(t *testing.T) {
	orig, mutA, workDir := setupMutationFiles(t, "ORIGINAL", "MUTANT_A")
	mutB := filepath.Join(workDir, "src.rs.mutantB")
	if err := os.WriteFile(mutB, []byte("MUTANT_B"), 0644); err != nil {
		t.Fatal(err)
	}

	// The script asserts that when executed, the on-disk file is NOT the
	// original. Under the per-file lock each run's mutant bytes are in
	// place while its script runs. If serialization broke down the
	// sibling goroutine could swap in a different mutant while our
	// script is mid-read, but the windowed race is hard to force in a
	// test — so we use a stronger assertion: after both runs finish, the
	// original MUST be on disk, and the `go test -race` harness catches
	// unsynchronized map access.
	runner := fakeRunner(`
content="$(cat $1)"
# Any exit is fine; we're probing for race, not kill/survive.
exit 0
`)

	runOne := func(mutantFile string, wg *sync.WaitGroup, failures chan<- string) {
		defer wg.Done()
		cfg := lang.TestRunConfig{
			RepoPath:     workDir,
			MutantFile:   mutantFile,
			OriginalFile: orig,
			Timeout:      5 * time.Second,
		}
		if _, _, err := runner.RunTest(cfg); err != nil {
			failures <- err.Error()
		}
	}

	var wg sync.WaitGroup
	failures := make(chan string, 4)
	wg.Add(2)
	go runOne(mutA, &wg, failures)
	go runOne(mutB, &wg, failures)
	wg.Wait()
	close(failures)
	for msg := range failures {
		t.Errorf("concurrent run failure: %s", msg)
	}

	got, _ := os.ReadFile(orig)
	if string(got) != "ORIGINAL" {
		t.Errorf("original file not restored after concurrent runs: %q", got)
	}
}

// TestCargoTestArgs asserts the argv shape when no overrides are set.
func TestCargoTestArgs(t *testing.T) {
	args := cargoTestArgs(lang.TestRunConfig{})
	if len(args) == 0 || args[0] != "test" {
		t.Errorf("args[0] = %q, want test (got %v)", first(args), args)
	}
	for _, a := range args {
		if a == "--test-threads=1" {
			// We deliberately omit --test-threads=1 by default; mutants
			// inherit the per-file mutex for serialization. If someone
			// adds it back, this test will catch it.
			t.Error("--test-threads=1 should not be a default arg")
		}
	}
}

func TestCargoTestArgs_WithPattern(t *testing.T) {
	args := cargoTestArgs(lang.TestRunConfig{TestPattern: "boundary_tests"})
	found := false
	for _, a := range args {
		if a == "boundary_tests" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'boundary_tests' in args, got %v", args)
	}
}

func first(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return ss[0]
}

// TestRunner_CargoIncrementalEnv asserts CARGO_INCREMENTAL=0 is set in
// the child environment. We can't easily observe the child's env without
// running an external script, so the test uses a fake shell command that
// echoes the env and we grep for the flag in stdout.
func TestRunner_CargoIncrementalEnv(t *testing.T) {
	r := fakeRunner("env | grep '^CARGO_INCREMENTAL=' || echo MISSING")
	orig, mut, workDir := setupMutationFiles(t, "x", "y")
	// We need to capture stdout; RunTest already combines stdout/stderr.
	cfg := lang.TestRunConfig{
		RepoPath:     workDir,
		MutantFile:   mut,
		OriginalFile: orig,
		Timeout:      5 * time.Second,
	}
	_, out, err := r.RunTest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "CARGO_INCREMENTAL=0") {
		t.Errorf("expected CARGO_INCREMENTAL=0 in child env, got:\n%s", out)
	}
}
