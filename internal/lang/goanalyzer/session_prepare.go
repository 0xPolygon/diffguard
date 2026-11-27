package goanalyzer

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/0xPolygon/diffguard/internal/lang"
)

// maxRepairs bounds the build-drop-rebuild loop of a shared build.
const maxRepairs = 6

// compileError matches a Go compiler diagnostic: path:line:col: message.
var compileError = regexp.MustCompile(`(?m)^(\S+\.go):(\d+):(\d+): `)

// prepare builds the package's shared test binaries and traces its tests.
// On any failure it leaves embedded empty, so the package's mutants take
// the overlay path instead.
func (p *mutantPackage) prepare(ctx context.Context) {
	if err := os.MkdirAll(p.work, 0o755); err != nil {
		return
	}
	in, ok := p.buildSchemata(ctx)
	if !ok {
		p.fallback(ctx, "its mutants cannot share one build")
		return
	}
	bins, ok := p.buildTestBinaries(ctx, in.overlayPath)
	if !ok {
		p.fallback(ctx, "its instrumented tests do not build")
		return
	}
	if !p.baselineAll(ctx, bins) {
		p.fallback(ctx, "its instrumented tests do not pass")
		return
	}
	p.embedded, p.bins = in.embedded, bins
	for _, b := range bins {
		p.leftOut = append(p.leftOut, b.leftOut...)
	}
}

func (p *mutantPackage) fallback(ctx context.Context, why string) {
	if ctx.Err() == nil {
		p.session.logf("%s: %s; testing its %d mutants one build at a time", p.dir, why, len(p.sites))
	}
}

// buildSchemata instruments the package's mutated files and compiles the
// package, dropping the mutants the compiler rejects until it builds.
func (p *mutantPackage) buildSchemata(ctx context.Context) (*instrumentation, bool) {
	in := newInstrumentation(p)
	for range maxRepairs {
		if err := in.write(); err != nil {
			return nil, false
		}
		release := p.session.acquire()
		res := p.goCmd(ctx, baselineTimeout(p.session.cfg), "build", "-overlay="+in.overlayPath, "-gcflags=-e", "-o", os.DevNull, ".")
		release()
		if res.err == nil {
			return in, true
		}
		if !in.dropBlamed(res.output) {
			break
		}
	}
	return nil, false
}

// instrumentation is the schemata build of one package: the instrumented
// copy of each mutated file, wired in through a build overlay.
type instrumentation struct {
	pkg         *mutantPackage
	overlayPath string
	files       []string         // mutated source files, sorted
	sitesOf     map[string][]int // source file -> site indexes
	sources     map[string][]byte
	excluded    map[int]bool
	embedded    map[int]bool
}

func newInstrumentation(p *mutantPackage) *instrumentation {
	in := &instrumentation{
		pkg:         p,
		overlayPath: filepath.Join(p.work, "overlay.json"),
		sitesOf:     map[string][]int{},
		excluded:    map[int]bool{},
	}
	for _, idx := range p.sites {
		file := filepath.Join(p.session.cfg.RepoPath, p.session.sites[idx].File)
		in.sitesOf[file] = append(in.sitesOf[file], idx)
	}
	in.files = slices.Sorted(maps.Keys(in.sitesOf))
	return in
}

// write instruments every file with its non-excluded sites and writes the
// overlay that swaps the results, plus the helper file, into the package.
func (in *instrumentation) write() error {
	replace := map[string]string{}
	in.sources = map[string][]byte{}
	in.embedded = map[int]bool{}
	pkgName := ""
	for n, file := range in.files {
		out := filepath.Join(in.pkg.work, fmt.Sprintf("f%d.go", n))
		name, err := in.instrument(file, out)
		if err != nil {
			return err
		}
		replace[file], pkgName = out, name
	}
	helper := filepath.Join(in.pkg.work, schemataFileName)
	if err := os.WriteFile(helper, schemataHelperSource(pkgName), 0o644); err != nil {
		return err
	}
	replace[filepath.Join(in.pkg.dir, schemataFileName)] = helper
	return writeOverlayMap(in.overlayPath, replace)
}

// instrument writes the instrumented copy of file to out, records which of
// its sites were embedded, and returns the file's package name.
func (in *instrumentation) instrument(file, out string) (string, error) {
	var sites []lang.MutantSite
	var ids []int
	for _, idx := range in.sitesOf[file] {
		if !in.excluded[idx] {
			sites = append(sites, in.pkg.session.sites[idx])
			ids = append(ids, idx+1)
		}
	}
	src, embedded, pkgName, err := instrumentFile(file, sites, ids)
	if err != nil {
		return "", err
	}
	for i, ok := range embedded {
		in.embedded[ids[i]-1] = ok
	}
	in.sources[out] = src
	return pkgName, os.WriteFile(out, src, 0o644)
}

