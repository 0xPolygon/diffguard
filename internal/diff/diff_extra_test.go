package diff

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCollectPaths_SingleFile(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "foo.go")
	os.WriteFile(fp, []byte("package x\n\nfunc f() {}\n"), 0644)

	r, err := CollectPaths(dir, []string{"foo.go"}, goFilter())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(r.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(r.Files))
	}
	if r.Files[0].Path != "foo.go" {
		t.Errorf("path = %q, want foo.go", r.Files[0].Path)
	}
	if len(r.Files[0].Regions) != 1 {
		t.Fatalf("expected 1 region, got %d", len(r.Files[0].Regions))
	}
	if r.Files[0].Regions[0].StartLine != 1 {
		t.Errorf("StartLine = %d, want 1", r.Files[0].Regions[0].StartLine)
	}
	// EndLine should be huge so any function in the file is "in range"
	if r.Files[0].Regions[0].EndLine < 1<<20 {
		t.Errorf("EndLine = %d, want very large", r.Files[0].Regions[0].EndLine)
	}
}

func TestCollectPaths_SkipsTestFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "foo.go"), []byte("package x\n"), 0644)
	os.WriteFile(filepath.Join(dir, "foo_test.go"), []byte("package x\n"), 0644)

	r, err := CollectPaths(dir, []string{"foo_test.go"}, goFilter())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(r.Files) != 0 {
		t.Errorf("expected 0 files (test files skipped), got %d", len(r.Files))
	}
}

func TestCollectPaths_Directory(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package x\n"), 0644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte("package x\n"), 0644)
	os.WriteFile(filepath.Join(dir, "a_test.go"), []byte("package x\n"), 0644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("readme\n"), 0644)
	os.WriteFile(filepath.Join(dir, "sub", "c.go"), []byte("package x\n"), 0644)

	r, err := CollectPaths(dir, []string{"."}, goFilter())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// Should have a.go, b.go, sub/c.go (3 files); skip _test.go and README.md
	if len(r.Files) != 3 {
		t.Errorf("expected 3 files, got %d: %v", len(r.Files), filenames(r.Files))
	}
}

func TestCollectPaths_NonexistentPath(t *testing.T) {
	dir := t.TempDir()
	_, err := CollectPaths(dir, []string{"nonexistent.go"}, goFilter())
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestCollectPaths_MultiplePaths(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "pkg1"), 0755)
	os.MkdirAll(filepath.Join(dir, "pkg2"), 0755)
	os.WriteFile(filepath.Join(dir, "pkg1", "a.go"), []byte("package pkg1\n"), 0644)
	os.WriteFile(filepath.Join(dir, "pkg2", "b.go"), []byte("package pkg2\n"), 0644)

	r, err := CollectPaths(dir, []string{"pkg1", "pkg2"}, goFilter())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(r.Files) != 2 {
		t.Errorf("expected 2 files, got %d", len(r.Files))
	}
}

func TestCollectPaths_Deduplicates(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package x\n"), 0644)

	// Pass the same file via both file path and dir
	r, err := CollectPaths(dir, []string{"a.go", "."}, goFilter())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(r.Files) != 1 {
		t.Errorf("expected 1 unique file, got %d", len(r.Files))
	}
}

func TestCollectPaths_SkipsNonGoFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("notes"), 0644)

	r, err := CollectPaths(dir, []string{"notes.txt"}, goFilter())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(r.Files) != 0 {
		t.Errorf("expected 0 files for non-Go file, got %d", len(r.Files))
	}
}

