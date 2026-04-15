package diff

import (
	"bufio"
	"fmt"
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