// dropBlamed excludes the mutants responsible for the compile errors in
// output and reports whether it excluded any new ones.
func (in *instrumentation) dropBlamed(output string) bool {
	before := len(in.excluded)
	for _, m := range compileError.FindAllStringSubmatch(output, -1) {
		path := m[1]
		if !filepath.IsAbs(path) {
			path = filepath.Join(in.pkg.dir, path)
		}
		line, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		// A path that is not an instrumented file has no source here, and
		// blames nothing.
		for _, id := range blamedMutants(in.sources[filepath.Clean(path)], line, col) {
			in.excluded[id-1] = true
		}
	}
	return len(in.excluded) > before
}

// baselineAll runs every binary's unmutated baseline concurrently and
// reports whether all of them passed.
func (p *mutantPackage) baselineAll(ctx context.Context, bins []*testBinary) bool {
	passed := make([]bool, len(bins))
	var wg sync.WaitGroup
	for i, b := range bins {
		wg.Add(1)
		go func() {
			defer wg.Done()
			passed[i] = b.baseline(ctx, p.session)
		}()
	}
	wg.Wait()
	return !slices.Contains(passed, false)
}

// buildTestBinaries compiles, concurrently, one instrumented test binary
// for the package and for each package beneath it whose tests depend on it.
// It fails only if the package's own tests do not build.
func (p *mutantPackage) buildTestBinaries(ctx context.Context, overlay string) ([]*testBinary, bool) {
	pkgs := p.testPackages(ctx)
	built := make([]*testBinary, len(pkgs))
	failed := make([]bool, len(pkgs))
	var wg sync.WaitGroup
	for n, tp := range pkgs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			built[n], failed[n] = p.buildTestBinary(ctx, overlay, tp, n)
		}()
	}
	wg.Wait()

	var bins []*testBinary
	for n, tp := range pkgs {
		if failed[n] && tp.own {
			return nil, false
		}
		if built[n] != nil {
			bins = append(bins, built[n])
		}
	}
	return bins, true
}

// buildTestBinary compiles tp's test binary. It returns nil without failure
// for a package that has no test files, for which go test -c writes nothing.
func (p *mutantPackage) buildTestBinary(ctx context.Context, overlay string, tp testPackage, n int) (bin *testBinary, failed bool) {
	// The .exe suffix is required on Windows and harmless elsewhere.
	path := filepath.Join(p.work, fmt.Sprintf("t%d.test.exe", n))
	defer p.session.acquire()()
	res := proc{dir: tp.dir, timeout: baselineTimeout(p.session.cfg), name: "go",
		args: []string{"test", "-c", "-overlay=" + overlay, "-o", path, "."}}.run(ctx)
	if res.err != nil {
		return nil, true
	}
	if _, err := os.Stat(path); err != nil {
		return nil, false
	}
	return &testBinary{path: path, dir: tp.dir}, false
}

// testPackage is a package whose tests can observe the mutated package.
type testPackage struct {
	dir string
	own bool // the mutated package itself
}

// testPackages returns the mutated package and the packages beneath it
// whose test binaries link it. If they cannot be listed, the mutated
// package alone is used.
func (p *mutantPackage) testPackages(ctx context.Context) []testPackage {
	own := []testPackage{{dir: p.dir, own: true}}
	self := p.goCmd(ctx, time.Minute, "list", "-f", "{{.ImportPath}}", ".")
	if self.err != nil {
		return own
	}
	list := p.goCmd(ctx, 5*time.Minute, "list", "-test", "-f", `{{.ImportPath}}|{{.Dir}}|{{join .Deps ","}}`, "./...")
	if list.err != nil {
		return own
	}
	return parseTestPackages(strings.TrimSpace(self.output), list.output)
}

// parseTestPackages picks, from `go list -test` output, the test binaries
// that link importPath. The mutated package's own tests come first.
func parseTestPackages(importPath, listOutput string) []testPackage {
	var own, others []testPackage
	for _, line := range strings.Split(listOutput, "\n") {
		fields := strings.SplitN(line, "|", 3)
		if len(fields) != 3 || !strings.HasSuffix(fields[0], ".test") {
			continue
		}
		switch {
		case fields[0] == importPath+".test":
			own = append(own, testPackage{dir: fields[1], own: true})
		case dependsOn(fields[2], importPath):
			others = append(others, testPackage{dir: fields[1]})
		}
	}
	return append(own, others...)
}

