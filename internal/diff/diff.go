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

// Result holds all changed Go files parsed from a git diff.
type Result struct {
	BaseBranch string
	Files      []FileChange
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

// Parse runs git diff against the given base branch and parses changed Go files.
func Parse(repoPath, baseBranch string) (*Result, error) {
	mergeBaseCmd := exec.Command("git", "merge-base", baseBranch, "HEAD")
	mergeBaseCmd.Dir = repoPath
	mergeBaseOut, err := mergeBaseCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git merge-base failed: %w", err)
	}
	mergeBase := strings.TrimSpace(string(mergeBaseOut))

	cmd := exec.Command("git", "diff", "-U0", mergeBase, "--", "*.go")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff failed: %w", err)
	}

	files, err := parseUnifiedDiff(string(out))
	if err != nil {
		return nil, err
	}

	return &Result{
		BaseBranch: baseBranch,
		Files:      files,
	}, nil
}

// CollectPaths builds a Result by treating each .go file under the given
// paths as fully changed. Useful for refactoring mode where you want to
// analyze entire files rather than diffed regions only.
//
// paths may contain individual files or directories (walked recursively).
// Test files (_test.go) are excluded to match Parse's behavior.
func CollectPaths(repoPath string, paths []string) (*Result, error) {
	var files []FileChange
	seen := make(map[string]bool)

	for _, p := range paths {
		if err := collectPath(repoPath, p, &files, seen); err != nil {
			return nil, err
		}
	}

	return &Result{Files: files}, nil
}

// FilterPaths restricts an existing Result to files matching the given file or
// directory paths. Directory inputs match recursively.
func FilterPaths(repoPath string, r *Result, paths []string) (*Result, error) {
	scopes, err := compilePathScopes(repoPath, paths)
	if err != nil {
		return nil, err
	}

	filtered := &Result{BaseBranch: r.BaseBranch}
	for _, f := range r.Files {
		if matchesAnyScope(f.Path, scopes) {
			filtered.Files = append(filtered.Files, f)
		}
	}
	return filtered, nil
}

type pathScope struct {
	path  string
	isDir bool
}

func compilePathScopes(repoPath string, paths []string) ([]pathScope, error) {
	scopes := make([]pathScope, 0, len(paths))
	for _, raw := range paths {
		scope, err := compilePathScope(repoPath, raw)
		if err != nil {
			return nil, err
		}
		scopes = append(scopes, scope)
	}
	return scopes, nil
}

func compilePathScope(repoPath, raw string) (pathScope, error) {
	if raw == "" {
		return pathScope{}, fmt.Errorf("empty path filter")
	}

	isDir := strings.HasSuffix(raw, "/") || strings.HasSuffix(raw, string(filepath.Separator))
	normalized := raw
	if filepath.IsAbs(raw) {
		rel, err := filepath.Rel(repoPath, raw)
		if err != nil {
			return pathScope{}, err
		}
		normalized = rel
	}
	normalized = filepath.Clean(normalized)
	if !isRepoRelative(normalized) {
		return pathScope{}, fmt.Errorf("path %q is outside repo", raw)
	}

	if info, err := os.Stat(filepath.Join(repoPath, normalized)); err == nil && info.IsDir() {
		isDir = true
	}

	return pathScope{path: normalized, isDir: isDir}, nil
}

func isRepoRelative(path string) bool {
	if path == "." {
		return true
	}
	if path == ".." {
		return false
	}
	return !strings.HasPrefix(path, ".."+string(filepath.Separator))
}

func matchesAnyScope(path string, scopes []pathScope) bool {
	for _, scope := range scopes {
		if matchesScope(path, scope) {
			return true
		}
	}
	return false
}

func matchesScope(path string, scope pathScope) bool {
	if scope.path == "." {
		return true
	}
	if scope.isDir {
		return matchesDirScope(path, scope.path)
	}
	return path == scope.path
}

func matchesDirScope(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

func collectPath(repoPath, p string, files *[]FileChange, seen map[string]bool) error {
	absPath := p
	if !filepath.IsAbs(p) {
		absPath = filepath.Join(repoPath, p)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", p, err)
	}
	if info.IsDir() {
		return collectDir(repoPath, absPath, files, seen)
	}
	return addFile(repoPath, absPath, files, seen)
}

func collectDir(repoPath, absPath string, files *[]FileChange, seen map[string]bool) error {
	return filepath.WalkDir(absPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !isAnalyzableGoFile(path) {
			return nil
		}
		return addFile(repoPath, path, files, seen)
	})
}

func addFile(repoPath, absPath string, files *[]FileChange, seen map[string]bool) error {
	if !isAnalyzableGoFile(absPath) {
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

func isAnalyzableGoFile(path string) bool {
	return strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
}

// parseUnifiedDiff parses the output of git diff -U0 into FileChange entries.
func parseUnifiedDiff(diffOutput string) ([]FileChange, error) {
	var files []FileChange
	var current *FileChange

	scanner := bufio.NewScanner(strings.NewReader(diffOutput))
	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "+++ b/") {
			current = handleFileLine(line, &files)
			continue
		}

		if current != nil && strings.HasPrefix(line, "@@") {
			handleHunkLine(line, current)
		}
	}

	return files, scanner.Err()
}

func handleFileLine(line string, files *[]FileChange) *FileChange {
	path := strings.TrimPrefix(line, "+++ b/")
	if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
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
