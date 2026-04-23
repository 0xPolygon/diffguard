package lang

import (
	"fmt"
	"sort"
	"sync"
)

// registry stores the set of languages that have self-registered via init().
// It is safe for concurrent use; registrations happen during package init so
// the lock is rarely contended in practice, but Get/All are called from the
// main goroutine while other init() calls may still be running when the
// diffguard binary is linked with many language plugins.
var (
	registryMu  sync.RWMutex
	registryMap = map[string]Language{}
)

// Register adds a Language to the global registry under its Name(). It
// panics on duplicate registration because registrations always happen from
// init() functions: a duplicate is a programming error in the build graph
// (two packages registering the same language) and should fail loudly before
// main() runs.
func Register(l Language) {
	if l == nil {
		panic("lang.Register: nil Language")
	}
	name := l.Name()
	if name == "" {
		panic("lang.Register: Language.Name() returned empty string")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registryMap[name]; exists {
		panic(fmt.Sprintf("lang.Register: language %q already registered", name))
	}
	registryMap[name] = l
}

// Get returns the language registered under the given name, or (nil, false)
// if no such language is registered.
func Get(name string) (Language, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	l, ok := registryMap[name]
	return l, ok
}

// All returns every registered language, sorted by Name(). Deterministic
// ordering keeps report sections stable across runs and hosts.
func All() []Language {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]Language, 0, len(registryMap))
	for _, l := range registryMap {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// unregisterForTest removes the named language from the registry. It is only
// useful from _test.go files that temporarily register fake languages; the
// production code path never unregisters.
//
// Tests use it by calling `lang.UnregisterForTest("x")` — declared here so
// test packages can access it without exporting an unhygienic symbol.
func unregisterForTest(name string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	delete(registryMap, name)
}

// UnregisterForTest is the exported entry point into unregisterForTest.
// Production code must never call it; it exists so unit tests can keep the
// registry clean after injecting a fake Language.
func UnregisterForTest(name string) {
	unregisterForTest(name)
}
