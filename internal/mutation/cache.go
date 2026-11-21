package mutation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/0xPolygon/diffguard/internal/lang"
)

// resultCache persists mutant outcomes between runs. Entries are grouped by
// the language's package fingerprint, so any change that could alter an
// outcome in a package invalidates that package's entries and nothing else.
// A nil *resultCache is a valid, disabled cache.
type resultCache struct {
	dir      string
	repoPath string
	salt     string
	fp       lang.Fingerprinter
	files    map[string]string // package dir -> cache file path ("" = unavailable)
}

func openCache(opts Options, repoPath string, l lang.Language) *resultCache {
	fp, ok := l.(lang.Fingerprinter)
	if opts.CacheDir == "" || !ok {
		return nil
	}
	return &resultCache{
		dir:      opts.CacheDir,
		repoPath: repoPath,
		salt:     l.Name() + "\x00" + opts.TestPattern,
		fp:       fp,
		files:    map[string]string{},
	}
}

// load fills in the outcome of every mutant with a cached result.
func (c *resultCache) load(mutants []Mutant) {
	if c == nil {
		return
	}
	entries := map[string]map[string]string{}
	for i := range mutants {
		path := c.fileFor(mutants[i].File)
		if _, read := entries[path]; !read {
			entries[path] = readCacheFile(path)
		}
		o := lang.ParseMutantOutcome(entries[path][cacheKey(mutants[i])])
		if !cacheable(o) {
			continue
		}
		mutants[i].setResult(lang.MutantResult{Outcome: o})
		mutants[i].Cached = true
	}
}

// store writes every definitive outcome back, merged with what is on disk.
func (c *resultCache) store(mutants []Mutant) {
	if c == nil {
		return
	}
	entries := map[string]map[string]string{}
	for _, m := range mutants {
		path := c.fileFor(m.File)
		if path == "" || !cacheable(m.outcome()) || m.Outcome == lang.OutcomeUnset {
			continue
		}
		if entries[path] == nil {
			entries[path] = readCacheFile(path)
		}
		entries[path][cacheKey(m)] = m.Outcome.String()
	}
	for path, e := range entries {
		writeCacheFile(path, e)
	}
}

// fileFor returns the cache file for the package containing file, or "" if
// the package cannot be fingerprinted (in which case it is never cached).
func (c *resultCache) fileFor(file string) string {
	pkg := filepath.Dir(file)
	if path, ok := c.files[pkg]; ok {
		return path
	}
	path := ""
	if print, err := c.fp.MutationFingerprint(c.repoPath, file); err == nil && print != "" {
		sum := sha256.Sum256([]byte(c.salt + "\x00" + print))
		path = filepath.Join(c.dir, hex.EncodeToString(sum[:])+".json")
	}
	c.files[pkg] = path
	return path
}

func cacheKey(m Mutant) string {
	s := m.site()
	return fmt.Sprintf("%s|%d|%d|%s|%s", s.File, s.Line, s.Offset, s.Operator, s.Description)
}

// cacheable reports whether an outcome is a property of the code rather
// than of this run: timeouts and not-run mutants must be tried again.
func cacheable(o lang.MutantOutcome) bool {
	switch o {
	case lang.OutcomeKilled, lang.OutcomeSurvived, lang.OutcomeNotCovered,
		lang.OutcomeBuildFailed, lang.OutcomeNotApplied:
		return true
	}
	return false
}

func readCacheFile(path string) map[string]string {
	entries := map[string]string{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &entries)
	}
	return entries
}

func writeCacheFile(path string, entries map[string]string) {
	data, err := json.Marshal(entries)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}
