package lang

import "testing"

func TestFileFilter_MatchesExtension(t *testing.T) {
	f := FileFilter{Extensions: []string{".go"}}
	tests := []struct {
		path string
		want bool
	}{
		{"foo.go", true},
		{"path/to/foo.go", true},
		{"foo_test.go", true},
		{"foo.txt", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := f.MatchesExtension(tt.path); got != tt.want {
			t.Errorf("MatchesExtension(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestFileFilter_IncludesSource(t *testing.T) {
	f := FileFilter{
		Extensions: []string{".go"},
		IsTestFile: func(p string) bool {
			return len(p) >= len("_test.go") && p[len(p)-len("_test.go"):] == "_test.go"
		},
	}
	tests := []struct {
		path string
		want bool
	}{
		{"foo.go", true},
		{"foo_test.go", false},
		{"foo.txt", false},
	}
	for _, tt := range tests {
		if got := f.IncludesSource(tt.path); got != tt.want {
			t.Errorf("IncludesSource(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestFileFilter_MultipleExtensions(t *testing.T) {
	f := FileFilter{Extensions: []string{".ts", ".tsx"}}
	if !f.MatchesExtension("foo.ts") {
		t.Error("want .ts to match")
	}
	if !f.MatchesExtension("foo.tsx") {
		t.Error("want .tsx to match")
	}
	if f.MatchesExtension("foo.js") {
		t.Error("want .js not to match")
	}
}

func TestFileFilter_NilIsTestFile(t *testing.T) {
	// IncludesSource with nil IsTestFile must not panic and should treat
	// everything with a matching extension as non-test.
	f := FileFilter{Extensions: []string{".go"}}
	if !f.IncludesSource("foo_test.go") {
		t.Error("with nil IsTestFile, everything with matching ext should be included")
	}
}
