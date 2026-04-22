package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
// The cross-language E1 integration test lives below in
// TestMixedRepo_* — those build the binary and run it as a subprocess
// against the three-language fixture in testdata/mixed-repo/.
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

// TestResolveLanguages_OnlyCommas fails with the "empty --language flag"
// hard error when the value contains nothing but separators. This exercises
// the final "len(out) == 0" guard that turns an empty parse into a visible
// error rather than falling back to auto-detect.
func TestResolveLanguages_OnlyCommas(t *testing.T) {
	repo := initTempGoRepo(t)
	_, err := resolveLanguages(repo, ", , ,")
	if err == nil {
		t.Fatal("expected error for empty --language value after splitting")
	}
	if !strings.Contains(err.Error(), "empty --language flag") {
		t.Errorf("error = %q, want mention of 'empty --language flag'", err.Error())
	}
}

// TestRegisteredNames_ListsGo verifies the helper returns at least "go"
// (other languages are registered via blank-import and may or may not be
// linked into this test binary).
func TestRegisteredNames_ListsGo(t *testing.T) {
	names := registeredNames()
	if len(names) == 0 {
		t.Fatal("expected at least one registered language")
	}
	found := false
	for _, n := range names {
		if n == "go" {
			found = true
		}
	}
	if !found {
		t.Errorf("registeredNames = %v, expected 'go'", names)
	}
}

// TestRun_NoFilesInDiff_SingleLanguage drives run() with a --paths filter
// that matches nothing. Exercises the "No <lang> files found." single-
// language short-circuit — the len(d.Files)==0 branch that mutation tests
// flagged as under-tested.
func TestRun_NoFilesInDiff_SingleLanguage(t *testing.T) {
	dir := t.TempDir()
	// Write a go.mod so language auto-detection succeeds, but no .go files
	// so the diff comes back empty.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		SkipMutation: true,
		Output:       "text",
		FailOn:       "none",
		Paths:        ".", // refactoring mode skips git diff entirely
	}
	withSuppressedStdio(t, func() {
		if err := run(dir, cfg); err != nil {
			t.Errorf("run should succeed with empty diff, got %v", err)
		}
	})
}

// TestLanguageNoun_KnownLanguagesAndFallback covers every branch of the
// switch: known language with special capitalization, plus the default
// fallback for an unrecognized name. A stub Language lets us hit the
// default without registering a real language implementation.
func TestLanguageNoun_KnownLanguagesAndFallback(t *testing.T) {
	got := languageNoun(stubLanguage("go"))
	if got != "Go" {
		t.Errorf("languageNoun(go) = %q, want Go", got)
	}
	got = languageNoun(stubLanguage("rust"))
	if got != "Rust" {
		t.Errorf("languageNoun(rust) = %q, want Rust", got)
	}
	got = languageNoun(stubLanguage("typescript"))
	if got != "TypeScript" {
		t.Errorf("languageNoun(typescript) = %q, want TypeScript", got)
	}
	// The fallback branch must echo the raw name.
	got = languageNoun(stubLanguage("unknown"))
	if got != "unknown" {
		t.Errorf("languageNoun(unknown) = %q, want unknown (fallback)", got)
	}
}

// stubLanguage implements just enough of lang.Language to exercise
// languageNoun. Every accessor returns nil because languageNoun only
// reads Name(); the test is in cmd/diffguard so we can't register it
// globally anyway.
type stubLanguage string

func (s stubLanguage) Name() string                             { return string(s) }
func (s stubLanguage) FileFilter() lang.FileFilter              { return lang.FileFilter{} }
func (s stubLanguage) ComplexityCalculator() lang.ComplexityCalculator { return nil }
func (s stubLanguage) FunctionExtractor() lang.FunctionExtractor       { return nil }
func (s stubLanguage) ImportResolver() lang.ImportResolver             { return nil }
func (s stubLanguage) ComplexityScorer() lang.ComplexityScorer         { return nil }
func (s stubLanguage) MutantGenerator() lang.MutantGenerator           { return nil }
func (s stubLanguage) MutantApplier() lang.MutantApplier               { return nil }
func (s stubLanguage) AnnotationScanner() lang.AnnotationScanner       { return nil }
func (s stubLanguage) TestRunner() lang.TestRunner                     { return nil }

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

// --- E1: mixed-repo end-to-end ---
//
// These tests build the diffguard binary via `go build` and exec it against
// the fixture at cmd/diffguard/testdata/mixed-repo/. The fixture has two
// variants: `violations/` with functions that trip the complexity threshold
// in every language, and `clean/` with trivial functions. The tests run in
// refactoring mode (--paths .) and --skip-mutation so they stay fast and
// don't require cargo / node / tests on $PATH.

