package goanalyzer

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/0xPolygon/diffguard/internal/lang"
)

// instrumentSource instruments every mutant of code and returns the result
// with the sites, numbering mutants from 1 in generation order.
func instrumentSource(t *testing.T, code string) (string, []lang.MutantSite, []bool) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.go"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	sites := sitesOf(t, dir, "test.go")
	ids := make([]int, len(sites))
	for i := range ids {
		ids[i] = i + 1
	}
	src, embedded, pkg, err := instrumentFile(filepath.Join(dir, "test.go"), sites, ids)
	if err != nil {
		t.Fatal(err)
	}
	if pkg != "test" {
		t.Errorf("package name = %q, want test", pkg)
	}
	return string(src), sites, embedded
}

func TestInstrumentFile_GuardsEachOperator(t *testing.T) {
	src, _, embedded := instrumentSource(t, `package test

func f(a, b int) (int, bool) {
	if a > b {
		a++
		b--
		note()
	}
	return a * b, a == b
}

func note() {}
`)
	for _, want := range []string{
		"if __dgGt(2, a, b) {\n\t\tif !__dgOn(1) {",
		"__dgInc(3, &a)",
		"__dgDec(4, &b)",
		"if !__dgOn(5) {\n\t\t\t\tnote()",
		"if __dgOn(6) {\n\t\treturn *new(int), *new(bool)\n\t} else {",
		"return __dgMul(7, a, b), ((a == b) != __dgOn(8))",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("instrumented source is missing %q:\n%s", want, src)
		}
	}
	for i, ok := range embedded {
		if !ok {
			t.Errorf("mutant %d was not embedded", i+1)
		}
	}
}

func TestInstrumentFile_BooleanLiteralStaysUntyped(t *testing.T) {
	src, _, _ := instrumentSource(t, "package test\n\ntype flag bool\n\nfunc f() flag {\n\tvar on flag = true\n\treturn on\n}\n")
	if !strings.Contains(src, "var on flag = ((true) != __dgOn(1))") {
		t.Errorf("got:\n%s", src)
	}
}

// Arithmetic on two literals is folded exactly by the compiler; routing it
// through a runtime helper could change the unmutated result.
func TestInstrumentFile_LeavesLiteralArithmeticToTheOverlayPath(t *testing.T) {
	src, sites, embedded := instrumentSource(t, "package test\n\nfunc f() float64 {\n\tx := 0.1 + 0.2\n\treturn x\n}\n")
	for i, s := range sites {
		if s.Operator == "math_operator" && embedded[i] {
			t.Errorf("literal arithmetic was embedded:\n%s", src)
		}
	}
	if !strings.Contains(src, "x := 0.1 + 0.2") {
		t.Errorf("literal arithmetic was rewritten:\n%s", src)
	}
}

func TestInstrumentFile_KeepsOnlyDirectiveComments(t *testing.T) {
	src, _, _ := instrumentSource(t, "//go:build !never\n\npackage test\n\n// f is documented.\n//\n//go:noinline\nfunc f(a int) bool {\n\t// plain comment\n\treturn a == 1\n}\n")
	if !strings.Contains(src, "//go:build !never") || !strings.Contains(src, "//go:noinline") {
		t.Errorf("directives were dropped:\n%s", src)
	}
	if strings.Contains(src, "plain comment") {
		t.Errorf("ordinary comments should be dropped:\n%s", src)
	}
}

func TestInstrumentFile_ParseError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.go")
	os.WriteFile(path, []byte("package"), 0o644)
	if _, _, _, err := instrumentFile(path, nil, nil); err == nil {
		t.Error("want a parse error")
	}
}

func TestHasDirective(t *testing.T) {
	src, _, _ := instrumentSource(t, "package test\n\n//export f\nfunc f() {}\n\n// +build ignore\nvar a = 1\n\n//line x.go:1\nvar b = 2\n\n// go: not a directive\nvar c = 3\n")
	for _, want := range []string{"//export f", "// +build ignore", "//line x.go:1"} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q in:\n%s", want, src)
		}
	}
	if strings.Contains(src, "not a directive") {
		t.Errorf("non-directive kept:\n%s", src)
	}
}

