package lang

import (
	"testing"

	"github.com/0xPolygon/diffguard/internal/diff"
)

// fakeLang is a minimal Language stub used to exercise the registry. Its
// sub-component accessors all return nil — nothing calls them in the
// registry-only tests.
type fakeLang struct{ name string }

func (f *fakeLang) Name() string                              { return f.name }
func (f *fakeLang) FileFilter() FileFilter                    { return FileFilter{} }
func (f *fakeLang) ComplexityCalculator() ComplexityCalculator { return nil }
func (f *fakeLang) FunctionExtractor() FunctionExtractor      { return nil }
func (f *fakeLang) ImportResolver() ImportResolver            { return nil }
func (f *fakeLang) ComplexityScorer() ComplexityScorer        { return nil }
func (f *fakeLang) MutantGenerator() MutantGenerator          { return nil }
func (f *fakeLang) MutantApplier() MutantApplier              { return nil }
func (f *fakeLang) AnnotationScanner() AnnotationScanner      { return nil }
func (f *fakeLang) TestRunner() TestRunner                    { return nil }
func (f *fakeLang) DeadCodeDetector() DeadCodeDetector        { return nil }

// Silence the unused-import check — the import is kept so that fakeLang
// remains plug-compatible with the analyzer interfaces that reference the
// diff package in their method signatures.
var _ = diff.FileChange{}

func TestRegister_And_Get(t *testing.T) {
	defer UnregisterForTest("test-registry-1")

	l := &fakeLang{name: "test-registry-1"}
	Register(l)

	got, ok := Get("test-registry-1")
	if !ok {
		t.Fatal("expected Get to find registered language")
	}
	if got.Name() != "test-registry-1" {
		t.Errorf("Get returned %q, want test-registry-1", got.Name())
	}

	if _, ok := Get("no-such-language"); ok {
		t.Error("Get should return false for unknown name")
	}
}

func TestRegister_DuplicatePanics(t *testing.T) {
	defer UnregisterForTest("test-dup")

	Register(&fakeLang{name: "test-dup"})

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	Register(&fakeLang{name: "test-dup"})
}

func TestRegister_NilPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil registration")
		}
	}()
	Register(nil)
}

func TestRegister_EmptyNamePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on empty-name registration")
		}
	}()
	Register(&fakeLang{name: ""})
}

func TestAll_SortedByName(t *testing.T) {
	// Use distinct prefixes so we don't collide with any real language
	// registrations coming from goanalyzer/init().
	defer UnregisterForTest("zzz-all-b")
	defer UnregisterForTest("zzz-all-a")
	defer UnregisterForTest("zzz-all-c")

	Register(&fakeLang{name: "zzz-all-b"})
	Register(&fakeLang{name: "zzz-all-a"})
	Register(&fakeLang{name: "zzz-all-c"})

	all := All()
	// Filter to just our test fakes so real registrations (e.g. "go" from
	// goanalyzer) don't disturb the ordering assertion.
	var got []string
	for _, l := range all {
		if len(l.Name()) >= 4 && l.Name()[:4] == "zzz-" {
			got = append(got, l.Name())
		}
	}
	want := []string{"zzz-all-a", "zzz-all-b", "zzz-all-c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("All[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
