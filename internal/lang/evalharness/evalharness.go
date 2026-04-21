// Package evalharness provides helpers shared by per-language evaluation
// test suites. Each analyzer package (rustanalyzer, tsanalyzer) has its
// own eval_test.go that drives the built diffguard binary against a tree
// of seeded fixtures and diff-compares emitted findings to an
// expected.json file next to the fixture.
//
// The harness:
//   - Builds the diffguard binary once per test run (sync.Once inside
//     BuildBinary) to keep the eval suites under 30s wall-clock when the
//     full language set is exercised.
//   - Copies each fixture into a temp dir before running so fixtures stay
//     pristine regardless of what any analyzer writes (mutation tests
//     swap files in place, so this matters).
//   - Runs the binary with stable flags (--output json, fixed
//     --mutation-sample-rate, etc.) and returns a decoded report.Report.
//   - Exposes a semantic equality helper: compares sections by
//     (name, severity) and finding sets by (file, function, severity,
//     operator). Exact counts / percentages / order-within-group are
//     not asserted because sampling and hashmap iteration can shuffle
//     them without changing correctness.
package evalharness

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"

	"github.com/0xPolygon/diffguard/internal/report"
)

// Expectation is the shape of an expected.json file next to each fixture.
// It captures just the facts worth pinning — the presence/severity of
// sections and whether each analyzer surfaced any Finding for a given
// (file, function) key. Fields not listed here are intentionally not
// asserted on; eval assertions are about "did the right thing get
// flagged", not "did the output bytes match exactly".
type Expectation struct {
	// WorstSeverity, if non-empty, pins the overall Report.WorstSeverity.
	WorstSeverity report.Severity `json:"worst_severity,omitempty"`
	// Sections pins per-section expectations, keyed by section name
	// (without a language suffix — the harness strips that before
	// matching). If omitted the section is not checked.
	Sections []SectionExpectation `json:"sections,omitempty"`
}

// SectionExpectation pins a single Section's minimum expectations.
type SectionExpectation struct {
	// Name is the metric prefix without a language suffix, e.g.
	// "Cognitive Complexity" or "Mutation Testing".
	Name string `json:"name"`
	// Severity, if non-empty, pins Section.Severity.
	Severity report.Severity `json:"severity,omitempty"`
	// MustHaveFindings, if non-empty, requires a Finding matching each
	// entry. The harness matches by the fields that are present in the
	// expectation (non-zero values). An expectation with just File set
	// passes if any finding mentions that file, for example.
	MustHaveFindings []FindingExpectation `json:"must_have_findings,omitempty"`
	// MustNotHaveFindings, if true, requires len(Findings)==0 for the
	// section.
	MustNotHaveFindings bool `json:"must_not_have_findings,omitempty"`
}

// FindingExpectation is the subset of report.Finding fields used for
// semantic matching. Unset fields are ignored.
type FindingExpectation struct {
	File     string          `json:"file,omitempty"`
	Function string          `json:"function,omitempty"`
	Severity report.Severity `json:"severity,omitempty"`
	Operator string          `json:"operator,omitempty"`
}

// BinaryBuilder caches the built diffguard binary across tests within a
// package. Call GetBinary(t) from each test — the first call builds, the
// rest return the cached path. Using package-level state keeps the cost
// of running 6+ eval tests in a package to a single build.
type BinaryBuilder struct {
	once sync.Once
	path string
	err  error
}

// GetBinary returns the path to a compiled diffguard binary, building it
// on the first call. Subsequent calls return the same path. The binary
// lives in os.TempDir; we don't clean it up because keeping the cache
// warm across tests is worth the few MB.
func (b *BinaryBuilder) GetBinary(t *testing.T, repoRoot string) string {
	t.Helper()
	b.once.Do(func() {
		dir, err := os.MkdirTemp("", "diffguard-eval-bin-")
		if err != nil {
			b.err = err
			return
		}
		bin := filepath.Join(dir, "diffguard")
		if runtime.GOOS == "windows" {
			bin += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", bin, "./cmd/diffguard")
		cmd.Dir = repoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			b.err = &BuildError{Output: string(out), Err: err}
			return
		}
		b.path = bin
	})
	if b.err != nil {
		t.Fatalf("building diffguard binary: %v", b.err)
	}
	return b.path
}

