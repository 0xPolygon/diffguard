package diff

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const generatedScanBytes = 8 * 1024

// IsGeneratedFile reports whether the file contains a standard generated-code
// marker near the top of the file, such as "Code generated ... DO NOT EDIT".
// Read errors return false so file selection stays conservative.
func IsGeneratedFile(repoPath, path string) bool {
	absPath := path
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(repoPath, filepath.FromSlash(path))
	}

	f, err := os.Open(absPath)
	// mutator-disable-next-line
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(&io.LimitedReader{R: f, N: generatedScanBytes})
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "Code generated") && strings.Contains(line, "DO NOT EDIT") {
			return true
		}
	}
	return false
}
