package rustanalyzer

import (
	"testing"

	"github.com/0xPolygon/diffguard/internal/lang"
)

// TestLanguageRegistration verifies the Rust analyzer registered itself
// and exposes the correct name + file filter. The init() function runs on
// package load so the registry should already contain "rust" by the time
// this test executes.
func TestLanguageRegistration(t *testing.T) {
	l, ok := lang.Get("rust")
	if !ok {
		t.Fatal("rust language not registered")
	}
	if l.Name() != "rust" {
		t.Errorf("Name() = %q, want %q", l.Name(), "rust")
	}
	ff := l.FileFilter()
	if len(ff.Extensions) != 1 || ff.Extensions[0] != ".rs" {
		t.Errorf("Extensions = %v, want [.rs]", ff.Extensions)
	}
	if len(ff.DiffGlobs) != 1 || ff.DiffGlobs[0] != "*.rs" {
		t.Errorf("DiffGlobs = %v, want [*.rs]", ff.DiffGlobs)
	}
}

func TestIsRustTestFile(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// Integration tests live under a `tests` directory at any depth.
		{"tests/integration.rs", true},
		{"crates/foo/tests/integration.rs", true},
		{"tests/subdir/more.rs", true},
		// Source files never count as tests, even when the path mentions
		// the word "test" in a non-segment context.
		{"src/lib.rs", false},
		{"src/tester.rs", false},
		{"src/foo/bar.rs", false},
		// Trailing slash variants don't confuse the segment split.
		{"src/tests_common.rs", false},
		// Windows separators should behave the same for consistency
		// across platforms.
		{`tests\integration.rs`, true},
	}
	for _, tc := range cases {
		got := isRustTestFile(tc.path)
		if got != tc.want {
			t.Errorf("isRustTestFile(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestFileFilterIncludesSource(t *testing.T) {
	l, _ := lang.Get("rust")
	ff := l.FileFilter()
	if !ff.IncludesSource("src/lib.rs") {
		t.Error("expected src/lib.rs to be included")
	}
	if ff.IncludesSource("tests/integration.rs") {
		t.Error("expected tests/integration.rs to be excluded")
	}
	if ff.IncludesSource("build.py") {
		t.Error("expected non-.rs files to be excluded")
	}
}
