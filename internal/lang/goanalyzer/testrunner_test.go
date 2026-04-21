package goanalyzer

import (
	"os"
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
