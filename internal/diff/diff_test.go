package diff

import (
	"testing"
)

func TestParseUnifiedDiff(t *testing.T) {
	input := `diff --git a/pkg/handler/routes.go b/pkg/handler/routes.go
index abc1234..def5678 100644
--- a/pkg/handler/routes.go
+++ b/pkg/handler/routes.go
@@ -10,0 +11,5 @@
+func newHandler() {
+    // new code
+    return nil
+}
+
@@ -50,3 +55,4 @@
+    extra line
diff --git a/pkg/auth/token.go b/pkg/auth/token.go
index 1111111..2222222 100644
--- a/pkg/auth/token.go
+++ b/pkg/auth/token.go
@@ -20,2 +20,3 @@
+    modified line
diff --git a/pkg/handler/routes_test.go b/pkg/handler/routes_test.go
--- a/pkg/handler/routes_test.go
+++ b/pkg/handler/routes_test.go
@@ -1,0 +1,5 @@
+test file should be skipped
`

	files, err := parseUnifiedDiff(input)
	if err != nil {
		t.Fatalf("parseUnifiedDiff error: %v", err)
	}

	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}

	// Check first file
	if files[0].Path != "pkg/handler/routes.go" {
		t.Errorf("file[0].Path = %q, want %q", files[0].Path, "pkg/handler/routes.go")
	}
	if len(files[0].Regions) != 2 {
		t.Fatalf("file[0] regions = %d, want 2", len(files[0].Regions))
	}
	if files[0].Regions[0].StartLine != 11 || files[0].Regions[0].EndLine != 15 {
		t.Errorf("region[0] = %+v, want {11, 15}", files[0].Regions[0])
	}
	if files[0].Regions[1].StartLine != 55 || files[0].Regions[1].EndLine != 58 {
		t.Errorf("region[1] = %+v, want {55, 58}", files[0].Regions[1])
	}

	// Check second file
	if files[1].Path != "pkg/auth/token.go" {
		t.Errorf("file[1].Path = %q, want %q", files[1].Path, "pkg/auth/token.go")
	}
	if len(files[1].Regions) != 1 {
		t.Fatalf("file[1] regions = %d, want 1", len(files[1].Regions))
	}
}

func TestParseUnifiedDiff_PureDeletion(t *testing.T) {
	input := `diff --git a/pkg/old.go b/pkg/old.go
--- a/pkg/old.go
+++ b/pkg/old.go
@@ -10,5 +10,0 @@
`

	files, err := parseUnifiedDiff(input)
	if err != nil {
		t.Fatalf("parseUnifiedDiff error: %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	// Pure deletion should have no regions
	if len(files[0].Regions) != 0 {
		t.Errorf("expected 0 regions for pure deletion, got %d", len(files[0].Regions))
	}
}

func TestFileChange_ContainsLine(t *testing.T) {
	fc := FileChange{
		Path: "test.go",
		Regions: []ChangedRegion{
			{StartLine: 10, EndLine: 20},
			{StartLine: 50, EndLine: 55},
		},
	}

	tests := []struct {
		line     int
		expected bool
	}{
		{9, false},
		{10, true},
		{15, true},
		{20, true},
		{21, false},
		{49, false},
		{50, true},
		{55, true},
		{56, false},
	}

	for _, tt := range tests {
		got := fc.ContainsLine(tt.line)
		if got != tt.expected {
			t.Errorf("ContainsLine(%d) = %v, want %v", tt.line, got, tt.expected)
		}
	}
}

func TestFileChange_OverlapsRange(t *testing.T) {
	fc := FileChange{
		Path: "test.go",
		Regions: []ChangedRegion{
			{StartLine: 10, EndLine: 20},
		},
	}

	tests := []struct {
		start, end int
		expected   bool
	}{
		{1, 9, false},
		{1, 10, true},
		{15, 18, true},
		{20, 30, true},
		{21, 30, false},
		{5, 25, true},
	}

	for _, tt := range tests {
		got := fc.OverlapsRange(tt.start, tt.end)
		if got != tt.expected {
			t.Errorf("OverlapsRange(%d, %d) = %v, want %v", tt.start, tt.end, got, tt.expected)
		}
	}
}

func TestResult_ChangedPackages(t *testing.T) {
	r := Result{
		Files: []FileChange{
			{Path: "pkg/handler/routes.go"},
			{Path: "pkg/handler/middleware.go"},
			{Path: "pkg/auth/token.go"},
		},
	}

	pkgs := r.ChangedPackages()
	if len(pkgs) != 2 {
		t.Fatalf("expected 2 packages, got %d: %v", len(pkgs), pkgs)
	}
}

func TestParseHunkHeader(t *testing.T) {
	tests := []struct {
		line  string
		start int
		end   int
		isNil bool
	}{
		{"@@ -10,3 +15,5 @@ func foo", 15, 19, false},
		{"@@ -10,3 +15 @@ func foo", 15, 15, false},
		{"@@ -10,5 +10,0 @@ deleted", 0, 0, true}, // pure deletion
	}

	for _, tt := range tests {
		region, err := parseHunkHeader(tt.line)
		if err != nil {
			t.Errorf("parseHunkHeader(%q) error: %v", tt.line, err)
			continue
		}
		if tt.isNil {
			if region != nil {
				t.Errorf("parseHunkHeader(%q) = %+v, want nil", tt.line, region)
			}
			continue
		}
		if region == nil {
			t.Errorf("parseHunkHeader(%q) = nil, want {%d, %d}", tt.line, tt.start, tt.end)
			continue
		}
		if region.StartLine != tt.start || region.EndLine != tt.end {
			t.Errorf("parseHunkHeader(%q) = {%d, %d}, want {%d, %d}",
				tt.line, region.StartLine, region.EndLine, tt.start, tt.end)
		}
	}
}