func lineCol(src, needle string) (int, int) {
	i := strings.Index(src, needle)
	before := src[:i]
	return strings.Count(before, "\n") + 1, i - strings.LastIndex(before, "\n")
}

func TestBlamedMutants(t *testing.T) {
	src := `package test

func f(a, b string, n int) (string, error) {
	if !__dgOn(1) {
		note()
	}
	if __dgOn(2) {
		return *new(string), *new(error)
	} else {
		return __dgAdd(3, a, b), nil
	}
}

func g(n int) int {
	if n > 0 {
		if !__dgOn(4) {
			return 1
		}
	}
	if !__dgOn(5) {
		panic("x")
	}
}

var top = 1
`
	at := func(needle string) []int {
		line, col := lineCol(src, needle)
		return blamedMutants([]byte(src), line, col)
	}
	if got := at("a, b), nil"); !reflect.DeepEqual(got, []int{3}) {
		t.Errorf("error in a helper call's argument: got %v, want [3]", got)
	}
	if got := at("*new(string)"); !reflect.DeepEqual(got, []int{2}) {
		t.Errorf("error in a zeroed return: got %v, want [2]", got)
	}
	// "missing return" is reported at the function's closing brace.
	closing := strings.Index(src, "}\n\nvar top")
	line := strings.Count(src[:closing], "\n") + 1
	if got := blamedMutants([]byte(src), line, 1); !reflect.DeepEqual(got, []int{4, 5}) {
		t.Errorf("unattributed error in g: got %v, want every guard in g [4 5]", got)
	}
	if got := at("top = 1"); got != nil {
		t.Errorf("error outside any function: got %v, want nil", got)
	}
	if got := blamedMutants([]byte(src), 999, 1); got != nil {
		t.Errorf("line out of range: got %v", got)
	}
	if got := blamedMutants([]byte(src), 0, 1); got != nil {
		t.Errorf("line zero: got %v", got)
	}
	if got := blamedMutants([]byte("not go"), 1, 1); got != nil {
		t.Errorf("unparseable source: got %v", got)
	}
}

func TestSchemataHelperSource(t *testing.T) {
	src := string(schemataHelperSource("widgets"))
	for _, want := range []string{"package widgets\n", `Getenv("DIFFGUARD_MUTANT")`, `Getenv("DIFFGUARD_TRACE")`, "func __dgOn(k int) bool"} {
		if !strings.Contains(src, want) {
			t.Errorf("helper source is missing %q", want)
		}
	}
}

func TestMutationFingerprint(t *testing.T) {
	root := writeModule(t, sampleSource, sampleTests)
	l := &Language{}
	print := func() string {
		t.Helper()
		p, err := l.MutationFingerprint(root, "p/p.go")
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	base := print()
	if base == "" || base != print() {
		t.Fatal("the fingerprint must be stable for unchanged code")
	}

	changes := map[string]string{
		"p/p_test.go":         sampleTests + "\n// changed\n",
		"p/testdata/in/a.txt": "fixture",
		"go.mod":              "module example.com/sample\n\ngo 1.25\n",
		"p/p.go":              sampleSource + "\n// changed\n",
	}
	seen := map[string]bool{base: true}
	for _, name := range []string{"p/p_test.go", "p/testdata/in/a.txt", "go.mod", "p/p.go"} {
		path := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(changes[name]), 0o644); err != nil {
			t.Fatal(err)
		}
		if p := print(); seen[p] {
			t.Errorf("changing %s did not change the fingerprint", name)
		} else {
			seen[p] = true
		}
	}

	// A file outside the package's build, such as an unrelated package,
	// leaves the fingerprint alone.
	before := print()
	os.MkdirAll(filepath.Join(root, "q"), 0o755)
	os.WriteFile(filepath.Join(root, "q/q.go"), []byte("package q\n"), 0o644)
	if print() != before {
		t.Error("an unrelated package changed the fingerprint")
	}

	if _, err := l.MutationFingerprint(root, "missing/x.go"); err == nil {
		t.Error("want an error for a package that cannot be listed")
	}
}

func TestRepoDirs(t *testing.T) {
	got := repoDirs("/repo", "/repo/b\n/repo/a\n/elsewhere/c\n/repo/b\n/repository/x\n/repo\n")
	if want := []string{"/repo", "/repo/a", "/repo/b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
