package baseline

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func initRepoWith(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "--initial-branch=main")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func TestFullCoverage(t *testing.T) {
	fc := FullCoverage("foo/bar.go")
	if fc.Path != "foo/bar.go" {
		t.Errorf("Path = %q, want foo/bar.go", fc.Path)
	}
	if len(fc.Regions) != 1 {
		t.Fatalf("Regions = %d, want 1", len(fc.Regions))
	}
	r := fc.Regions[0]
	if r.StartLine != 1 {
		t.Errorf("StartLine = %d, want 1", r.StartLine)
	}
	if r.EndLine != math.MaxInt32 {
		t.Errorf("EndLine = %d, want math.MaxInt32 (so OverlapsRange always matches)", r.EndLine)
	}
}

func TestFetchToTemp_HappyPath(t *testing.T) {
	dir := initRepoWith(t, map[string]string{"a.go": "package x\nfunc F() {}\n"})

	tmp, err := FetchToTemp(dir, "HEAD", "a.go")
	if err != nil {
		t.Fatalf("FetchToTemp: %v", err)
	}
	if tmp == "" {
		t.Fatal("tmp = \"\", want a path for an existing file")
	}
	defer os.Remove(tmp)

	// Extension preservation matters: some analyzers branch on path ext.
	if filepath.Ext(tmp) != ".go" {
		t.Errorf("temp ext = %q, want .go (must be preserved)", filepath.Ext(tmp))
	}

	// Bytes round-trip.
	got, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "func F()") {
		t.Errorf("temp content missing original; got %q", got)
	}
}

func TestFetchToTemp_AbsentReturnsEmptyPath(t *testing.T) {
	dir := initRepoWith(t, map[string]string{"a.go": "package x\n"})

	tmp, err := FetchToTemp(dir, "HEAD", "missing.go")
	if err != nil {
		t.Errorf("err = %v, want nil for absent path", err)
	}
	if tmp != "" {
		os.Remove(tmp)
		t.Errorf("tmp = %q, want \"\" for absent path", tmp)
	}
}

func TestFetchToTemp_BadRefSurfacesError(t *testing.T) {
	dir := initRepoWith(t, map[string]string{"a.go": "package x\n"})

	_, err := FetchToTemp(dir, "no-such-ref", "a.go")
	if err == nil {
		t.Fatal("expected error for unknown ref")
	}
}

func TestFetchToTemp_PreservesExtension(t *testing.T) {
	dir := initRepoWith(t, map[string]string{
		"a.ts":  "export const x = 1\n",
		"b.rs":  "fn main() {}\n",
		"c.txt": "hi\n",
	})

	for _, name := range []string{"a.ts", "b.rs", "c.txt"} {
		tmp, err := FetchToTemp(dir, "HEAD", name)
		if err != nil {
			t.Fatalf("FetchToTemp(%s): %v", name, err)
		}
		want := filepath.Ext(name)
		if got := filepath.Ext(tmp); got != want {
			t.Errorf("%s: tmp ext = %q, want %q", name, got, want)
		}
		os.Remove(tmp)
	}
}
