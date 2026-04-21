package diff

import "strings"

// goFilter returns a minimal Filter matching the old hardcoded Go behavior:
// includes any path ending in .go except _test.go. Used by the in-package
// tests so they exercise the filter parameter without pulling in the
// goanalyzer package (which would create a test-time import cycle).
func goFilter() Filter {
	return Filter{
		DiffGlobs: []string{"*.go"},
		Includes: func(path string) bool {
			return strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
		},
	}
}
