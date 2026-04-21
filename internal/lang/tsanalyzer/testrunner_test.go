package tsanalyzer

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
// of the real vitest/jest/npm — keeps tests hermetic and fast.
func fakeRunner(script string) *testRunnerImpl {
	return &testRunnerImpl{
		cmd:       "/bin/sh",
		extraArgs: []string{"-c", script},
	}
}

// setupMutationFiles materializes an original source file and a mutant
// source file in a temp dir + a minimal package.json so the detector
// has somewhere to look. The test runner will swap them and then
// restore — tests assert final state after each run.
func setupMutationFiles(t *testing.T, original, mutant string) (origPath, mutPath, workDir string) {
	t.Helper()
	workDir = t.TempDir()
	// Minimal package.json so detectTSRunner has a file to read even if
	// the concrete test doesn't care about runner detection.
	_ = os.WriteFile(filepath.Join(workDir, "package.json"), []byte(`{"name":"demo"}`), 0644)

	origPath = filepath.Join(workDir, "src.ts")
	mutPath = filepath.Join(workDir, "src.ts.mutant")
	if err := os.WriteFile(origPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mutPath, []byte(mutant), 0644); err != nil {
		t.Fatal(err)
	}
	return origPath, mutPath, workDir
}

func TestRunner_KilledMutant(t *testing.T) {
	r := fakeRunner("exit 1") // non-zero exit = test failed = killed
	orig, mut, workDir := setupMutationFiles(t, "const a = 1;", "const b = 2;")
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
	got, _ := os.ReadFile(orig)
	if string(got) != "const a = 1;" {
		t.Errorf("original file not restored: %q", got)
	}
}

func TestRunner_SurvivedMutant(t *testing.T) {
	r := fakeRunner("exit 0")
	orig, mut, workDir := setupMutationFiles(t, "const a = 1;", "const b = 2;")
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
	if string(got) != "const a = 1;" {
		t.Errorf("original file not restored: %q", got)
	}
}

func TestRunner_Timeout(t *testing.T) {
	r := fakeRunner("sleep 5")
	orig, mut, workDir := setupMutationFiles(t, "const a = 1;", "const b = 2;")
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
	if string(got) != "const a = 1;" {
		t.Errorf("original file not restored after timeout: %q", got)
	}
}

// TestRunner_RestoreAfterProcessFailure simulates a runner crash by
// using a non-existent command. The runner should still restore the
// original file via defer.
func TestRunner_RestoreAfterProcessFailure(t *testing.T) {
	r := &testRunnerImpl{cmd: "/nonexistent/command/path"}
	orig, mut, workDir := setupMutationFiles(t, "const a = 1;", "const b = 2;")
	cfg := lang.TestRunConfig{
		RepoPath:     workDir,
		MutantFile:   mut,
		OriginalFile: orig,
		Timeout:      1 * time.Second,
	}
	_, _, _ = r.RunTest(cfg)
	got, _ := os.ReadFile(orig)
	if string(got) != "const a = 1;" {
		t.Errorf("original file not restored after runner start failure: %q", got)
	}
}

// TestRunner_PerFileSerialization — mirror of the Rust analyzer test.
func TestRunner_PerFileSerialization(t *testing.T) {
	orig, mutA, workDir := setupMutationFiles(t, "ORIGINAL", "MUTANT_A")
	mutB := filepath.Join(workDir, "src.ts.mutantB")
	if err := os.WriteFile(mutB, []byte("MUTANT_B"), 0644); err != nil {
		t.Fatal(err)
	}

	runner := fakeRunner(`
content="$(cat $1 2>/dev/null)"
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

// TestRunner_CIEnv asserts CI=true is set in the child environment so
// vitest/jest don't prompt for interactive input.
func TestRunner_CIEnv(t *testing.T) {
	r := fakeRunner("env | grep '^CI=' || echo MISSING")
	orig, mut, workDir := setupMutationFiles(t, "x", "y")
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
	if !strings.Contains(out, "CI=true") {
		t.Errorf("expected CI=true in child env, got:\n%s", out)
	}
}

// TestDetectTSRunner exercises the runner selection precedence by staging
// package.json variants in temp dirs.
func TestDetectTSRunner(t *testing.T) {
	cases := []struct {
		name string
		pkg  string
		want string
	}{
		{
			name: "vitest preferred",
			pkg:  `{"devDependencies":{"vitest":"^1.0.0","jest":"^29.0.0"}}`,
			want: "vitest",
		},
		{
			name: "jest fallback",
			pkg:  `{"devDependencies":{"jest":"^29.0.0"}}`,
			want: "jest",
		},
		{
			name: "@jest/core counted as jest",
			pkg:  `{"devDependencies":{"@jest/core":"^29.0.0"}}`,
			want: "jest",
		},
		{
			name: "neither => empty (npm test fallback)",
			pkg:  `{"name":"demo"}`,
			want: "",
		},
		{
			name: "dependencies section also counts",
			pkg:  `{"dependencies":{"vitest":"^1.0.0"}}`,
			want: "vitest",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(tc.pkg), 0644); err != nil {
				t.Fatal(err)
			}
			got := detectTSRunner(dir)
			if got != tc.want {
				t.Errorf("detectTSRunner = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestTSTestArgs_Vitest asserts vitest argv shape including TestPattern.
func TestTSTestArgs_Vitest(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"devDependencies":{"vitest":"^1.0.0"}}`), 0644)

	cmdName, args := tsTestArgs(dir, lang.TestRunConfig{TestPattern: "feature X"})
	if cmdName != "npx" {
		t.Errorf("cmd = %q, want npx", cmdName)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "vitest") || !strings.Contains(joined, "run") {
		t.Errorf("vitest args missing expected tokens: %v", args)
	}
	if !strings.Contains(joined, "-t feature X") {
		t.Errorf("expected -t pattern in args, got %v", args)
	}
}

// TestTSTestArgs_Jest asserts jest argv shape including TestPattern.
func TestTSTestArgs_Jest(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"devDependencies":{"jest":"^29.0.0"}}`), 0644)

	cmdName, args := tsTestArgs(dir, lang.TestRunConfig{TestPattern: "foo"})
	if cmdName != "npx" {
		t.Errorf("cmd = %q, want npx", cmdName)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "jest") {
		t.Errorf("jest arg missing: %v", args)
	}
	if !strings.Contains(joined, "--testNamePattern foo") {
		t.Errorf("expected --testNamePattern foo, got %v", args)
	}
}

// TestTSTestArgs_NpmTestFallback asserts the npm-test fallback when
// neither vitest nor jest is declared.
func TestTSTestArgs_NpmTestFallback(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"name":"demo"}`), 0644)

	cmdName, args := tsTestArgs(dir, lang.TestRunConfig{})
	if cmdName != "npm" {
		t.Errorf("cmd = %q, want npm", cmdName)
	}
	if len(args) != 1 || args[0] != "test" {
		t.Errorf("args = %v, want [test]", args)
	}
}