// BuildError wraps the build command's exit status with the captured
// combined output so test failures show why the build failed.
type BuildError struct {
	Output string
	Err    error
}

func (e *BuildError) Error() string { return e.Err.Error() + "\n" + e.Output }

// RepoRoot walks upward from cwd until it finds go.mod, returning that
// directory. Eval tests live several packages deep; using this avoids
// hard-coding relative paths that break when go test is invoked from
// different working directories (IDE vs. CLI vs. CI).
func RepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			// Guard against the fixture's own go.mod by requiring the
			// repo to contain a cmd/diffguard directory too.
			if _, err := os.Stat(filepath.Join(dir, "cmd", "diffguard")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repo root (no go.mod with cmd/diffguard found)")
		}
		dir = parent
	}
}

// CopyFixture mirrors srcDir to a fresh temp dir and returns the path.
// The copy is rooted at t.TempDir so Go's test harness cleans it up.
// Directories are preserved but none of the fixture metadata (mode,
// mtime) is — eval tests don't care.
func CopyFixture(t *testing.T, srcDir string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(srcDir, path)
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
		t.Fatalf("copying fixture %s: %v", srcDir, err)
	}
	return dst
}

// RunBinary runs the diffguard binary against a repo dir with the
// provided extra flags and returns the decoded JSON report. The harness
// always sets --output json, --fail-on none (so exit codes don't kill
// the test), and passes the repo path as the final positional arg.
func RunBinary(t *testing.T, binary, repo string, extraArgs []string) report.Report {
	t.Helper()
	args := []string{"--output", "json", "--fail-on", "none"}
	args = append(args, extraArgs...)
	args = append(args, repo)

	cmd := exec.Command(binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if stderr.Len() > 0 {
		t.Logf("diffguard stderr:\n%s", stderr.String())
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Logf("diffguard exit=%d", ee.ExitCode())
		} else {
			t.Fatalf("running diffguard: %v", err)
		}
	}

	var rpt report.Report
	if err := json.Unmarshal(stdout.Bytes(), &rpt); err != nil {
		t.Fatalf("unmarshal report: %v\nstdout:\n%s", err, stdout.String())
	}
	return rpt
}

// LoadExpectation reads expected.json from a fixture directory. Returns
// (Expectation{}, false) if the file doesn't exist.
func LoadExpectation(t *testing.T, fixtureDir string) (Expectation, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureDir, "expected.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return Expectation{}, false
		}
		t.Fatalf("reading expected.json: %v", err)
	}
	var exp Expectation
	if err := json.Unmarshal(data, &exp); err != nil {
		t.Fatalf("parsing expected.json: %v", err)
	}
	return exp, true
}

// AssertMatches compares a report against an Expectation and fails the
// test with human-readable diagnostics on any mismatch. Assertions are
// semantic: section name (stripped of language suffix), severity, and
// finding identity — not line-exact counts or percentages.
func AssertMatches(t *testing.T, got report.Report, want Expectation) {
	t.Helper()
	assertWorstSeverity(t, got, want.WorstSeverity)
	for _, wantSec := range want.Sections {
		assertSection(t, got, wantSec)
	}
}

func assertWorstSeverity(t *testing.T, got report.Report, want report.Severity) {
	t.Helper()
	if want == "" {
		return
	}
	if got.WorstSeverity() != want {
		dumpReport(t, got)
		t.Errorf("WorstSeverity = %q, want %q", got.WorstSeverity(), want)
	}
}

