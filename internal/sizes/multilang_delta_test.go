package sizes

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
	_ "github.com/0xPolygon/diffguard/internal/lang/rustanalyzer"
	_ "github.com/0xPolygon/diffguard/internal/lang/tsanalyzer"
	"github.com/0xPolygon/diffguard/internal/report"
)

// TestSizesDelta_AcrossLanguages mirrors the complexity multi-language test:
// confirms that delta-gating drops oversized-but-untouched-size functions
// and files for TypeScript and Rust as well as Go. The orchestrator is
// language-agnostic, but tree-sitter analyzers can fail in subtle ways on
// temp paths if extension preservation is wrong, so this test guards the
// integration.
func TestSizesDelta_AcrossLanguages(t *testing.T) {
	cases := []struct {
		name     string
		lang     string
		ext      string
		manifest map[string]string
		// header / footer wrap a language-appropriate "no-op statement".
		// The body has 80 such statements so the function lands above
		// funcThreshold=50; a single-line tweak in the diff overlaps the
		// function but doesn't change line count → delta gate must drop.
		header string
		footer string
		stmt   func(i int) string
	}{
		{
			name: "typescript",
			lang: "typescript",
			ext:  ".ts",
			manifest: map[string]string{
				"package.json": `{"name":"x","version":"1.0.0"}` + "\n",
			},
			header: "export function big() {\n",
			footer: "}\n",
			stmt:   func(i int) string { return fmt.Sprintf("    let x%d = %d\n", i, i) },
		},
		{
			name: "rust",
			lang: "rust",
			ext:  ".rs",
			manifest: map[string]string{
				"Cargo.toml": "[package]\nname = \"x\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
			},
			header: "pub fn big() {\n",
			footer: "}\n",
			stmt:   func(i int) string { return fmt.Sprintf("    let _x%d = %d;\n", i, i) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := initRepo(t)
			for name, content := range tc.manifest {
				writeAndCommit(t, dir, name, content, "manifest")
			}
			path := "lib" + tc.ext
			writeAndCommit(t, dir, path, buildBigFile(tc.header, tc.footer, tc.stmt, "v1"), "base")

			runGit(t, dir, "checkout", "-q", "-b", "feature")
			writeAndCommit(t, dir, path, buildBigFile(tc.header, tc.footer, tc.stmt, "v2"), "tweak inner comment")

			s := analyzeMultiLangSizes(t, dir, "main", tc.lang)
			for _, f := range s.Findings {
				t.Errorf("%s: legacy size finding leaked through delta gate: %+v", tc.name, f)
			}
		})
	}
}

func analyzeMultiLangSizes(t *testing.T, dir, base, langName string) report.Section {
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
	section, err := Analyze(dir, d, 50, 500, DeltaTolerances{}, l.FunctionExtractor())
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	return section
}

// buildBigFile assembles a >50-line function whose only diff between
// versions is a single comment marker inside the body, so the diff overlaps
// the function but the line count is unchanged.
func buildBigFile(header, footer string, stmt func(int) string, marker string) string {
	var sb strings.Builder
	sb.WriteString(header)
	for i := range 80 {
		if i == 0 {
			sb.WriteString("    // " + marker + "\n")
		}
		sb.WriteString(stmt(i))
	}
	sb.WriteString(footer)
	return sb.String()
}