// dependsOn reports whether a comma-separated dependency list names
// importPath, either plainly or as a variant recompiled for a test
// ("path [other.test]").
func dependsOn(deps, importPath string) bool {
	for _, dep := range strings.Split(deps, ",") {
		if dep == importPath || strings.HasPrefix(dep, importPath+" [") {
			return true
		}
	}
	return false
}

// baseline runs the binary's tests unmutated, recording which mutants each
// reaches. It prefers one run per test, which lets a mutant be tested by
// only the tests that reach it. If some tests fail on their own, a single
// run of the whole suite is tried (tests that depend on each other); if
// that fails too, the failing tests are left out and the rest are used.
// It reports false if no test passes.
func (b *testBinary) baseline(ctx context.Context, s *goSession) bool {
	names, listed := b.listTests(ctx, s.cfg)
	passing, failing := b.traceEach(ctx, s, names)
	if listed && failing == nil {
		b.tests = passing
		return true
	}
	release := s.acquire()
	whole, ok := b.trace(ctx, s.cfg, "")
	release()
	if ok {
		b.whole = &whole
		return true
	}
	if passing == nil || ctx.Err() != nil {
		return false
	}
	// The failed suite run counts too: a test that needs another's setup
	// reaches code there that it never gets to on its own.
	b.tests, b.broken = passing, whole.reach
	for _, t := range failing {
		b.leftOut = append(b.leftOut, t.name)
		maps.Copy(b.broken, t.reach)
	}
	s.logf("%s: %d test(s) fail without any mutation and were left out: %s", b.dir, len(b.leftOut), strings.Join(b.leftOut, ", "))
	return true
}

// testName matches the top-level tests a binary lists that -test.run can
// select. Benchmarks are listed too but never run.
var testName = regexp.MustCompile(`^(Test|Example|Fuzz)\S*$`)

func (b *testBinary) listTests(ctx context.Context, cfg lang.SessionConfig) ([]string, bool) {
	pattern := cfg.TestPattern
	if pattern == "" {
		pattern = ".*"
	}
	res := proc{dir: b.dir, timeout: baselineTimeout(cfg), name: b.path, args: []string{"-test.list", pattern}, keep: 1 << 20}.run(ctx)
	if res.err != nil {
		return nil, false
	}
	var names []string
	for _, line := range strings.Split(res.output, "\n") {
		if testName.MatchString(line) {
			names = append(names, line)
		}
	}
	return names, true
}

// traceEach traces every test in its own process, a second time if it
// fails, and returns the tests that passed and those that did not.
func (b *testBinary) traceEach(ctx context.Context, s *goSession, names []string) (passing, failing []tracedTest) {
	tests := make([]tracedTest, len(names))
	passed := make([]bool, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		release := s.acquire()
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer release()
			tests[i], passed[i] = b.traceTwice(ctx, s.cfg, name)
		}()
	}
	wg.Wait()
	for i, t := range tests {
		if passed[i] {
			passing = append(passing, t)
		} else {
			failing = append(failing, t)
		}
	}
	return passing, failing
}

// traceTwice traces one test, and once more if it fails. A test that fails
// both times keeps what both runs reached.
func (b *testBinary) traceTwice(ctx context.Context, cfg lang.SessionConfig, name string) (tracedTest, bool) {
	first, ok := b.trace(ctx, cfg, name)
	if ok {
		return first, true
	}
	second, ok := b.trace(ctx, cfg, name)
	if !ok {
		maps.Copy(second.reach, first.reach)
	}
	return second, ok
}

// trace runs one test (or, with an empty name, the whole suite) unmutated
// and returns what it reached, which is recorded even if the run fails.
func (b *testBinary) trace(ctx context.Context, cfg lang.SessionConfig, name string) (tracedTest, bool) {
	traceFile := b.path + "." + name + ".trace"
	os.Remove(traceFile)
	var names []string
	if name != "" {
		names = []string{name}
	}
	res := proc{
		dir:     b.dir,
		env:     []string{traceEnv + "=" + traceFile},
		timeout: baselineTimeout(cfg),
		name:    b.path,
		args:    append([]string{"-test.count=1"}, runFilter(names, cfg.TestPattern)...),
	}.run(ctx)
	return tracedTest{name: name, duration: res.duration, reach: readTrace(traceFile)}, res.err == nil
}

// readTrace returns the mutant ids recorded in a trace file. A missing file
// means the run reached no mutation point.
func readTrace(path string) map[int]bool {
	reach := map[int]bool{}
	data, _ := os.ReadFile(path)
	for _, line := range strings.Fields(string(data)) {
		if id, err := strconv.Atoi(line); err == nil {
			reach[id] = true
		}
	}
	return reach
}
