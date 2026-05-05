// Package baseline provides helpers for "delta gating": running an analyzer
// against the pre-change version of a file (via `git show <merge-base>:<path>`)
// so that callers can drop findings whose underlying metric did not get worse
// in the diff.
//
// The package keeps language analyzers stateless and unaware of base refs:
// the file content is fetched, written to a temp file preserving the original
// extension, and the existing AnalyzeFile / ExtractFunctions methods run on
// it with a synthetic full-coverage FileChange.
package baseline

import (
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/0xPolygon/diffguard/internal/diff"
)

// FullCoverage returns a FileChange whose single region spans the entire file,
// so per-language overlap filters include every function.
func FullCoverage(repoRelPath string) diff.FileChange {
	return diff.FileChange{
		Path:    repoRelPath,
		Regions: []diff.ChangedRegion{{StartLine: 1, EndLine: math.MaxInt32}},
	}
}

// FetchToTemp fetches repoRelPath at ref and writes it to a temp file whose
// name preserves the original extension (some analyzers branch on path
// extension). Returns ("", nil) if the file did not exist at the base ref.
// Caller is responsible for os.Remove(path).
func FetchToTemp(repoPath, ref, repoRelPath string) (string, error) {
	content, err := diff.ShowAtRef(repoPath, ref, repoRelPath)
	if err != nil {
		return "", err
	}
	if content == nil {
		return "", nil
	}
	ext := filepath.Ext(repoRelPath)
	tmp, err := os.CreateTemp("", "diffguard-base-*"+ext)
	if err != nil {
		return "", fmt.Errorf("temp file: %w", err)
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", fmt.Errorf("write base content: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}
