package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xPolygon/diffguard/internal/diff"
)

func TestRun_RejectsPathsWithBase(t *testing.T) {
	err := run(t.TempDir(), Config{Paths: "miner", BaseBranch: "develop"})
	if err == nil {
		t.Fatal("expected error for --paths with --base")
	}
	if !strings.Contains(err.Error(), "--paths and --base are mutually exclusive") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRun_RejectsPathsWithIncludePaths(t *testing.T) {
	err := run(t.TempDir(), Config{Paths: "miner", IncludePaths: "miner"})
	if err == nil {
		t.Fatal("expected error for --paths with --include-paths")
	}
	if !strings.Contains(err.Error(), "--paths and --include-paths are mutually exclusive") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAnnounceMessage(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "refactoring mode",
			cfg:  Config{Paths: "miner"},
			want: "Analyzing 2 Go files (refactoring mode)...",
		},
		{
			name: "filtered diff mode",
			cfg:  Config{BaseBranch: "develop", IncludePaths: "miner"},
			want: "Analyzing 2 changed Go files against develop (filtered to miner)...",
		},
		{
			name: "plain diff mode",
			cfg:  Config{BaseBranch: "develop"},
			want: "Analyzing 2 changed Go files against develop...",
		},
	}

	for _, tt := range tests {
		if got := announceMessage(2, tt.cfg); got != tt.want {
			t.Fatalf("%s: announceMessage() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestLoadFiles_IncludePathsFiltersDiff(t *testing.T) {
	repo := initGitRepo(t)
	writeFile(t, filepath.Join(repo, "miner", "a.go"), "package miner\n\nfunc A() int { return 1 }\n")
	writeFile(t, filepath.Join(repo, "other", "b.go"), "package other\n\nfunc B() int { return 1 }\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "initial")

	git(t, repo, "checkout", "-b", "feature")
	writeFile(t, filepath.Join(repo, "miner", "a.go"), "package miner\n\nfunc A() int { return 2 }\n")
	writeFile(t, filepath.Join(repo, "other", "b.go"), "package other\n\nfunc B() int { return 2 }\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "change")

	d, err := loadFiles(repo, Config{BaseBranch: "develop", IncludePaths: "miner"})
	if err != nil {
		t.Fatalf("loadFiles error: %v", err)
	}

	if len(d.Files) != 1 {
		t.Fatalf("expected 1 filtered file, got %d", len(d.Files))
	}
	if d.Files[0].Path != "miner/a.go" {
		t.Fatalf("filtered path = %q, want miner/a.go", d.Files[0].Path)
	}
}

func TestFilterDiffFiles_EmptyIncludePaths(t *testing.T) {
	d := &diff.Result{
		BaseBranch: "develop",
		Files: []diff.FileChange{
			{Path: "miner/a.go"},
		},
	}

	filtered, err := filterDiffFiles(t.TempDir(), d, "")
	if err != nil {
		t.Fatalf("filterDiffFiles error: %v", err)
	}
	if filtered != d {
		t.Fatal("expected empty include-paths to return original diff result")
	}
}

func TestFilterDiffFiles_InvalidIncludePaths(t *testing.T) {
	d := &diff.Result{
		BaseBranch: "develop",
		Files: []diff.FileChange{
			{Path: "miner/a.go"},
		},
	}

	_, err := filterDiffFiles(t.TempDir(), d, " , ")
	if err == nil {
		t.Fatal("expected invalid include-paths to return an error")
	}
	if !strings.Contains(err.Error(), "--include-paths requires at least one path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParsePathList_RejectsEmptyInput(t *testing.T) {
	_, err := parsePathList(" , ", "paths")
	if err == nil {
		t.Fatal("expected empty path list to return an error")
	}
	if !strings.Contains(err.Error(), "--paths requires at least one path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()

	repo := t.TempDir()
	git(t, repo, "init")
	git(t, repo, "config", "user.email", "test@example.com")
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "commit.gpgsign", "false")
	git(t, repo, "checkout", "-b", "develop")
	return repo
}

func git(t *testing.T, repo string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}
