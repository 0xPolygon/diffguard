package tsanalyzer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectModulePath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`
{
  "name": "my-package",
  "version": "1.0.0"
}
`), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := depsImpl{}.DetectModulePath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "my-package" {
		t.Errorf("DetectModulePath = %q, want my-package", got)
	}
}

func TestDetectModulePath_Missing(t *testing.T) {
	dir := t.TempDir()
	_, err := depsImpl{}.DetectModulePath(dir)
	if err == nil {
		t.Error("expected error for missing package.json")
	}
}

func TestDetectModulePath_NoName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"version":"1.0.0"}`), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := depsImpl{}.DetectModulePath(dir)
	if err == nil {
		t.Error("expected error for package.json without name")
	}
}

// TestScanPackageImports_InternalVsExternal asserts that relative imports
// and project-alias imports produce internal edges while bare specifiers
// (external packages) are filtered out.
func TestScanPackageImports_InternalVsExternal(t *testing.T) {
	root := t.TempDir()

	must := func(p, content string) {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	must("package.json", `{"name":"demo"}`)
	must("src/index.ts", `
import { foo } from './foo';
import { bar } from '../util/bar';
import * as _React from 'react';
import { Card } from '@/components/Card';
import { util } from '~/lib/util';
`)
	must("src/foo.ts", `export const foo = 1;`)
	must("util/bar.ts", `export const bar = 2;`)

	edges := depsImpl{}.ScanPackageImports(root, "src", "demo")
	if edges == nil {
		t.Fatal("expected non-nil edges for src")
	}
	srcEdges := edges["src"]
	if srcEdges == nil {
		t.Fatalf("expected edges keyed by 'src', got %v", edges)
	}

	// Relative imports resolve against src/, so './foo' -> src/foo.
	if !srcEdges["src/foo"] {
		t.Errorf("missing internal edge src/foo in %v", srcEdges)
	}
	// '../util/bar' resolves against parent of src -> util/bar.
	if !srcEdges["util/bar"] {
		t.Errorf("missing internal edge util/bar in %v", srcEdges)
	}
	// Project aliases: @/components/Card -> @/components (directory of the
	// imported symbol), ~/lib/util -> lib/util (the directory, since
	// resolveInternal folds 'util' as the target dir).
	if !srcEdges["@/components"] {
		t.Errorf("missing alias edge @/components in %v", srcEdges)
	}
	if !srcEdges["lib/util"] {
		t.Errorf("missing alias edge lib/util in %v", srcEdges)
	}

	// External imports must not leak edges. 'react' is bare.
	for k := range srcEdges {
		if k == "react" {
			t.Errorf("external react edge leaked: %q", k)
		}
	}
}

// TestScanPackageImports_Require exercises the CommonJS path.
func TestScanPackageImports_Require(t *testing.T) {
	root := t.TempDir()

	must := func(p, content string) {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	must("package.json", `{"name":"demo"}`)
	must("src/index.ts", `
const foo = require('./foo');
const lodash = require('lodash');
`)
	must("src/foo.ts", `module.exports = {};`)

	edges := depsImpl{}.ScanPackageImports(root, "src", "demo")
	srcEdges := edges["src"]
	if !srcEdges["src/foo"] {
		t.Errorf("missing require internal edge src/foo, got %v", srcEdges)
	}
	if srcEdges["lodash"] {
		t.Errorf("external require leaked: %v", srcEdges)
	}
}

// TestResolveInternal exercises the resolver directly — handy for pinning
// the alias / relative path rules.
func TestResolveInternal(t *testing.T) {
	cases := []struct {
		spec   string
		pkgDir string
		want   string
	}{
		{"./foo", "src", "src/foo"},
		{"./foo/bar", "src", "src/foo/bar"},
		{"../util/x", "src/lib", "src/util/x"},
		{"@/components/Card", "src", "@/components"},
		{"~/lib/util", "src", "lib/util"},
		{"lodash", "src", ""},
		{"react-dom", "src", ""},
		// index fold: `./dir/index` collapses to `./dir`
		{"./comp/index", "src", "src/comp"},
	}
	for _, tc := range cases {
		got := resolveInternal(tc.spec, "", tc.pkgDir)
		if got != tc.want {
			t.Errorf("resolveInternal(%q in %q) = %q, want %q", tc.spec, tc.pkgDir, got, tc.want)
		}
	}
}

// TestUnquote exercises the tiny literal-stripping helper so regressions
// (e.g. template string handling) are caught.
func TestUnquote(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`'foo'`, "foo"},
		{`"bar"`, "bar"},
		{"`baz`", "baz"},
		{"foo", "foo"}, // unquoted passthrough
		{`'foo`, `'foo`},
	}
	for _, tc := range cases {
		got := unquote(tc.in)
		if got != tc.want {
			t.Errorf("unquote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
