package rustanalyzer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCargoPackageName(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{
			src: `
[package]
name = "diffguard-rust-fixture"
version = "0.1.0"
`,
			want: "diffguard-rust-fixture",
		},
		{
			src: `
[package]
name="foo"
`,
			want: "foo",
		},
		{
			// Nested table: name under [dependencies] must NOT match.
			src: `
[dependencies]
name = "other"

[package]
name = "real-pkg"
`,
			want: "real-pkg",
		},
		{
			src:  `[workspace]\nmembers = []`,
			want: "",
		},
	}
	for _, tc := range cases {
		got := parseCargoPackageName(tc.src)
		if got != tc.want {
			t.Errorf("parseCargoPackageName got %q, want %q", got, tc.want)
		}
	}
}

func TestDetectModulePath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(`
[package]
name = "my-crate"
version = "0.1.0"
`), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := depsImpl{}.DetectModulePath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "my-crate" {
		t.Errorf("DetectModulePath = %q, want my-crate", got)
	}
}

func TestDetectModulePath_Missing(t *testing.T) {
	dir := t.TempDir()
	_, err := depsImpl{}.DetectModulePath(dir)
	if err == nil {
		t.Error("expected error for missing Cargo.toml")
	}
}

// TestScanPackageImports_InternalVsExternal asserts that `use crate::...`
// and `use super::...` produce internal edges while external crates and
// std imports are filtered out.
func TestScanPackageImports_InternalVsExternal(t *testing.T) {
	root := t.TempDir()

	// Layout:
	//   Cargo.toml
	//   src/
	//     lib.rs      -- `use crate::foo::bar::Baz;` + `use std::fmt;`
	//     foo/
	//       mod.rs
	//       bar.rs
	//   src/util/mod.rs -- `use super::foo::Helper;`
	must := func(p, content string) {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	must("Cargo.toml", `
[package]
name = "demo"
`)
	must("src/lib.rs", `
use crate::foo::bar::Baz;
use std::fmt;
mod foo;
mod util;
`)
	must("src/foo/mod.rs", `
pub mod bar;
`)
	must("src/foo/bar.rs", `
pub struct Baz;
`)
	must("src/util/mod.rs", `
use super::foo::Helper;
`)

	// Scan src/ — should find the `use crate::foo::bar` edge (-> src/foo/bar)
	// and `mod foo;` (-> src/foo) and `mod util;` (-> src/util). External
	// std import must NOT create an edge.
	edges := depsImpl{}.ScanPackageImports(root, "src", "demo")
	if edges == nil {
		t.Fatal("expected non-nil edges for src")
	}
	srcEdges := edges["src"]
	if srcEdges == nil {
		t.Fatalf("expected edges keyed by 'src', got %v", edges)
	}
	// Expected internal edges (directory nodes):
	expectedInternal := []string{
		"src/foo/bar", // crate::foo::bar
		"src/foo",     // mod foo;
		"src/util",    // mod util;
	}
	for _, want := range expectedInternal {
		if !srcEdges[want] {
			t.Errorf("missing edge to %q in %v", want, srcEdges)
		}
	}

	// Nothing external should sneak in.
	for k := range srcEdges {
		if k == "std/fmt" || k == "std" {
			t.Errorf("external std edge leaked: %q", k)
		}
	}
}

// TestScanPackageImports_SuperResolution directly asserts the resolver on
// a "super::" use to keep the relative-path arithmetic honest in isolation.
func TestScanPackageImports_SuperResolution(t *testing.T) {
	// super:: in pkgDir=src/util resolves to src/foo for `super::foo::X`.
	got := resolveInternalPath([]string{"super", "foo", "Bar"}, "src/util")
	want := "src/foo"
	if got != want {
		t.Errorf("resolveInternalPath(super::foo::Bar in src/util) = %q, want %q", got, want)
	}
	// self:: in pkgDir=src resolves to src for `self::foo::X`.
	got = resolveInternalPath([]string{"self", "foo", "Bar"}, "src")
	want = "src/foo"
	if got != want {
		t.Errorf("resolveInternalPath(self::foo::Bar in src) = %q, want %q", got, want)
	}
	// crate::x::y::Z always resolves to src/x/y regardless of pkgDir.
	got = resolveInternalPath([]string{"crate", "x", "y", "Z"}, "anywhere")
	want = "src/x/y"
	if got != want {
		t.Errorf("resolveInternalPath(crate::x::y::Z) = %q, want %q", got, want)
	}
	// External roots return "".
	got = resolveInternalPath([]string{"std", "fmt", "Display"}, "src")
	if got != "" {
		t.Errorf("resolveInternalPath(std::fmt::Display) = %q, want empty", got)
	}
}
