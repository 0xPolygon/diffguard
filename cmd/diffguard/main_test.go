package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xPolygon/diffguard/internal/lang"
	"github.com/0xPolygon/diffguard/internal/report"
)

// TestRun_SingleLanguageGo is the B6 smoke test: runs the orchestrator
// against a temp git repo with a single .go file change. Exercises the
// end-to-end path (CLI config → language resolution → diff parse →
// analyzer pipeline → report build → exit code) without spawning a
// subprocess.
//
// TODO: once Rust and TypeScript analyzers land, extend this test with
// fixture files in the same temp repo and assert all three language
// sections appear in the output. The current test only has the Go
// analyzer registered, so multi-language section naming isn't exercised.
func TestRun_SingleLanguageGo(t *testing.T) {
	repo := initTempGoRepo(t)

	cfg := Config{
		ComplexityThreshold:   10,
		FunctionSizeThreshold: 50,
		FileSizeThreshold:     500,
		SkipMutation:          true,
		Output:                "text",
		FailOn:                "none",
		BaseBranch:            "main",
	}

	// Redirect stdout/stderr so the test doesn't pollute output. We don't
	// assert on exact content here — the byte-identical regression gate
	// covers that — but we do assert run() returns no error.
	withSuppressedStdio(t, func() {
		if err := run(repo, cfg); err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	})
}

// TestRun_UnknownLanguageHardError locks in that an unknown --language
// value fails with a clear error rather than silently falling back to
// auto-detect.
func TestRun_UnknownLanguageHardError(t *testing.T) {
	repo := initTempGoRepo(t)
	cfg := Config{
		Output:     "text",
		FailOn:     "none",
		BaseBranch: "main",
		Language:   "cobol",
	}
	err := run(repo, cfg)
	if err == nil {
		t.Fatal("expected error for unknown language, got nil")
	}
	if !strings.Contains(err.Error(), "cobol") {
		t.Errorf("error = %q, want it to mention 'cobol'", err.Error())
	}
}

// TestResolveLanguages_ExplicitGo verifies the comma-split path.
func TestResolveLanguages_ExplicitGo(t *testing.T) {
	repo := initTempGoRepo(t)
	langs, err := resolveLanguages(repo, "go")
	if err != nil {
		t.Fatalf("resolveLanguages: %v", err)
	}
	if len(langs) != 1 || langs[0].Name() != "go" {
		t.Errorf("langs = %v, want [go]", names(langs))
	}
}

// TestResolveLanguages_AutoDetect verifies that a repo with go.mod is
// auto-detected as Go.
func TestResolveLanguages_AutoDetect(t *testing.T) {
	repo := initTempGoRepo(t)
	langs, err := resolveLanguages(repo, "")
	if err != nil {
		t.Fatalf("resolveLanguages: %v", err)
	}
	if len(langs) != 1 || langs[0].Name() != "go" {
		t.Errorf("langs = %v, want [go]", names(langs))
	}
}

// TestResolveLanguages_EmptyDetection fails cleanly when nothing is
// detectable and no --language is provided.
func TestResolveLanguages_EmptyDetection(t *testing.T) {
	dir := t.TempDir()
	_, err := resolveLanguages(dir, "")
	if err == nil {
		t.Fatal("expected error for empty detection")
	}
	if !strings.Contains(err.Error(), "--language") {
		t.Errorf("error = %q, expected hint about --language", err.Error())
	}
}

// TestResolveLanguages_Deduplicates ensures passing "go,go" returns one
// Language, not two.
func TestResolveLanguages_Deduplicates(t *testing.T) {
	repo := initTempGoRepo(t)
	langs, err := resolveLanguages(repo, "go,go")
	if err != nil {
		t.Fatalf("resolveLanguages: %v", err)
	}
	if len(langs) != 1 {
		t.Errorf("len = %d, want 1 (dedup)", len(langs))
	}
}

// initTempGoRepo creates a minimal git repo with a single committed Go
// file on main, plus an additional file on HEAD so the diff has content.
// Returns the absolute path to the repo.
func initTempGoRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// init + author config
	run("init", "-q", "--initial-branch=main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	run("config", "commit.gpgsign", "false")

	// base commit with go.mod + a base file so Parse has something to
	// merge-base against.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/testrepo\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "base.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "base")

	// Feature commit adds a new file with a small function. This is what
	// appears in the diff.
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("package main\n\nfunc helper(x int) int {\n\tif x > 0 {\n\t\treturn x\n\t}\n\treturn -x\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "add new.go")

	return dir
}

// withSuppressedStdio redirects os.Stdout/Stderr to /dev/null for the
// duration of fn. Restores on return.
func withSuppressedStdio(t *testing.T, fn func()) {
	t.Helper()
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()

	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout = devnull
	os.Stderr = devnull
	defer func() {
		os.Stdout = origOut
		os.Stderr = origErr
	}()
	fn()
}

func names(langs []lang.Language) []string {
	out := make([]string, len(langs))
	for i, l := range langs {
		out[i] = l.Name()
	}
	return out
}

// TestCheckExitCode_FailInAnyLanguageEscalates covers B5: a FAIL section
// in any language must escalate the overall exit code, regardless of how
// many languages contribute sections. checkExitCode already takes a
// merged report, so this is a unit-level check on WorstSeverity behavior
// mirrored through checkExitCode.
func TestCheckExitCode_FailInAnyLanguageEscalates(t *testing.T) {
	fail := report.Section{Name: "Complexity [rust]", Severity: report.SeverityFail}
	pass := report.Section{Name: "Complexity [go]", Severity: report.SeverityPass}
	warn := report.Section{Name: "Sizes [typescript]", Severity: report.SeverityWarn}

	merged := report.Report{Sections: []report.Section{pass, fail, warn}}

	// fail-on=warn: any FAIL escalates.
	if err := checkExitCode(merged, "warn"); err == nil {
		t.Error("fail-on=warn with FAIL section should return error")
	}

	// fail-on=all: any non-PASS escalates (FAIL or WARN).
	if err := checkExitCode(merged, "all"); err == nil {
		t.Error("fail-on=all with FAIL section should return error")
	}

	// fail-on=none: never escalates.
	if err := checkExitCode(merged, "none"); err != nil {
		t.Errorf("fail-on=none should not error, got %v", err)
	}

	// All PASS: no error.
	allPass := report.Report{Sections: []report.Section{pass, pass}}
	if err := checkExitCode(allPass, "warn"); err != nil {
		t.Errorf("all-PASS should not error, got %v", err)
	}
}
