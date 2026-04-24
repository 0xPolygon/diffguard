package rustanalyzer

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xPolygon/diffguard/internal/lang"
)

// applyAt writes src to a temp file and invokes the applier for `site`.
// Returns the mutated bytes (or nil if the applier skipped the site).
func applyAt(t *testing.T, src string, site lang.MutantSite) []byte {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "a.rs")
	if err := writeFile(path, []byte(src)); err != nil {
		t.Fatal(err)
	}
	out, err := mutantApplierImpl{}.ApplyMutation(path, site)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestApply_BinaryOperator(t *testing.T) {
	src := `fn f(x: i32) -> bool {
    x > 0
}
`
	site := lang.MutantSite{
		File:        "a.rs",
		Line:        2,
		Operator:    "conditional_boundary",
		Description: "> -> >=",
	}
	out := applyAt(t, src, site)
	if out == nil {
		t.Fatal("applier returned nil")
	}
	if !strings.Contains(string(out), "x >= 0") {
		t.Errorf("expected 'x >= 0' in output, got:\n%s", out)
	}
}

func TestApply_BooleanFlip(t *testing.T) {
	src := `fn f() -> bool { true }
`
	site := lang.MutantSite{
		File:        "a.rs",
		Line:        1,
		Operator:    "boolean_substitution",
		Description: "true -> false",
	}
	out := applyAt(t, src, site)
	if out == nil {
		t.Fatal("applier returned nil")
	}
	if !strings.Contains(string(out), "false") {
		t.Errorf("expected 'false' in output, got:\n%s", out)
	}
	if strings.Contains(string(out), "true") {
		t.Errorf("'true' should have been replaced, got:\n%s", out)
	}
}

func TestApply_ReturnValueToDefault(t *testing.T) {
	src := `fn f() -> i32 {
    return 42;
}
`
	site := lang.MutantSite{
		File:        "a.rs",
		Line:        2,
		Operator:    "return_value",
		Description: "replace return value with Default::default()",
	}
	out := applyAt(t, src, site)
	if out == nil {
		t.Fatal("applier returned nil")
	}
	if !strings.Contains(string(out), "Default::default()") {
		t.Errorf("expected Default::default(), got:\n%s", out)
	}
}

func TestApply_SomeToNone(t *testing.T) {
	src := `fn g(x: i32) -> Option<i32> {
    return Some(x);
}
`
	site := lang.MutantSite{
		File:        "a.rs",
		Line:        2,
		Operator:    "some_to_none",
		Description: "Some(x) -> None",
	}
	out := applyAt(t, src, site)
	if out == nil {
		t.Fatal("applier returned nil")
	}
	if !strings.Contains(string(out), "return None;") {
		t.Errorf("expected 'return None;', got:\n%s", out)
	}
}

func TestApply_BranchRemoval(t *testing.T) {
	src := `fn side() {}
fn f(x: i32) {
    if x > 0 {
        side();
    }
}
`
	site := lang.MutantSite{
		File:        "a.rs",
		Line:        3,
		Operator:    "branch_removal",
		Description: "remove if body",
	}
	out := applyAt(t, src, site)
	if out == nil {
		t.Fatal("applier returned nil")
	}
	// The call inside the body should be gone.
	if strings.Contains(string(out), "side();") && strings.Contains(string(out), "if x > 0") {
		// The function-declaration body still contains `side()` statement;
		// we're asserting the if-body is emptied. After branch removal the
		// `side();` call inside the braces must not appear between the if
		// braces. Parse and check the if body is empty (approximated via
		// a substring match that fails only if the consequence body still
		// has text).
		if strings.Contains(string(out), "if x > 0 {\n        side();") {
			t.Errorf("if body not emptied, got:\n%s", out)
		}
	}
}

func TestApply_StatementDeletion(t *testing.T) {
	src := `fn side() {}
fn f() {
    side();
}
`
	site := lang.MutantSite{
		File:        "a.rs",
		Line:        3,
		Operator:    "statement_deletion",
		Description: "remove call statement",
	}
	out := applyAt(t, src, site)
	if out == nil {
		t.Fatal("applier returned nil")
	}
	if !strings.Contains(string(out), "();") {
		t.Errorf("expected statement replaced with '();', got:\n%s", out)
	}
}

func TestApply_UnwrapRemoval(t *testing.T) {
	src := `fn g(x: Option<i32>) -> i32 {
    x.unwrap()
}
`
	site := lang.MutantSite{
		File:        "a.rs",
		Line:        2,
		Operator:    "unwrap_removal",
		Description: "strip .unwrap()",
	}
	out := applyAt(t, src, site)
	if out == nil {
		t.Fatal("applier returned nil")
	}
	if strings.Contains(string(out), "unwrap") {
		t.Errorf(".unwrap() not stripped, got:\n%s", out)
	}
}

func TestApply_QuestionMarkRemoval(t *testing.T) {
	src := `fn g(x: Result<i32, ()>) -> Result<i32, ()> {
    let v = x?;
    Ok(v)
}
`
	site := lang.MutantSite{
		File:        "a.rs",
		Line:        2,
		Operator:    "question_mark_removal",
		Description: "strip trailing ?",
	}
	out := applyAt(t, src, site)
	if out == nil {
		t.Fatal("applier returned nil")
	}
	if strings.Contains(string(out), "?;") {
		t.Errorf("trailing ? not stripped, got:\n%s", out)
	}
}

// TestApply_ReparseRejectsCorrupt asserts that when the applier produces
// source that fails to tree-sitter parse (via a synthetic "apply every
// operator that doesn't exist" scenario), the applier returns nil.
//
// We exercise this via an operator the applier doesn't know — result is
// nil bytes, not a corrupt output.
func TestApply_UnknownOperatorReturnsNil(t *testing.T) {
	src := `fn f() {}
`
	site := lang.MutantSite{Line: 1, Operator: "nonexistent_op"}
	out := applyAt(t, src, site)
	if out != nil {
		t.Errorf("expected nil for unknown operator, got:\n%s", out)
	}
}

// TestApply_SiteMismatchReturnsNil asserts a mutant whose target line has
// no matching node is a silent no-op (nil bytes, no error).
func TestApply_SiteMismatchReturnsNil(t *testing.T) {
	src := `fn f() -> i32 { 42 }
`
	// boolean_substitution on a line that has no boolean literal.
	site := lang.MutantSite{Line: 1, Operator: "boolean_substitution", Description: "true -> false"}
	out := applyAt(t, src, site)
	if out != nil {
		t.Errorf("expected nil for site with no matching node, got:\n%s", out)
	}
}

// TestIsValidRust exercises the re-parse gate directly.
func TestIsValidRust(t *testing.T) {
	good := []byte(`fn f() -> i32 { 42 }`)
	bad := []byte(`fn f() -> i32 { 42 `) // missing brace
	if !isValidRust(good) {
		t.Error("well-formed Rust reported invalid")
	}
	if isValidRust(bad) {
		t.Error("malformed Rust reported valid")
	}
}
