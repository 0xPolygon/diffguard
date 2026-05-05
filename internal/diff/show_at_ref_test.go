package diff

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestShowAtRef_ReturnsContent: happy path — file exists at the requested
// ref and the bytes come back verbatim.
func TestShowAtRef_ReturnsContent(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	mustWrite(t, filepath.Join(dir, "a.txt"), "hello\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "init")

	got, err := ShowAtRef(dir, "HEAD", "a.txt")
	if err != nil {
		t.Fatalf("ShowAtRef: %v", err)
	}
	if string(got) != "hello\n" {
		t.Errorf("content = %q, want %q", got, "hello\n")
	}
}

// TestShowAtRef_AbsentPath: a path that doesn't exist at ref must come back
// as (nil, nil), not as an error — that's the "no baseline" signal that
// callers (delta gating) rely on.
func TestShowAtRef_AbsentPath(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	mustWrite(t, filepath.Join(dir, "exists.txt"), "x\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "init")

	got, err := ShowAtRef(dir, "HEAD", "missing.txt")
	if err != nil {
		t.Errorf("err = %v, want nil for absent path", err)
	}
	if got != nil {
		t.Errorf("got = %q, want nil for absent path", got)
	}
}

// TestShowAtRef_BadRef: a malformed/unknown ref must surface as an error so
// callers don't silently treat it as "no baseline" and let regressions slip.
func TestShowAtRef_BadRef(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	mustWrite(t, filepath.Join(dir, "a.txt"), "x\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "init")

	_, err := ShowAtRef(dir, "no-such-ref", "a.txt")
	if err == nil {
		t.Fatal("expected error for unknown ref, got nil")
	}
	if !strings.Contains(err.Error(), "git show") {
		t.Errorf("err = %v, expected to mention git show", err)
	}
}

// TestShowAtRef_NotGitRepo: running outside a git repo must surface an
// error, not be silently swallowed as "absent path".
func TestShowAtRef_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		t.Fatalf("temp dir unexpectedly has .git")
	}
	_, err := ShowAtRef(dir, "HEAD", "a.txt")
	if err == nil {
		t.Fatal("expected error outside git repo")
	}
}
