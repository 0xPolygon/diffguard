package goanalyzer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// fingerprintVersion is bumped whenever a change to the engine could give a
// mutant a different outcome on unchanged code.
const fingerprintVersion = "go-schemata-1"

// MutationFingerprint implements lang.Fingerprinter. The fingerprint covers
// everything in the repository that can change the outcome of a mutant in
// file's package: the toolchain, the module's dependency pins, and every
// file of every in-repo package that the package's tests (and the tests of
// the packages beneath it) are built from, including their testdata.
func (*Language) MutationFingerprint(repoPath, file string) (string, error) {
	dir := filepath.Join(repoPath, filepath.Dir(file))
	goCmd := func(args ...string) (string, error) {
		res := proc{dir: dir, timeout: 5 * time.Minute, name: "go", args: args, keep: 16 << 20}.run(context.Background())
		if res.err != nil {
			return "", fmt.Errorf("go %s: %w", args[0], res.err)
		}
		return res.output, nil
	}
	env, err := goCmd("env", "GOVERSION", "GOMOD", "GOOS", "GOARCH", "GOFLAGS")
	if err != nil {
		return "", err
	}
	deps, err := goCmd("list", "-test", "-deps", "-f", "{{if not .Standard}}{{.Dir}}{{end}}", "./...")
	if err != nil {
		return "", err
	}

	h := sha256.New()
	fmt.Fprintf(h, "%s\n%s\n", fingerprintVersion, env)
	if fields := strings.Fields(env); len(fields) > 1 {
		hashFile(h, fields[1])
		hashFile(h, strings.TrimSuffix(fields[1], ".mod")+".sum")
	}
	for _, d := range repoDirs(repoPath, deps) {
		if err := hashPackageDir(h, d); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// repoDirs returns the distinct directories of listOutput (one per line)
// that lie inside repoPath, sorted. Packages outside the repository are
// pinned by go.sum and need no hashing.
func repoDirs(repoPath, listOutput string) []string {
	seen := map[string]bool{}
	var dirs []string
	for _, d := range strings.Fields(listOutput) {
		rel, err := filepath.Rel(repoPath, d)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || seen[d] {
			continue
		}
		seen[d] = true
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	return dirs
}

// hashPackageDir hashes the files directly inside dir, plus everything
// under its testdata directory.
func hashPackageDir(h io.Writer, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if e.Type().IsRegular() {
			hashFile(h, path)
		}
		if e.IsDir() && e.Name() == "testdata" {
			hashTree(h, path)
		}
	}
	return nil
}

// hashTree hashes every regular file under root.
func hashTree(h io.Writer, root string) {
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			hashFile(h, path)
		}
		return nil
	})
}

// hashFile writes the file's path and content to h. An unreadable file
// contributes only its path.
func hashFile(h io.Writer, path string) {
	fmt.Fprintf(h, "%s\x00", path)
	if f, err := os.Open(path); err == nil {
		n, _ := io.Copy(h, f)
		f.Close()
		fmt.Fprintf(h, "\x00%d\x00", n)
	}
}
