package tsanalyzer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/0xPolygon/diffguard/internal/diff"
)

func tsFileChange(path string, startLine, endLine int) diff.FileChange {
	return diff.FileChange{
		Path:    path,
		Regions: []diff.ChangedRegion{{StartLine: startLine, EndLine: endLine}},
	}
}

func writeTSFile(t *testing.T, filename, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFindDeadCode_UnusedFunction(t *testing.T) {
	dir := writeTSFile(t, "a.ts", `function unused() { return 1; }

function used() { return 2; }

console.log(used());
`)
	got, err := deadcodeImpl{}.FindDeadCode(dir, tsFileChange("a.ts", 1, 100))
	if err != nil {
		t.Fatalf("FindDeadCode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d unused symbols, want 1: %+v", len(got), got)
	}
	if got[0].Name != "unused" || got[0].Kind != "func" {
		t.Errorf("got %+v, want unused/func", got[0])
	}
}

func TestFindDeadCode_ExportedSkipped(t *testing.T) {
	dir := writeTSFile(t, "a.ts", `export function publicUnused() { return 1; }

export const value = 42;
`)
	got, _ := deadcodeImpl{}.FindDeadCode(dir, tsFileChange("a.ts", 1, 100))
	if len(got) != 0 {
		t.Errorf("exported names should be skipped, got %+v", got)
	}
}

func TestFindDeadCode_UnusedConst(t *testing.T) {
	dir := writeTSFile(t, "a.ts", `const unused = 5;
const used = 10;
console.log(used);
`)
	got, _ := deadcodeImpl{}.FindDeadCode(dir, tsFileChange("a.ts", 1, 100))
	if len(got) != 1 {
		t.Fatalf("got %d, want 1: %+v", len(got), got)
	}
	if got[0].Name != "unused" || got[0].Kind != "var" {
		t.Errorf("got %+v, want unused/var", got[0])
	}
}

func TestFindDeadCode_UnusedArrowFunctionConst(t *testing.T) {
	// `const helper = () => ...` should be treated as a function (kind "func"),
	// not a plain variable, so the report message reads naturally.
	dir := writeTSFile(t, "a.ts", `const helper = (x: number) => x + 1;
`)
	got, _ := deadcodeImpl{}.FindDeadCode(dir, tsFileChange("a.ts", 1, 100))
	if len(got) != 1 {
		t.Fatalf("got %d, want 1: %+v", len(got), got)
	}
	if got[0].Name != "helper" || got[0].Kind != "func" {
		t.Errorf("got %+v, want helper/func", got[0])
	}
}

func TestFindDeadCode_UnusedClass(t *testing.T) {
	dir := writeTSFile(t, "a.ts", `class Unused {
    foo(): void {}
}
`)
	got, _ := deadcodeImpl{}.FindDeadCode(dir, tsFileChange("a.ts", 1, 100))
	if len(got) != 1 {
		t.Fatalf("got %d, want 1: %+v", len(got), got)
	}
	if got[0].Name != "Unused" || got[0].Kind != "class" {
		t.Errorf("got %+v, want Unused/class", got[0])
	}
}

func TestFindDeadCode_UsedClass(t *testing.T) {
	dir := writeTSFile(t, "a.ts", `class Used {}

const x = new Used();
console.log(x);
`)
	got, _ := deadcodeImpl{}.FindDeadCode(dir, tsFileChange("a.ts", 1, 100))
	if len(got) != 0 {
		t.Errorf("Used should not be flagged, got %+v", got)
	}
}

func TestFindDeadCode_OutsideChangedRegionIgnored(t *testing.T) {
	dir := writeTSFile(t, "a.ts", `function unused() { return 1; }
`)
	got, _ := deadcodeImpl{}.FindDeadCode(dir, tsFileChange("a.ts", 100, 200))
	if len(got) != 0 {
		t.Errorf("outside changed region should be skipped, got %+v", got)
	}
}

func TestFindDeadCode_ReferenceFromInsideAnotherFunction(t *testing.T) {
	dir := writeTSFile(t, "a.ts", `function helper() { return 1; }

export function api() {
    return helper();
}
`)
	got, _ := deadcodeImpl{}.FindDeadCode(dir, tsFileChange("a.ts", 1, 100))
	if len(got) != 0 {
		t.Errorf("helper used inside api should not be flagged, got %+v", got)
	}
}

func TestFindDeadCode_DestructuredVarSkipped(t *testing.T) {
	// const { a, b } = obj — the destructuring pattern is not an identifier
	// node in tree-sitter, so we conservatively skip these. Tracking which
	// individual fields are "used" needs per-property analysis we don't do.
	dir := writeTSFile(t, "a.ts", `const obj = { a: 1, b: 2 };
const { a, b } = obj;
console.log(a, b);
`)
	got, _ := deadcodeImpl{}.FindDeadCode(dir, tsFileChange("a.ts", 1, 100))
	// We expect no flag for the destructured names; obj is used so it's not
	// flagged either. The exact assertion is "a" and "b" are not in results.
	for _, r := range got {
		if r.Name == "a" || r.Name == "b" {
			t.Errorf("destructured name %q should not be flagged: %+v", r.Name, got)
		}
	}
}

func TestFindDeadCode_MultipleDeclaratorsInOneStmt(t *testing.T) {
	dir := writeTSFile(t, "a.ts", `let a = 1, b = 2, c = 3;
console.log(a);
`)
	got, _ := deadcodeImpl{}.FindDeadCode(dir, tsFileChange("a.ts", 1, 100))

	names := map[string]bool{}
	for _, r := range got {
		names[r.Name] = true
	}
	if names["a"] {
		t.Errorf("a is used, should not be flagged: %+v", got)
	}
	if !names["b"] || !names["c"] {
		t.Errorf("b and c are unused, should be flagged: %+v", got)
	}
}

func TestFindDeadCode_PropertyAccessIsNotAReference(t *testing.T) {
	// `obj.foo` reads foo as a property_identifier, not an identifier — so
	// a top-level `function foo` that's only matched by a property access
	// should still be flagged as unused.
	dir := writeTSFile(t, "a.ts", `function foo() { return 1; }

const obj = { foo: 42 };
console.log(obj.foo);
`)
	got, _ := deadcodeImpl{}.FindDeadCode(dir, tsFileChange("a.ts", 1, 100))
	flagged := false
	for _, r := range got {
		if r.Name == "foo" && r.Kind == "func" {
			flagged = true
		}
	}
	if !flagged {
		t.Errorf("function foo (only matched by property access) should be flagged, got %+v", got)
	}
}

func TestFindDeadCode_ParseErrorTolerated(t *testing.T) {
	// Tree-sitter parses partial trees on broken input, so the call may
	// either return nothing or whatever it managed to extract. Either way
	// it must not error out.
	dir := writeTSFile(t, "a.ts", `function broken( {`)
	_, err := deadcodeImpl{}.FindDeadCode(dir, tsFileChange("a.ts", 1, 100))
	if err != nil {
		t.Errorf("parse error should be tolerated, got err=%v", err)
	}
}
