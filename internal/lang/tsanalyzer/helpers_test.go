package tsanalyzer

import (
	"os"
	"path/filepath"
)

// writeFile is the shared test helper used across the tsanalyzer test
// files. Mirrors the rustanalyzer's helper (rustanalyzer/helpers_test.go)
// — defined once here rather than via a testutil package so each _test.go
// file stays self-contained in what it inspects.
//
// Returns an error (rather than swallowing silently) so tests that care
// about directory/write failures can t.Fatal on them — matching the
// rustanalyzer pattern.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
