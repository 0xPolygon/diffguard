package diff

import (
	"testing"
)

func TestHandleFileLine_GoFile(t *testing.T) {
	var files []FileChange
	result := handleFileLine("+++ b/pkg/handler.go", &files)
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
	result := handleFileLine("+++ b/pkg/handler_test.go", &files)
	if result != nil {
		t.Error("expected nil for test file")
	}
	if len(files) != 0 {
		t.Error("test file should not be added")
	}
}

func TestHandleFileLine_NonGoFile(t *testing.T) {
	var files []FileChange
	result := handleFileLine("+++ b/README.md", &files)
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
	files, err := parseUnifiedDiff(input)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files for non-Go file, got %d", len(files))
	}
}

func TestParseUnifiedDiff_EmptyInput(t *testing.T) {
	files, err := parseUnifiedDiff("")
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
