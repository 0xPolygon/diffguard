package goanalyzer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/0xPolygon/diffguard/internal/lang"
)

// testRunnerImpl implements lang.TestRunner for Go using `go test -overlay`.
// The overlay mechanism lets mutants run fully in parallel — the build
// system picks up the mutant file without touching the real source — so
// this runner is stateless and safe to call concurrently.
type testRunnerImpl struct{}

// RunTest writes a build-time overlay that redirects cfg.OriginalFile to
// cfg.MutantFile and invokes `go test` from the directory of the original
// file. A non-nil error from `go test` means at least one test failed —
// the mutant was killed.
//
// The returned (killed, output, err) triple matches the lang.TestRunner
// contract: err is the only error return for "the runner itself could not
// run" (e.g. couldn't write the overlay file); a normal test failure is
// reported via killed=true with the test output in `output`.
func (testRunnerImpl) RunTest(cfg lang.TestRunConfig) (bool, string, error) {
	overlayPath := filepath.Join(cfg.WorkDir, fmt.Sprintf("m%d-overlay.json", cfg.Index))
	if err := writeOverlayJSON(overlayPath, cfg.OriginalFile, cfg.MutantFile); err != nil {
		return false, "", err
	}

	pkgDir := filepath.Dir(cfg.OriginalFile)
	cmd := exec.Command("go", buildTestArgs(cfg, overlayPath)...)
	cmd.Dir = pkgDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()

	if err != nil {
		return true, stderr.String(), nil
	}
	return false, "", nil
}

// writeOverlayJSON writes a go build overlay file mapping originalPath to
// mutantPath. See `go help build` -overlay flag for format details.
func writeOverlayJSON(path, originalPath, mutantPath string) error {
	overlay := struct {
		Replace map[string]string `json:"Replace"`
	}{
		Replace: map[string]string{originalPath: mutantPath},
	}
	data, err := json.Marshal(overlay)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// buildTestArgs constructs the `go test` argv. The overlay argument is
// always present; -run is only added if the caller set TestPattern.
func buildTestArgs(cfg lang.TestRunConfig, overlayPath string) []string {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultGoTestTimeout
	}
	args := []string{"test", "-overlay=" + overlayPath, "-count=1", "-timeout", timeout.String()}
	if cfg.TestPattern != "" {
		args = append(args, "-run", cfg.TestPattern)
	}
	args = append(args, "./...")
	return args
}
