package tsanalyzer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/0xPolygon/diffguard/internal/lang"
)

// TestLanguageRegistration verifies the TypeScript analyzer registered
// itself and exposes the correct name + file filter.
func TestLanguageRegistration(t *testing.T) {
	l, ok := lang.Get("typescript")
	if !ok {
		t.Fatal("typescript language not registered")
	}
	if l.Name() != "typescript" {
		t.Errorf("Name() = %q, want %q", l.Name(), "typescript")
	}
	ff := l.FileFilter()
	if len(ff.Extensions) != 2 || ff.Extensions[0] != ".ts" || ff.Extensions[1] != ".tsx" {
		t.Errorf("Extensions = %v, want [.ts .tsx]", ff.Extensions)
	}
	if len(ff.DiffGlobs) != 2 || ff.DiffGlobs[0] != "*.ts" || ff.DiffGlobs[1] != "*.tsx" {
		t.Errorf("DiffGlobs = %v, want [*.ts *.tsx]", ff.DiffGlobs)
	}
}

func TestIsTSTestFile(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// Standard test-file patterns.
		{"src/foo.test.ts", true},
		{"src/foo.spec.ts", true},
		{"src/foo.test.tsx", true},
		{"src/foo.spec.tsx", true},
		// __tests__ / __mocks__ directory segments.
		{"src/__tests__/any.ts", true},
		{"src/__mocks__/any.ts", true},
		{"__tests__/foo.ts", true},
		{"deep/nested/__tests__/thing.ts", true},
		// Non-test files.
		{"src/foo.ts", false},
		{"src/foo.tsx", false},
		{"src/test-utils.ts", false},
		// The explicitly-called-out edge case: utils.test-helper.ts is NOT
		// a test file. The penultimate stem segment is `test-helper`, not
		// `test`.
		{"src/utils.test-helper.ts", false},
		{"src/tests_common.ts", false},
		// .js, .jsx, .mjs, .cjs must not be treated as TS test files.
		{"src/foo.test.js", false},
		{"src/foo.test.mjs", false},
		// Windows separators.
		{`src\__tests__\a.ts`, true},
		{`src\foo.test.ts`, true},
	}
	for _, tc := range cases {
		got := isTSTestFile(tc.path)
		if got != tc.want {
			t.Errorf("isTSTestFile(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestFileFilterIncludesSource(t *testing.T) {
	l, _ := lang.Get("typescript")
	ff := l.FileFilter()
	if !ff.IncludesSource("src/app.ts") {
		t.Error("expected src/app.ts to be included")
	}
	if !ff.IncludesSource("src/app.tsx") {
		t.Error("expected src/app.tsx to be included")
	}
	if ff.IncludesSource("src/app.test.ts") {
		t.Error("expected src/app.test.ts to be excluded")
	}
	if ff.IncludesSource("src/__tests__/x.ts") {
		t.Error("expected __tests__/x.ts to be excluded")
	}
	if ff.IncludesSource("src/app.js") {
		t.Error("expected .js to be excluded (JS-only repos out of scope)")
	}
}

// TestDetector_TSRepoMatches asserts a repo with package.json + at least
// one .ts file matches, while a JS-only repo (package.json + only .js)
// does NOT match. This is the behavior promised by the design doc.
func TestDetector_TSRepoMatches(t *testing.T) {
	root := t.TempDir()
	writeFile(filepath.Join(root, "package.json"), []byte(`{"name":"demo"}`))
	writeFile(filepath.Join(root, "src", "app.ts"), []byte(`export const x = 1;`))

	langs := lang.Detect(root)
	got := map[string]bool{}
	for _, l := range langs {
		got[l.Name()] = true
	}
	if !got["typescript"] {
		t.Errorf("expected typescript in detection, got %v", got)
	}
}

func TestDetector_JSOnlyRepoDoesNotMatch(t *testing.T) {
	root := t.TempDir()
	writeFile(filepath.Join(root, "package.json"), []byte(`{"name":"demo"}`))
	writeFile(filepath.Join(root, "src", "app.js"), []byte(`module.exports = {};`))

	langs := lang.Detect(root)
	for _, l := range langs {
		if l.Name() == "typescript" {
			t.Errorf("JS-only repo should not detect as typescript, got %v",
				func() []string {
					var ns []string
					for _, x := range langs {
						ns = append(ns, x.Name())
					}
					return ns
				}())
		}
	}
}

// TestDetector_NoPackageJSONDoesNotMatch: a .ts file alone without a
// package.json isn't a TS project we care about (not a node package).
func TestDetector_NoPackageJSONDoesNotMatch(t *testing.T) {
	root := t.TempDir()
	writeFile(filepath.Join(root, "src", "app.ts"), []byte(`export const x = 1;`))

	langs := lang.Detect(root)
	for _, l := range langs {
		if l.Name() == "typescript" {
			t.Error(".ts-only repo (no package.json) should not detect as typescript")
		}
	}
}

// TestDetector_IgnoresNodeModules ensures the walker doesn't wade into
// node_modules looking for .ts files — those would always be present on
// any node project and wouldn't indicate the repo itself is TypeScript.
func TestDetector_IgnoresNodeModules(t *testing.T) {
	root := t.TempDir()
	writeFile(filepath.Join(root, "package.json"), []byte(`{"name":"demo"}`))
	// Only a .ts file under node_modules — hasTSFile must skip it.
	writeFile(filepath.Join(root, "node_modules", "pkg", "lib.ts"), []byte(`export const x = 1;`))

	if hasTSFile(root) {
		t.Error("hasTSFile should skip node_modules and return false for JS-only repo layout")
	}
}

// writeFile is the package-local test helper mirroring the Rust analyzer.
func writeFile(path string, data []byte) {
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	_ = os.WriteFile(path, data, 0644)
}
