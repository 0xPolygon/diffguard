package lang

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// manifestFiles maps a repo-root filename to the language Name() that owns
// it. When multiple languages share a manifest (e.g. package.json for JS and
// TS), the ambiguity is resolved inside the language's own detection hook
// — here we only record the canonical owner.
//
// Languages without a manifest (or where the manifest needs extra inspection
// to disambiguate) can add themselves to this map from their init() via
// RegisterManifest so the auto-detector still picks them up.
var (
	manifestMu sync.Mutex
	manifests  = map[string]string{}
)

// RegisterManifest associates a repo-root filename with a language name.
// A language implementation typically calls this alongside Register():
//
//	func init() {
//	    lang.Register(&Language{})
//	    lang.RegisterManifest("go.mod", "go")
//	}
//
// The detector only fires on files that exist at the repository root, so
// sub-directory manifests (e.g. nested Cargo.toml for workspaces) don't
// falsely trigger; languages that need subtree scanning should implement
// their own detection hook via RegisterDetector.
func RegisterManifest(filename, languageName string) {
	manifestMu.Lock()
	defer manifestMu.Unlock()
	manifests[filename] = languageName
}

// Detector is a per-language hook that reports whether the given repo root
// contains a project of this language. Languages use RegisterDetector when
// manifest-file matching is too coarse — e.g. "package.json + at least one
// .ts file" for TypeScript.
type Detector func(repoPath string) bool

var (
	detectorMu sync.Mutex
	detectors  = map[string]Detector{}
)

// RegisterDetector associates a language name with a custom detection
// function. Both the detector (if present) and the manifest file (if
// registered) are consulted during Detect; a language matches if either
// returns true.
func RegisterDetector(languageName string, d Detector) {
	detectorMu.Lock()
	defer detectorMu.Unlock()
	detectors[languageName] = d
}

// Detect scans repoPath for per-language manifest files and custom detectors
// and returns the languages whose signatures match. The returned slice is
// sorted by Name() so report ordering stays deterministic across calls.
//
// Only languages that are both (a) registered via Register and (b) match via
// a manifest or detector are returned. That way, adding a new language to
// the binary without a matching manifest entry is inert — nothing misfires.
func Detect(repoPath string) []Language {
	matched := map[string]bool{}

	// Manifest-based detection.
	manifestMu.Lock()
	for filename, name := range manifests {
		if _, err := os.Stat(filepath.Join(repoPath, filename)); err == nil {
			matched[name] = true
		}
	}
	manifestMu.Unlock()

	// Custom-detector fallback. Languages that can't be distinguished by a
	// single manifest file (TypeScript vs. JavaScript, for example) install
	// a detector that inspects the tree.
	detectorMu.Lock()
	for name, d := range detectors {
		if d(repoPath) {
			matched[name] = true
		}
	}
	detectorMu.Unlock()

	var out []Language
	registryMu.RLock()
	for name := range matched {
		if l, ok := registryMap[name]; ok {
			out = append(out, l)
		}
	}
	registryMu.RUnlock()

	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}
