package rustanalyzer

import "os"

// writeFile is a tiny helper shared across the rustanalyzer test files.
// We define it here (rather than importing testutil) so each _test.go
// file can stay self-contained in what it inspects.
func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}