func assertSection(t *testing.T, got report.Report, wantSec SectionExpectation) {
	t.Helper()
	sec := findSectionByPrefix(got, wantSec.Name)
	if sec == nil {
		t.Errorf("missing section starting with %q; got %v",
			wantSec.Name, sectionNames(got))
		return
	}
	if wantSec.Severity != "" && sec.Severity != wantSec.Severity {
		t.Errorf("section %q severity = %q, want %q (findings=%d)",
			sec.Name, sec.Severity, wantSec.Severity, len(sec.Findings))
	}
	if wantSec.MustNotHaveFindings && len(sec.Findings) > 0 {
		t.Errorf("section %q should have no findings, got %d:\n%s",
			sec.Name, len(sec.Findings), dumpFindings(sec.Findings))
	}
	for _, wantF := range wantSec.MustHaveFindings {
		if !anyMatchingFinding(sec.Findings, wantF) {
			t.Errorf("section %q missing finding %+v; findings were:\n%s",
				sec.Name, wantF, dumpFindings(sec.Findings))
		}
	}
}

// findSectionByPrefix returns the first section whose name starts with
// prefix. Section names in multi-language runs are suffixed with a
// `[<lang>]` marker; the prefix match makes callers oblivious to that
// distinction.
func findSectionByPrefix(r report.Report, prefix string) *report.Section {
	for i := range r.Sections {
		name := r.Sections[i].Name
		if name == prefix {
			return &r.Sections[i]
		}
		if len(name) > len(prefix) && name[:len(prefix)] == prefix &&
			(name[len(prefix)] == ' ' || name[len(prefix)] == '[') {
			return &r.Sections[i]
		}
	}
	return nil
}

// anyMatchingFinding reports whether any f in findings matches wantF on
// the fields wantF has set.
func anyMatchingFinding(findings []report.Finding, wantF FindingExpectation) bool {
	for _, f := range findings {
		if findingMatches(f, wantF) {
			return true
		}
	}
	return false
}

// findingMatches reports whether a single finding satisfies every non-zero
// field of wantF. Operator isn't a first-class field on report.Finding;
// mutation encodes it in Message as "SURVIVED: <desc> (<operator>)", so
// that field is checked by substring search.
func findingMatches(f report.Finding, wantF FindingExpectation) bool {
	if wantF.File != "" && !pathMatches(f.File, wantF.File) {
		return false
	}
	if wantF.Function != "" && f.Function != wantF.Function {
		return false
	}
	if wantF.Severity != "" && f.Severity != wantF.Severity {
		return false
	}
	if wantF.Operator != "" && !containsOperator(f.Message, wantF.Operator) {
		return false
	}
	return true
}

// pathMatches accepts either an exact match or a basename match. Fixture
// expectations usually pin basenames so analyzer path normalizations
// (relative vs absolute, repo-relative vs working-dir-relative) don't
// break the assertion.
func pathMatches(got, want string) bool {
	if got == want {
		return true
	}
	return filepath.Base(got) == filepath.Base(want)
}

// containsOperator reports whether msg names the operator — either as a
// parenthesized tail (`... (operator_name)`) or inline. Case-sensitive
// because all operator names in this codebase are lowercase_snake.
func containsOperator(msg, op string) bool {
	return bytesContains([]byte(msg), []byte(op))
}

// bytesContains is a tiny helper to avoid pulling in strings just for
// this. Returns true if sub appears in s.
func bytesContains(s, sub []byte) bool {
	return bytes.Contains(s, sub)
}

// dumpFindings formats findings for failure diagnostics.
func dumpFindings(findings []report.Finding) string {
	lines := make([]string, 0, len(findings))
	for _, f := range findings {
		lines = append(lines, "  - "+f.File+":"+f.Function+" ["+string(f.Severity)+"] "+f.Message)
	}
	sort.Strings(lines)
	var buf bytes.Buffer
	for _, l := range lines {
		buf.WriteString(l)
		buf.WriteString("\n")
	}
	return buf.String()
}

// dumpReport logs all section names + severities so failures are
// actionable without re-running with extra flags.
func dumpReport(t *testing.T, r report.Report) {
	t.Helper()
	for _, s := range r.Sections {
		t.Logf("  section %q -> %s (findings=%d)", s.Name, s.Severity, len(s.Findings))
	}
}

// sectionNames returns the names for diagnostics.
func sectionNames(r report.Report) []string {
	out := make([]string, len(r.Sections))
	for i, s := range r.Sections {
		out[i] = s.Name
	}
	return out
}
