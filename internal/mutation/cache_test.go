package mutation

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/0xPolygon/diffguard/internal/lang"
)

func cachedRun(t *testing.T, dir string, l *sessionLanguage, opts Options) []Mutant {
	t.Helper()
	opts.CacheDir = dir
	mutants := numbered(4, "negate_conditional")
	cache := openCache(opts, "/repo", l)
	cache.load(mutants)
	if err := runMutantsParallel("/repo", mutants, l, opts, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	cache.store(mutants)
	return mutants
}

func TestCache_ReusesDefinitiveOutcomesOnly(t *testing.T) {
	dir := t.TempDir()
	outcomes := byLine(lang.OutcomeKilled, lang.OutcomeSurvived, lang.OutcomeTimedOut, lang.OutcomeNotRun)
	first := &sessionLanguage{outcome: outcomes, fingerprint: "v1"}
	cachedRun(t, dir, first, Options{})
	if first.runs.Load() != 4 {
		t.Fatalf("first run tested %d mutants, want 4", first.runs.Load())
	}

	second := &sessionLanguage{outcome: outcomes, fingerprint: "v1"}
	mutants := cachedRun(t, dir, second, Options{})
	if second.runs.Load() != 2 {
		t.Errorf("second run tested %d mutants; want 2: a timeout and a not-run mutant must be retried", second.runs.Load())
	}
	if !mutants[0].Cached || !mutants[0].Killed || !mutants[1].Cached || mutants[1].Outcome != lang.OutcomeSurvived {
		t.Errorf("cached results not restored: %+v", mutants[:2])
	}
	if mutants[2].Cached || mutants[3].Cached {
		t.Error("non-definitive outcomes must not come from the cache")
	}
}

func TestCache_InvalidatedByFingerprintAndTestPattern(t *testing.T) {
	dir := t.TempDir()
	killed := func(lang.MutantSite) lang.MutantOutcome { return lang.OutcomeKilled }
	cachedRun(t, dir, &sessionLanguage{outcome: killed, fingerprint: "v1"}, Options{})

	changed := &sessionLanguage{outcome: killed, fingerprint: "v2"}
	cachedRun(t, dir, changed, Options{})
	if changed.runs.Load() != 4 {
		t.Errorf("a changed fingerprint must re-test everything; ran %d", changed.runs.Load())
	}

	scoped := &sessionLanguage{outcome: killed, fingerprint: "v1"}
	cachedRun(t, dir, scoped, Options{TestPattern: "TestOnlyThis"})
	if scoped.runs.Load() != 4 {
		t.Errorf("a different test pattern must not reuse results; ran %d", scoped.runs.Load())
	}
}

func TestCache_Disabled(t *testing.T) {
	if openCache(Options{}, "/repo", &sessionLanguage{}) != nil {
		t.Error("no cache dir means no cache")
	}
	if openCache(Options{CacheDir: t.TempDir()}, "/repo", &fakeLanguage{}) != nil {
		t.Error("a language without a fingerprint cannot be cached")
	}
	var c *resultCache
	c.load(nil)
	c.store(nil)
}

func TestCache_UnfingerprintablePackageIsNeverCached(t *testing.T) {
	dir := t.TempDir()
	killed := func(lang.MutantSite) lang.MutantOutcome { return lang.OutcomeKilled }
	for _, l := range []*sessionLanguage{
		{outcome: killed, fpErr: errors.New("go list failed")},
		{outcome: killed, fingerprint: ""},
	} {
		cachedRun(t, dir, l, Options{})
		again := &sessionLanguage{outcome: killed, fingerprint: l.fingerprint, fpErr: l.fpErr}
		cachedRun(t, dir, again, Options{})
		if again.runs.Load() != 4 {
			t.Errorf("ran %d, want 4: nothing may be cached without a fingerprint", again.runs.Load())
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("cache dir has %d files, want none", len(entries))
	}
}

func TestCache_KeyDistinguishesSites(t *testing.T) {
	a := Mutant{File: "a.go", Line: 1, Offset: 10, Operator: "op", Description: "d"}
	for _, b := range []Mutant{
		{File: "b.go", Line: 1, Offset: 10, Operator: "op", Description: "d"},
		{File: "a.go", Line: 2, Offset: 10, Operator: "op", Description: "d"},
		{File: "a.go", Line: 1, Offset: 11, Operator: "op", Description: "d"},
		{File: "a.go", Line: 1, Offset: 10, Operator: "other", Description: "d"},
		{File: "a.go", Line: 1, Offset: 10, Operator: "op", Description: "e"},
	} {
		if cacheKey(a) == cacheKey(b) {
			t.Errorf("key collision between %+v and %+v", a, b)
		}
	}
}

func TestCache_StoreMergesWithExistingFile(t *testing.T) {
	dir := t.TempDir()
	l := &sessionLanguage{fingerprint: "v1"}
	c := openCache(Options{CacheDir: filepath.Join(dir, "nested")}, "/repo", l)
	first := numbered(1, "negate_conditional")
	first[0].setResult(lang.MutantResult{Outcome: lang.OutcomeKilled})
	c.store(first)

	second := []Mutant{{File: "a.go", Line: 2, Operator: "negate_conditional"}}
	second[0].setResult(lang.MutantResult{Outcome: lang.OutcomeBuildFailed})
	c.store(second)

	both := numbered(2, "negate_conditional")
	openCache(Options{CacheDir: filepath.Join(dir, "nested")}, "/repo", l).load(both)
	if both[0].Outcome != lang.OutcomeKilled || both[1].Outcome != lang.OutcomeBuildFailed {
		t.Errorf("a later store must not drop earlier entries: %+v", both)
	}
}

func TestCacheable(t *testing.T) {
	for o, want := range map[lang.MutantOutcome]bool{
		lang.OutcomeKilled: true, lang.OutcomeSurvived: true, lang.OutcomeNotCovered: true,
		lang.OutcomeBuildFailed: true, lang.OutcomeNotApplied: true,
		lang.OutcomeTimedOut: false, lang.OutcomeNotRun: false, lang.OutcomeStoppedEarly: false, lang.OutcomeUnset: false,
	} {
		if cacheable(o) != want {
			t.Errorf("cacheable(%v) = %v, want %v", o, !want, want)
		}
	}
}