// TestMixedRepo_ViolationsHasAllThreeLanguageSections asserts the positive
// variant produces a section for each registered language with the [lang]
// suffix, and that the overall verdict is FAIL (the seeded complexity
// violations are across every language).
func TestMixedRepo_ViolationsHasAllThreeLanguageSections(t *testing.T) {
	binary := buildDiffguardBinary(t)
	repo := copyFixture(t, "testdata/mixed-repo/violations")

	rpt := runBinaryJSON(t, binary, repo, []string{
		"--paths", ".",
		"--skip-mutation",
		"--fail-on", "none",
		"--output", "json",
	})

	// Expect at least one section per language, suffixed by [lang]. We
	// don't pin exact section counts because future analyzers may add
	// more, but [go]/[rust]/[typescript] must all appear.
	wantSuffixes := []string{"[go]", "[rust]", "[typescript]"}
	for _, suf := range wantSuffixes {
		if !anySectionHasSuffix(rpt, suf) {
			t.Errorf("expected at least one section with suffix %s; got sections: %v",
				suf, sectionNames(rpt))
		}
	}

	// Complexity per-language must be FAIL in the violations fixture.
	for _, lang := range []string{"go", "rust", "typescript"} {
		sec := findSectionBySuffix(rpt, "Cognitive Complexity", lang)
		if sec == nil {
			t.Errorf("missing Cognitive Complexity [%s] section", lang)
			continue
		}
		if sec.Severity != report.SeverityFail {
			t.Errorf("Cognitive Complexity [%s] severity = %q, want FAIL",
				lang, sec.Severity)
		}
	}

	if rpt.WorstSeverity() != report.SeverityFail {
		t.Errorf("WorstSeverity = %q, want FAIL", rpt.WorstSeverity())
	}
}

// TestMixedRepo_CleanAllPass asserts the negative control (no violations)
// produces PASS across all language sections.
func TestMixedRepo_CleanAllPass(t *testing.T) {
	binary := buildDiffguardBinary(t)
	repo := copyFixture(t, "testdata/mixed-repo/clean")

	rpt := runBinaryJSON(t, binary, repo, []string{
		"--paths", ".",
		"--skip-mutation",
		"--fail-on", "none",
		"--output", "json",
	})

	for _, suf := range []string{"[go]", "[rust]", "[typescript]"} {
		if !anySectionHasSuffix(rpt, suf) {
			t.Errorf("expected at least one section with suffix %s; got sections: %v",
				suf, sectionNames(rpt))
		}
	}

	if rpt.WorstSeverity() != report.SeverityPass {
		// Dump section severities for diagnostics.
		for _, s := range rpt.Sections {
			t.Logf("  %s -> %s", s.Name, s.Severity)
		}
		t.Errorf("WorstSeverity = %q, want PASS", rpt.WorstSeverity())
	}
}

// --- Helpers used by the mixed-repo tests ---

// buildDiffguardBinary builds the CLI to a temp dir and returns the path.
// The test's t.Cleanup removes the dir so no build artifacts pollute the
// source tree.
func buildDiffguardBinary(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "diffguard")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, ".")
	// Build from the package dir so `.` resolves correctly.
	cmd.Dir = packageDir(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// packageDir returns the directory containing the current test binary's
// package source. Works for both `go test ./cmd/diffguard` and `go test ./...`.
func packageDir(t *testing.T) string {
	t.Helper()
	// The test runs with cwd == the package directory by default.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}

// copyFixture copies a testdata subdir into an isolated temp dir. Tests
// must never mutate the source tree, and some analyzers (churn) call git
// inside repoPath; a fresh copy keeps both concerns clean.
func copyFixture(t *testing.T, relDir string) string {
	t.Helper()
	src := filepath.Join(packageDir(t), relDir)
	dst := t.TempDir()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
	if err != nil {
		t.Fatalf("copying fixture %s: %v", relDir, err)
	}
	return dst
}

// runBinaryJSON executes the binary against the repo dir and decodes the
// JSON report from stdout. Stderr is streamed to the test log for debug
// visibility. Non-zero exit is tolerated (caller controls --fail-on) as
// long as stdout parses.
func runBinaryJSON(t *testing.T, binary, repo string, args []string) report.Report {
	t.Helper()
	full := append([]string{}, args...)
	full = append(full, repo)
	cmd := exec.Command(binary, full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if stderr.Len() > 0 {
		t.Logf("diffguard stderr:\n%s", stderr.String())
	}
	// Only a genuine run failure (e.g. can't find the repo) is a problem
	// here. An exit=1 due to FAIL is expected in the violations test and
	// we opt out via --fail-on=none anyway.
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.Logf("diffguard exited with %d (ok for --fail-on=none runs)", exitErr.ExitCode())
		}
	}
	var rpt report.Report
	if err := json.Unmarshal(stdout.Bytes(), &rpt); err != nil {
		t.Fatalf("unmarshal report: %v\nstdout was:\n%s", err, stdout.String())
	}
	return rpt
}

func anySectionHasSuffix(r report.Report, suffix string) bool {
	for _, s := range r.Sections {
		if strings.HasSuffix(s.Name, suffix) {
			return true
		}
	}
	return false
}

func sectionNames(r report.Report) []string {
	out := make([]string, len(r.Sections))
	for i, s := range r.Sections {
		out[i] = s.Name
	}
	return out
}

// findSectionBySuffix finds the section whose name is "<metricPrefix> [lang]".
func findSectionBySuffix(r report.Report, metricPrefix, langName string) *report.Section {
	want := metricPrefix + " [" + langName + "]"
	for i := range r.Sections {
		if r.Sections[i].Name == want {
			return &r.Sections[i]
		}
	}
	return nil
}
