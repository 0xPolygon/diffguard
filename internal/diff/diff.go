package diff

import (
	"bufio"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ChangedRegion represents a contiguous range of changed lines in a file.
type ChangedRegion struct {
	StartLine int
	EndLine   int
}

// FileChange represents changes to a single file in the diff.
type FileChange struct {
	Path    string
	Regions []ChangedRegion
}

// IsNew returns true if the entire file is new (single region from line 1).
func (fc FileChange) IsNew() bool {
	return len(fc.Regions) == 1 && fc.Regions[0].StartLine == 1
}

// ContainsLine returns true if the given line number falls within a changed region.
func (fc FileChange) ContainsLine(line int) bool {
	for _, r := range fc.Regions {
		if line >= r.StartLine && line <= r.EndLine {
			return true
		}
	}
	return false
}

// OverlapsRange returns true if any changed region overlaps [start, end].
func (fc FileChange) OverlapsRange(start, end int) bool {
	for _, r := range fc.Regions {
		if r.StartLine <= end && r.EndLine >= start {
			return true
		}
	}
	return false
}

// Result holds all changed source files parsed from a git diff.
type Result struct {
	BaseBranch string
	// MergeBase is the resolved commit SHA of merge-base(BaseBranch, HEAD).
	// Populated by Parse; empty in refactoring mode (CollectPaths). Used by
	// delta-gating analyzers to fetch pre-change file content via `git show`.
	MergeBase string
	Files     []FileChange
}

// ChangedPackages returns the unique set of package directories with changes.
func (r Result) ChangedPackages() []string {
	seen := make(map[string]bool)
	var pkgs []string
	for _, f := range r.Files {
		dir := filepath.Dir(f.Path)
		if !seen[dir] {
			seen[dir] = true
			pkgs = append(pkgs, dir)
		}
	}
	return pkgs
}

// FilesByPackage groups changed files by their package directory.
func (r Result) FilesByPackage() map[string][]FileChange {
	m := make(map[string][]FileChange)
	for _, f := range r.Files {
		dir := filepath.Dir(f.Path)
		m[dir] = append(m[dir], f)
	}
	return m
}

// Filter describes the subset of the diff the caller cares about. It is a
// narrower shape than lang.FileFilter so the diff package doesn't have to
// import lang (which would pull the full analyzer stack). Callers (usually
// cmd/diffguard) construct a Filter from their chosen language's
// lang.FileFilter and pass it here.
type Filter struct {
	// DiffGlobs is passed to `git diff -- <globs>` to restrict the raw diff
	// to language source files.
	DiffGlobs []string
	// Includes reports whether an analyzable source path (extension matches,
	// not a test file) belongs to the caller's language.
	Includes func(path string) bool
}

// includes returns true iff the filter accepts the path. An empty filter
// (Includes == nil) defaults to accepting every path — but production
// callers always supply one.
func (f Filter) includes(path string) bool {
	if f.Includes == nil {
		return true
	}
	return f.Includes(path)
}

// Parse runs `git diff` against the merge-base of baseBranch..HEAD and
// returns the changed files that pass the filter. The filter is also used to
// restrict the raw `git diff` output via -- globs so the parser never has to
// see files from other languages.
func Parse(repoPath, baseBranch string, filter Filter) (*Result, error) {
	mergeBaseCmd := exec.Command("git", "merge-base", baseBranch, "HEAD")
	mergeBaseCmd.Dir = repoPath
	mergeBaseOut, err := mergeBaseCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git merge-base failed: %w", err)
	}
	mergeBase := strings.TrimSpace(string(mergeBaseOut))

	args := []string{"diff", "--src-prefix=a/", "--dst-prefix=b/", "-U0", mergeBase}
	if len(filter.DiffGlobs) > 0 {
		args = append(args, "--")
		args = append(args, filter.DiffGlobs...)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff failed: %w", err)
	}

	files, err := parseUnifiedDiff(string(out), filter)
	if err != nil {
		return nil, err
	}

	return &Result{
		BaseBranch: baseBranch,
		MergeBase:  mergeBase,
		Files:      files,
	}, nil
}

