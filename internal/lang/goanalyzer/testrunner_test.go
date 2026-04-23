package goanalyzer

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/0xPolygon/diffguard/internal/lang"
)

func TestWriteOverlayJSON(t *testing.T) {
	dir := t.TempDir()
	overlayPath := filepath.Join(dir, "overlay.json")
	if err := writeOverlayJSON(overlayPath, "/orig/foo.go", "/tmp/mutated.go"); err != nil {
		t.Fatalf("writeOverlayJSON error: %v", err)
	}
	data, err := os.ReadFile(overlayPath)
	if err != nil {
		t.Fatal(err)
	}
	// Must be the exact shape go test -overlay expects:
	// {"Replace":{"<original>":"<mutant>"}}
	expected := `{"Replace":{"/orig/foo.go":"/tmp/mutated.go"}}`
	if string(data) != expected {
		t.Errorf("overlay JSON = %q, want %q", string(data), expected)
	}
}

func TestBuildTestArgs_Default(t *testing.T) {
	args := buildTestArgs(lang.TestRunConfig{}, "/tmp/overlay.json")
	if args[0] != "test" {
		t.Errorf("args[0] = %q, want test", args[0])
	}
	foundOverlay := false
	for _, a := range args {
		if a == "-overlay=/tmp/overlay.json" {
			foundOverlay = true
		}
	}
	if !foundOverlay {
		t.Errorf("expected -overlay in args, got %v", args)
	}
	for _, a := range args {
		if a == "-run" {
			t.Error("did not expect -run in default args")
		}
	}
}

func TestBuildTestArgs_WithPattern(t *testing.T) {
	args := buildTestArgs(lang.TestRunConfig{TestPattern: "TestFoo"}, "/tmp/overlay.json")
	found := false
	for i, a := range args {
		if a == "-run" && i+1 < len(args) && args[i+1] == "TestFoo" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected -run TestFoo in args, got %v", args)
	}
}

func TestBuildTestArgs_TimeoutPassed(t *testing.T) {
	args := buildTestArgs(lang.TestRunConfig{}, "/tmp/overlay.json")
	// Default timeout (30s) should be formatted as "30s"
	found := false
	for i, a := range args {
		if a == "-timeout" && i+1 < len(args) && args[i+1] == "30s" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected -timeout 30s in args, got %v", args)
	}
}

// TestRunTest_OverlayWriteFailsReturnsError forces the overlay-write
// failure path by pointing WorkDir at a non-existent nested directory.
func TestRunTest_OverlayWriteFailsReturnsError(t *testing.T) {
	// WorkDir that doesn't exist: writeOverlayJSON will fail on Create.
	cfg := lang.TestRunConfig{
		WorkDir:      filepath.Join(t.TempDir(), "missing", "dir"),
		OriginalFile: "/tmp/orig.go",
		MutantFile:   "/tmp/mut.go",
		Index:        0,
	}
	killed, out, err := testRunnerImpl{}.RunTest(cfg)
	if err == nil {
		t.Fatal("expected an error when overlay directory is missing")
	}
	if killed {
		t.Error("killed should be false on setup error")
	}
	if out != "" {
		t.Errorf("output should be empty on setup error, got %q", out)
	}
}

// TestRunTest_KillsMutantWhenTestFails end-to-end-verifies the kill path
// by creating a tiny Go module whose test fails after an overlay swaps in
// a bad file. The runner must return killed=true and a non-empty output.
func TestRunTest_KillsMutantWhenTestFails(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go binary not on PATH")
	}
	modDir := t.TempDir()
	writeFile := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(modDir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("go.mod", "module example.com/mut\n\ngo 1.21\n")
	writeFile("m.go", "package mut\n\nfunc Add(a, b int) int { return a + b }\n")
	writeFile("m_test.go", "package mut\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1, 2) != 3 { t.Fail() } }\n")

	mutant := `package mut

func Add(a, b int) int { return a - b }
`
	mutantPath := filepath.Join(t.TempDir(), "m.go")
	if err := os.WriteFile(mutantPath, []byte(mutant), 0644); err != nil {
		t.Fatal(err)
	}

	work := t.TempDir()
	cfg := lang.TestRunConfig{
		WorkDir:      work,
		OriginalFile: filepath.Join(modDir, "m.go"),
		MutantFile:   mutantPath,
		Index:        1,
	}
	killed, _, err := testRunnerImpl{}.RunTest(cfg)
	if err != nil {
		t.Fatalf("RunTest: %v", err)
	}
	if !killed {
		t.Error("expected killed=true when tests fail")
	}
}

// TestRunTest_LivesWhenTestsPass covers the survive (!killed) path.
func TestRunTest_LivesWhenTestsPass(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go binary not on PATH")
	}
	modDir := t.TempDir()
	writeFile := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(modDir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("go.mod", "module example.com/mut\n\ngo 1.21\n")
	writeFile("m.go", "package mut\n\nfunc Add(a, b int) int { return a + b }\n")
	writeFile("m_test.go", "package mut\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1, 2) != 3 { t.Fail() } }\n")

	// Mutant is semantically equivalent so tests still pass.
	mutant := "package mut\n\nfunc Add(a, b int) int { return b + a }\n"
	mutantPath := filepath.Join(t.TempDir(), "m.go")
	if err := os.WriteFile(mutantPath, []byte(mutant), 0644); err != nil {
		t.Fatal(err)
	}

	work := t.TempDir()
	cfg := lang.TestRunConfig{
		WorkDir:      work,
		OriginalFile: filepath.Join(modDir, "m.go"),
		MutantFile:   mutantPath,
		Index:        2,
	}
	killed, out, err := testRunnerImpl{}.RunTest(cfg)
	if err != nil {
		t.Fatalf("RunTest: %v", err)
	}
	if killed {
		t.Error("expected killed=false when mutant is equivalent")
	}
	if out != "" {
		t.Errorf("expected empty output on survive, got %q", out)
	}
}