// TestFilter_IncludesGoFile exercises the path the diff parser takes when
// deciding whether to admit a file from `git diff` output. The old
// hardcoded isAnalyzableGoFile function is gone; the same semantic check
// now lives in the caller-supplied Filter.Includes, and this test locks in
// that Filter.includes() routes through it correctly.
func TestFilter_IncludesGoFile(t *testing.T) {
	filter := goFilter()
	tests := []struct {
		path string
		want bool
	}{
		{"foo.go", true},
		{"foo_test.go", false},
		{"foo.txt", false},
		{"path/to/foo.go", true},
		{"path/to/foo_test.go", false},
	}
	for _, tt := range tests {
		if got := filter.includes(tt.path); got != tt.want {
			t.Errorf("filter.includes(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func filenames(files []FileChange) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Path
	}
	return out
}

func TestHandleFileLine_GoFile(t *testing.T) {
	var files []FileChange
	result := handleFileLine("+++ b/pkg/handler.go", goFilter(), &files)
	if result == nil {
		t.Fatal("expected non-nil result for .go file")
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Path != "pkg/handler.go" {
		t.Errorf("path = %q, want pkg/handler.go", files[0].Path)
	}
}

func TestHandleFileLine_TestFile(t *testing.T) {
	var files []FileChange
	result := handleFileLine("+++ b/pkg/handler_test.go", goFilter(), &files)
	if result != nil {
		t.Error("expected nil for test file")
	}
	if len(files) != 0 {
		t.Error("test file should not be added")
	}
}

func TestHandleFileLine_NonGoFile(t *testing.T) {
	var files []FileChange
	result := handleFileLine("+++ b/README.md", goFilter(), &files)
	if result != nil {
		t.Error("expected nil for non-Go file")
	}
	if len(files) != 0 {
		t.Error("non-Go file should not be added")
	}
}

func TestHandleHunkLine_Valid(t *testing.T) {
	fc := &FileChange{Path: "test.go"}
	handleHunkLine("@@ -10,3 +15,5 @@ func foo", fc)
	if len(fc.Regions) != 1 {
		t.Fatalf("expected 1 region, got %d", len(fc.Regions))
	}
	if fc.Regions[0].StartLine != 15 || fc.Regions[0].EndLine != 19 {
		t.Errorf("region = %+v, want {15, 19}", fc.Regions[0])
	}
}

func TestHandleHunkLine_Invalid(t *testing.T) {
	fc := &FileChange{Path: "test.go"}
	handleHunkLine("not a hunk header", fc)
	if len(fc.Regions) != 0 {
		t.Error("invalid hunk should not add regions")
	}
}

func TestHandleHunkLine_PureDeletion(t *testing.T) {
	fc := &FileChange{Path: "test.go"}
	handleHunkLine("@@ -10,5 +10,0 @@ func foo", fc)
	if len(fc.Regions) != 0 {
		t.Error("pure deletion should not add regions")
	}
}

func TestFileChange_IsNew(t *testing.T) {
	newFile := FileChange{
		Path:    "new.go",
		Regions: []ChangedRegion{{StartLine: 1, EndLine: 50}},
	}
	if !newFile.IsNew() {
		t.Error("expected IsNew() = true for single region from line 1")
	}

	modifiedFile := FileChange{
		Path:    "mod.go",
		Regions: []ChangedRegion{{StartLine: 10, EndLine: 20}},
	}
	if modifiedFile.IsNew() {
		t.Error("expected IsNew() = false for region not starting at line 1")
	}

	multiRegion := FileChange{
		Path: "multi.go",
		Regions: []ChangedRegion{
			{StartLine: 1, EndLine: 10},
			{StartLine: 20, EndLine: 30},
		},
	}
	if multiRegion.IsNew() {
		t.Error("expected IsNew() = false for multiple regions")
	}
}

func TestResult_FilesByPackage(t *testing.T) {
	r := Result{
		Files: []FileChange{
			{Path: "pkg/handler/routes.go"},
			{Path: "pkg/handler/middleware.go"},
			{Path: "pkg/auth/token.go"},
		},
	}

	byPkg := r.FilesByPackage()
	if len(byPkg) != 2 {
		t.Fatalf("expected 2 packages, got %d", len(byPkg))
	}
	if len(byPkg["pkg/handler"]) != 2 {
		t.Errorf("pkg/handler has %d files, want 2", len(byPkg["pkg/handler"]))
	}
	if len(byPkg["pkg/auth"]) != 1 {
		t.Errorf("pkg/auth has %d files, want 1", len(byPkg["pkg/auth"]))
	}
}

func TestParseUnifiedDiff_NonGoFile(t *testing.T) {
	input := `diff --git a/README.md b/README.md
--- a/README.md
+++ b/README.md
@@ -1,0 +1,5 @@
+new content
`
	files, err := parseUnifiedDiff(input, goFilter())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files for non-Go file, got %d", len(files))
	}
}

func TestParseUnifiedDiff_EmptyInput(t *testing.T) {
	files, err := parseUnifiedDiff("", goFilter())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files for empty input, got %d", len(files))
	}
}

func TestParseRange(t *testing.T) {
	start, count, err := parseRange("15,5")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if start != 15 || count != 5 {
		t.Errorf("got start=%d count=%d, want 15,5", start, count)
	}

	start, count, err = parseRange("42")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if start != 42 || count != 1 {
		t.Errorf("got start=%d count=%d, want 42,1", start, count)
	}
}

func TestContainsLine_Empty(t *testing.T) {
	fc := FileChange{Path: "test.go"}
	if fc.ContainsLine(1) {
		t.Error("empty regions should not contain any line")
	}
}

func TestOverlapsRange_Empty(t *testing.T) {
	fc := FileChange{Path: "test.go"}
	if fc.OverlapsRange(1, 100) {
		t.Error("empty regions should not overlap anything")
	}
}

// TestParseHunkHeader_InvalidFormat exercises the error-return path when the
// hunk header doesn't have the expected @@ ... @@ framing.
func TestParseHunkHeader_InvalidFormat(t *testing.T) {
	_, err := parseHunkHeader("not a hunk header")
	if err == nil {
		t.Error("expected error for malformed hunk header")
	}
}

// TestParseHunkHeader_NoPlusRange exercises the fallback error-return when
// the header has @@ markers but no + range.
func TestParseHunkHeader_NoPlusRange(t *testing.T) {
	_, err := parseHunkHeader("@@ -10,3 @@ context only")
	if err == nil {
		t.Error("expected error when + range is missing")
	}
}

// TestParseHunkHeader_NonNumericRange exercises the wrapped error from
// parseRange when the + range contains non-integers.
func TestParseHunkHeader_NonNumericRange(t *testing.T) {
	_, err := parseHunkHeader("@@ -10,3 +abc,5 @@")
	if err == nil {
		t.Error("expected error for non-numeric start")
	}

	_, err = parseHunkHeader("@@ -10,3 +15,xyz @@")
	if err == nil {
		t.Error("expected error for non-numeric count")
	}
}

// TestParseUnifiedDiff_TestFileFollowedByHunk ensures the `current != nil`
// guard prevents processing hunks that come right after a filtered-out
// (test-file) entry. Without the guard we'd dereference nil and panic.
func TestParseUnifiedDiff_TestFileFollowedByHunk(t *testing.T) {
	input := `diff --git a/a_test.go b/a_test.go
--- a/a_test.go
+++ b/a_test.go
@@ -1,0 +1,5 @@
+test content
diff --git a/b.go b/b.go
--- a/b.go
+++ b/b.go
@@ -10,0 +11,3 @@
+new code
`
	files, err := parseUnifiedDiff(input, goFilter())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file (b.go), got %d: %v", len(files), filenames(files))
	}
	if files[0].Path != "b.go" {
		t.Errorf("path = %q, want b.go", files[0].Path)
	}
	// b.go's hunk should still be recorded even though a test file preceded it.
	if len(files[0].Regions) != 1 {
		t.Errorf("expected 1 region on b.go, got %d", len(files[0].Regions))
	}
}