// ShowAtRef returns the bytes of repoRelPath at the given git ref, or
// (nil, nil) if the path didn't exist there. Other failures (bad ref, not a
// git repo) bubble up as errors. Used by delta-gating analyzers to compare a
// changed file's metric against its pre-change baseline.
//
// `git show ref:path` exits 128 for several distinct conditions: path absent
// in the tree, ref unknown, not a git repo. Only the first should turn into
// the "no baseline" signal — the others must surface as errors so a broken
// CI config doesn't silently weaken the gate. We disambiguate on stderr.
func ShowAtRef(repoPath, ref, repoRelPath string) ([]byte, error) {
	cmd := exec.Command("git", "show", ref+":"+repoRelPath)
	cmd.Dir = repoPath
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil {
		return out, nil
	}
	if strings.Contains(stderr.String(), "does not exist in") {
		return nil, nil
	}
	return nil, fmt.Errorf("git show %s:%s: %w (%s)", ref, repoRelPath, err, strings.TrimSpace(stderr.String()))
}

// CollectPaths builds a Result by treating each analyzable source file under
// the given paths as fully changed. Useful for refactoring mode where you
// want to analyze entire files rather than diffed regions only.
//
// paths may contain individual files or directories (walked recursively).
// Files that fail filter.Includes are excluded — test files and non-source
// files never show up in the result.
func CollectPaths(repoPath string, paths []string, filter Filter) (*Result, error) {
	var files []FileChange
	seen := make(map[string]bool)

	for _, p := range paths {
		if err := collectPath(repoPath, p, filter, &files, seen); err != nil {
			return nil, err
		}
	}

	return &Result{Files: files}, nil
}

func collectPath(repoPath, p string, filter Filter, files *[]FileChange, seen map[string]bool) error {
	absPath := p
	if !filepath.IsAbs(p) {
		absPath = filepath.Join(repoPath, p)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", p, err)
	}
	if info.IsDir() {
		return collectDir(repoPath, absPath, filter, files, seen)
	}
	return addFile(repoPath, absPath, filter, files, seen)
}

func collectDir(repoPath, absPath string, filter Filter, files *[]FileChange, seen map[string]bool) error {
	return filepath.WalkDir(absPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// mutator-disable-next-line
		if d.IsDir() {
			return nil
		}
		return addFile(repoPath, path, filter, files, seen)
	})
}

func addFile(repoPath, absPath string, filter Filter, files *[]FileChange, seen map[string]bool) error {
	if !filter.includes(absPath) {
		return nil
	}
	rel, err := filepath.Rel(repoPath, absPath)
	if err != nil {
		return err
	}
	if seen[rel] {
		return nil
	}
	seen[rel] = true
	*files = append(*files, FileChange{
		Path:    rel,
		Regions: []ChangedRegion{{StartLine: 1, EndLine: math.MaxInt32}},
	})
	return nil
}

// parseUnifiedDiff parses the output of git diff -U0 into FileChange entries,
// dropping files that don't match filter.Includes.
func parseUnifiedDiff(diffOutput string, filter Filter) ([]FileChange, error) {
	var files []FileChange
	var current *FileChange

	scanner := bufio.NewScanner(strings.NewReader(diffOutput))
	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "+++ b/") {
			current = handleFileLine(line, filter, &files)
			continue
		}

		if current != nil && strings.HasPrefix(line, "@@") {
			handleHunkLine(line, current)
		}
	}

	return files, scanner.Err()
}

func handleFileLine(line string, filter Filter, files *[]FileChange) *FileChange {
	path := strings.TrimPrefix(line, "+++ b/")
	if !filter.includes(path) {
		return nil
	}
	*files = append(*files, FileChange{Path: path})
	return &(*files)[len(*files)-1]
}

func handleHunkLine(line string, current *FileChange) {
	region, err := parseHunkHeader(line)
	if err != nil || region == nil {
		return
	}
	current.Regions = append(current.Regions, *region)
}

// parseHunkHeader extracts the new file range from a unified diff hunk header.
func parseHunkHeader(line string) (*ChangedRegion, error) {
	parts := strings.SplitN(line, "@@", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid hunk header: %s", line)
	}

	ranges := strings.TrimSpace(parts[1])
	for _, r := range strings.Fields(ranges) {
		if !strings.HasPrefix(r, "+") {
			continue
		}
		r = strings.TrimPrefix(r, "+")
		start, count, err := parseRange(r)
		if err != nil {
			return nil, err
		}
		if count == 0 {
			return nil, nil
		}
		return &ChangedRegion{
			StartLine: start,
			EndLine:   start + count - 1,
		}, nil
	}

	return nil, fmt.Errorf("no new range in hunk header: %s", line)
}

// parseRange parses "start,count" or "start" (count defaults to 1).
func parseRange(s string) (start, count int, err error) {
	parts := strings.SplitN(s, ",", 2)
	start, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	if len(parts) == 1 {
		return start, 1, nil
	}
	count, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, err
	}
	return start, count, nil
}
