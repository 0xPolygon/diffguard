package lang

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetect_ManifestMatch(t *testing.T) {
	defer UnregisterForTest("test-detect-manifest")
	Register(&fakeLang{name: "test-detect-manifest"})
	RegisterManifest("test-detect-marker", "test-detect-manifest")
	t.Cleanup(func() {
		manifestMu.Lock()
		delete(manifests, "test-detect-marker")
		manifestMu.Unlock()
	})

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test-detect-marker"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	found := names(Detect(dir))
	if !contains(found, "test-detect-manifest") {
		t.Errorf("Detect returned %v, want it to include test-detect-manifest", found)
	}
}

func TestDetect_CustomDetector(t *testing.T) {
	defer UnregisterForTest("test-detect-custom")
	Register(&fakeLang{name: "test-detect-custom"})
	RegisterDetector("test-detect-custom", func(string) bool { return true })
	t.Cleanup(func() {
		detectorMu.Lock()
		delete(detectors, "test-detect-custom")
		detectorMu.Unlock()
	})

	dir := t.TempDir()
	found := names(Detect(dir))
	if !contains(found, "test-detect-custom") {
		t.Errorf("Detect returned %v, want it to include test-detect-custom", found)
	}
}

func TestDetect_EmptyRepo(t *testing.T) {
	dir := t.TempDir()
	// No languages with matching manifests should fire on an empty dir.
	// We can't assert len==0 because goanalyzer's init() registered "go"
	// with a go.mod manifest, and there's no go.mod in the tempdir so "go"
	// should not match.
	found := names(Detect(dir))
	if contains(found, "go") {
		t.Errorf("Detect on empty dir returned %v, did not expect 'go'", found)
	}
}

func TestDetect_MultipleLanguages(t *testing.T) {
	defer UnregisterForTest("test-multi-a")
	defer UnregisterForTest("test-multi-b")
	Register(&fakeLang{name: "test-multi-a"})
	Register(&fakeLang{name: "test-multi-b"})
	RegisterManifest("marker-a", "test-multi-a")
	RegisterManifest("marker-b", "test-multi-b")
	t.Cleanup(func() {
		manifestMu.Lock()
		delete(manifests, "marker-a")
		delete(manifests, "marker-b")
		manifestMu.Unlock()
	})

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "marker-a"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "marker-b"), []byte("x"), 0644)

	found := names(Detect(dir))
	if !contains(found, "test-multi-a") || !contains(found, "test-multi-b") {
		t.Errorf("Detect returned %v, want both test-multi-a and test-multi-b", found)
	}

	// Ordering must be deterministic (sorted by Name()).
	idxA, idxB := -1, -1
	for i, n := range found {
		if n == "test-multi-a" {
			idxA = i
		}
		if n == "test-multi-b" {
			idxB = i
		}
	}
	if idxA > idxB {
		t.Errorf("Detect did not sort by name: %v", found)
	}
}

func TestDetect_UnregisteredManifestIgnored(t *testing.T) {
	// Register a manifest pointing to a language that is NOT registered.
	// Detect should not include it in the results.
	RegisterManifest("unknown-manifest", "no-such-language")
	t.Cleanup(func() {
		manifestMu.Lock()
		delete(manifests, "unknown-manifest")
		manifestMu.Unlock()
	})

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "unknown-manifest"), []byte("x"), 0644)

	found := names(Detect(dir))
	if contains(found, "no-such-language") {
		t.Errorf("Detect returned unregistered language: %v", found)
	}
}

func names(langs []Language) []string {
	out := make([]string, len(langs))
	for i, l := range langs {
		out[i] = l.Name()
	}
	return out
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}
