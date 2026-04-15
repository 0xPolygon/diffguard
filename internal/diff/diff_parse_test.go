package diff

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initGitRepo creates a minimal git repo in dir with a single base commit on
// the "main" branch, suitable for driving Parse in tests.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init", "-q", "--initial-branch=main")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "commit.gpgsign", "false")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestParse_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	_, err := Parse(dir, "main")
	if err == nil {
		t.Fatal("expected error when running Parse outside a git repo")
	}
	if !strings.Contains(err.Error(), "git merge-base") {
		t.Errorf("expected wrapped git merge-base error, got: %v", err)
	}
}

func TestParse_MissingBaseBranch(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "init")

	_, err := Parse(dir, "no-such-branch")
	if err == nil {
		t.Fatal("expected error for nonexistent base branch")
	}
	if !strings.Contains(err.Error(), "git merge-base") {
		t.Errorf("expected wrapped git merge-base error, got: %v", err)
	}
}

func TestParse_SuccessDetectsChangedGoFile(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Base commit on main.
	mustWrite(t, filepath.Join(dir, "base.go"), "package x\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "base")

	// Feature branch with a new .go file.
	runGit(t, dir, "checkout", "-q", "-b", "feature")
	mustWrite(t, filepath.Join(dir, "new.go"), "package x\n\nfunc f() {}\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "add new.go")

	result, err := Parse(dir, "main")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if result.BaseBranch != "main" {
		t.Errorf("BaseBranch = %q, want main", result.BaseBranch)
	}
	if len(result.Files) != 1 {
		t.Fatalf("expected 1 changed file, got %d: %v", len(result.Files), result.Files)
	}
	if result.Files[0].Path != "new.go" {
		t.Errorf("path = %q, want new.go", result.Files[0].Path)
	}
	if len(result.Files[0].Regions) == 0 {
		t.Error("expected at least one changed region")
	}
}

func TestParse_IgnoresTestFiles(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	mustWrite(t, filepath.Join(dir, "base.go"), "package x\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "base")

	runGit(t, dir, "checkout", "-q", "-b", "feature")
	mustWrite(t, filepath.Join(dir, "a_test.go"), "package x\n\nfunc TestFoo(t *testing.T) {}\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "add test")

	result, err := Parse(dir, "main")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(result.Files) != 0 {
		t.Errorf("expected 0 non-test changes, got %v", result.Files)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
