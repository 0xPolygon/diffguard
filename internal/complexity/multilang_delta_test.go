package complexity

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
	_ "github.com/0xPolygon/diffguard/internal/lang/rustanalyzer"
	_ "github.com/0xPolygon/diffguard/internal/lang/tsanalyzer"
	"github.com/0xPolygon/diffguard/internal/report"
)

// TestDeltaGating_AcrossLanguages locks in that delta gating is
// language-agnostic: the orchestrator drives whichever ComplexityCalculator
// the language registers, and the temp-file-with-preserved-extension trick
// works for the tree-sitter-based TypeScript and Rust analyzers as well as
// for Go. Without this test, a regression in the TS/Rust path (e.g. an
// analyzer that rejects unknown paths or that produces non-deterministic
// names) could land silently.
func TestDeltaGating_AcrossLanguages(t *testing.T) {
	cases := []struct {
		name     string
		lang     string
		ext      string
		manifest map[string]string
		// baseV / featV differ on a single comment line *inside* the
		// function body, so OverlapsRange picks the function up but
		// cognitive complexity is identical → delta gating must drop it.
		baseV string
		featV string
	}{
		{
			name: "typescript",
			lang: "typescript",
			ext:  ".ts",
			manifest: map[string]string{
				"package.json": `{"name":"x","version":"1.0.0"}` + "\n",
			},
			baseV: tsBody("v1"),
			featV: tsBody("v2"),
		},
		{
			name: "rust",
			lang: "rust",
			ext:  ".rs",
			manifest: map[string]string{
				"Cargo.toml": "[package]\nname = \"x\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
			},
			baseV: rustBody("v1"),
			featV: rustBody("v2"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := initRepo(t)
			for name, content := range tc.manifest {
				writeAndCommit(t, dir, name, content, "manifest")
			}
			// Tree-sitter front-ends parse the file directly — Cargo's
			// usual src/ layout isn't required for complexity analysis.
			path := "lib" + tc.ext
			writeAndCommit(t, dir, path, tc.baseV, "base")

			runGit(t, dir, "checkout", "-q", "-b", "feature")
			writeAndCommit(t, dir, path, tc.featV, "tweak inner comment")

			s := analyzeMultiLang(t, dir, "main", tc.lang)
			if s.Severity != report.SeverityPass {
				t.Errorf("severity = %v, want PASS (legacy complexity not worsened); findings=%+v", s.Severity, s.Findings)
			}
			if len(s.Findings) != 0 {
				t.Errorf("findings = %d, want 0", len(s.Findings))
			}
		})
	}
}

// analyzeMultiLang mirrors parseAndAnalyze but takes a language name so the
// TS / Rust analyzers can be exercised without dragging Go-specific helpers
// into the test setup.
func analyzeMultiLang(t *testing.T, dir, base, langName string) report.Section {
	t.Helper()
	l, ok := lang.Get(langName)
	if !ok {
		t.Fatalf("language %q not registered", langName)
	}
	d, err := diff.Parse(dir, base, diff.Filter{
		DiffGlobs: l.FileFilter().DiffGlobs,
		Includes: func(p string) bool {
			ff := l.FileFilter()
			if !slices.Contains(ff.Extensions, filepath.Ext(p)) {
				return false
			}
			return !ff.IsTestFile(p)
		},
	})
	if err != nil {
		t.Fatalf("diff.Parse: %v", err)
	}
	section, err := Analyze(dir, d, 10, 0, l.ComplexityCalculator())
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	return section
}

// tsBody returns a TypeScript function body whose 6-deep nested ifs land
// cognitive complexity well above the threshold. tag goes inside the
// inner-most block so the diff between v1 and v2 lands inside the function.
func tsBody(tag string) string {
	return `export function complex(x: number) {
    if (x > 0) {
        if (x > 10) {
            if (x > 100) {
                if (x > 1000) {
                    if (x > 10000) {
                        if (x > 100000) {
                            return x // ` + tag + `
                        }
                    }
                }
            }
        }
    }
    return 0
}
`
}

// rustBody mirrors tsBody for Rust. Same nesting shape so cognitive scores
// stay comparable across languages.
func rustBody(tag string) string {
	return `pub fn complex(x: i32) -> i32 {
    if x > 0 {
        if x > 10 {
            if x > 100 {
                if x > 1000 {
                    if x > 10000 {
                        if x > 100000 {
                            return x; // ` + tag + `
                        }
                    }
                }
            }
        }
    }
    0
}
`
}
