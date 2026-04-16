package rustanalyzer

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/0xPolygon/diffguard/internal/lang"
)

// mutantApplierImpl implements lang.MutantApplier for Rust. Unlike the Go
// analyzer, which rewrites the AST and re-renders with go/printer, we
// operate on source bytes directly: tree-sitter reports exact byte offsets
// for every node, and text-level edits keep formatting intact without a
// dedicated Rust formatter.
//
// After every mutation we re-parse the output with tree-sitter and check
// for ERROR nodes. If the mutation produced syntactically invalid code we
// return nil (no bytes, no error) — the mutation orchestrator treats that
// as "skip this mutant", matching the Go analyzer's contract.
type mutantApplierImpl struct{}

// ApplyMutation returns the mutated file bytes, or (nil, nil) if the
// mutation can't be applied cleanly.
func (mutantApplierImpl) ApplyMutation(absPath string, site lang.MutantSite) ([]byte, error) {
	tree, src, err := parseFile(absPath)
	if err != nil {
		return nil, nil
	}
	defer tree.Close()

	mutated := applyBySite(tree.RootNode(), src, site)
	if mutated == nil {
		return nil, nil
	}
	if !isValidRust(mutated) {
		// Re-parse check per the design doc: don't ship corrupt mutants.
		return nil, nil
	}
	return mutated, nil
}

// applyBySite dispatches to the operator-specific helper. Each helper
// returns either the mutated byte slice or nil if it couldn't find a
// matching node on the target line.
func applyBySite(root *sitter.Node, src []byte, site lang.MutantSite) []byte {
	switch site.Operator {
	case "conditional_boundary", "negate_conditional", "math_operator":
		return applyBinary(root, src, site)
	case "boolean_substitution":
		return applyBool(root, src, site)
	case "return_value":
		return applyReturnValue(root, src, site)
	case "some_to_none":
		return applySomeToNone(root, src, site)
	case "branch_removal":
		return applyBranchRemoval(root, src, site)
	case "statement_deletion":
		return applyStatementDeletion(root, src, site)
	case "unwrap_removal":
		return applyUnwrapRemoval(root, src, site)
	case "question_mark_removal":
		return applyQuestionMarkRemoval(root, src, site)
	}
	return nil
}

// findOnLine returns the first node matching `pred` whose start line
// equals `line`. We keep it small: the CST walks are tiny and predicates
// stay decidable in one pass.
func findOnLine(root *sitter.Node, line int, pred func(*sitter.Node) bool) *sitter.Node {
	var hit *sitter.Node
	walk(root, func(n *sitter.Node) bool {
		if hit != nil {
			return false
		}
		if nodeLine(n) != line {
			// We're still searching; descend into children that might
			// reach the target line.
			if int(n.StartPoint().Row)+1 > line || int(n.EndPoint().Row)+1 < line {
				return false
			}
			return true
		}
		if pred(n) {
			hit = n
			return false
		}
		return true
	})
	return hit
}

// replaceRange returns src with the bytes [start, end) replaced by `with`.
func replaceRange(src []byte, start, end uint32, with []byte) []byte {
	out := make([]byte, 0, len(src)-int(end-start)+len(with))
	out = append(out, src[:start]...)
	out = append(out, with...)
	out = append(out, src[end:]...)
	return out
}

// applyBinary swaps the operator of a binary_expression on the target line.
// We honor the site description so overlapping binaries on the same line
// (`a == b && c > d`) mutate the exact one the generator emitted.
func applyBinary(root *sitter.Node, src []byte, site lang.MutantSite) []byte {
	fromOp, toOp := parseBinaryDesc(site.Description)
	if fromOp == "" {
		return nil
	}
	var target *sitter.Node
	walk(root, func(n *sitter.Node) bool {
		if target != nil {
			return false
		}
		if n.Type() != "binary_expression" || nodeLine(n) != site.Line {
			return true
		}
		op := n.ChildByFieldName("operator")
		if op != nil && op.Type() == fromOp {
			target = n
			return false
		}
		return true
	})
	if target == nil {
		return nil
	}
	op := target.ChildByFieldName("operator")
	return replaceRange(src, op.StartByte(), op.EndByte(), []byte(toOp))
}

// parseBinaryDesc parses "X -> Y" from the mutant description.
func parseBinaryDesc(desc string) (string, string) {
	parts := strings.SplitN(desc, " -> ", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

// applyBool flips a boolean literal on the target line.
func applyBool(root *sitter.Node, src []byte, site lang.MutantSite) []byte {
	n := findOnLine(root, site.Line, func(n *sitter.Node) bool {
		if n.Type() != "boolean_literal" {
			return false
		}
		txt := nodeText(n, src)
		return txt == "true" || txt == "false"
	})
	if n == nil {
		return nil
	}
	txt := nodeText(n, src)
	flipped := "true"
	if txt == "true" {
		flipped = "false"
	}
	return replaceRange(src, n.StartByte(), n.EndByte(), []byte(flipped))
}

// applyReturnValue replaces the returned expression with
// `Default::default()`. Works for any non-unit return; tests on Option /
// unit / numeric returns will all observe either a type mismatch (caught
// by the re-parse step — wait, rustc type errors won't show in
// tree-sitter; so this is a Tier-1 operator that can produce equivalent
// mutants on some types, which we accept).
func applyReturnValue(root *sitter.Node, src []byte, site lang.MutantSite) []byte {
	ret := findOnLine(root, site.Line, func(n *sitter.Node) bool {
		return n.Type() == "return_expression"
	})
	if ret == nil {
		return nil
	}
	var value *sitter.Node
	for i := 0; i < int(ret.NamedChildCount()); i++ {
		value = ret.NamedChild(i)
		break
	}
	if value == nil {
		return nil
	}
	return replaceRange(src, value.StartByte(), value.EndByte(), []byte("Default::default()"))
}

// applySomeToNone replaces a `Some(x)` call expression with `None`. The
// target can sit anywhere — inside a return, as the tail expression of
// a block, as an argument to another function, etc. We find the first
// call_expression on the line whose function identifier is exactly
// `Some` and rewrite the entire call to `None`.
func applySomeToNone(root *sitter.Node, src []byte, site lang.MutantSite) []byte {
	call := findOnLine(root, site.Line, func(n *sitter.Node) bool {
		if n.Type() != "call_expression" {
			return false
		}
		fn := n.ChildByFieldName("function")
		return fn != nil && nodeText(fn, src) == "Some"
	})
	if call == nil {
		return nil
	}
	return replaceRange(src, call.StartByte(), call.EndByte(), []byte("None"))
}

// applyBranchRemoval empties the consequence block of an if_expression.
// We replace the block contents with nothing so the braces remain and
// the code still parses.
func applyBranchRemoval(root *sitter.Node, src []byte, site lang.MutantSite) []byte {
	ifNode := findOnLine(root, site.Line, func(n *sitter.Node) bool {
		return n.Type() == "if_expression"
	})
	if ifNode == nil {
		return nil
	}
	body := ifNode.ChildByFieldName("consequence")
	if body == nil {
		return nil
	}
	// Preserve the outer braces; replace inner bytes with an empty body.
	inner := bodyInnerRange(body, src)
	if inner == nil {
		return nil
	}
	return replaceRange(src, inner[0], inner[1], []byte{})
}

// bodyInnerRange returns [openBracePlusOne, closeBrace) for a block node —
// i.e. the byte range strictly inside the braces. Returns nil if the
// node doesn't look like a block with braces.
func bodyInnerRange(block *sitter.Node, src []byte) []uint32 {
	start := block.StartByte()
	end := block.EndByte()
	if start >= end {
		return nil
	}
	if src[start] != '{' || src[end-1] != '}' {
		return nil
	}
	return []uint32{start + 1, end - 1}
}

// applyStatementDeletion replaces a bare call statement with the empty
// expression `();`. Keeps the source parseable and kills the side effect.
func applyStatementDeletion(root *sitter.Node, src []byte, site lang.MutantSite) []byte {
	stmt := findOnLine(root, site.Line, func(n *sitter.Node) bool {
		return n.Type() == "expression_statement"
	})
	if stmt == nil {
		return nil
	}
	return replaceRange(src, stmt.StartByte(), stmt.EndByte(), []byte("();"))
}

// applyUnwrapRemoval strips `.unwrap()` / `.expect(...)` from a call,
// leaving the receiver. We find the outer call_expression, then rewrite
// the whole call to be just the receiver.
func applyUnwrapRemoval(root *sitter.Node, src []byte, site lang.MutantSite) []byte {
	call := findOnLine(root, site.Line, func(n *sitter.Node) bool {
		if n.Type() != "call_expression" {
			return false
		}
		fn := n.ChildByFieldName("function")
		if fn == nil || fn.Type() != "field_expression" {
			return false
		}
		field := fn.ChildByFieldName("field")
		if field == nil {
			return false
		}
		name := nodeText(field, src)
		return name == "unwrap" || name == "expect"
	})
	if call == nil {
		return nil
	}
	fn := call.ChildByFieldName("function")
	receiver := fn.ChildByFieldName("value")
	if receiver == nil {
		return nil
	}
	return replaceRange(src, call.StartByte(), call.EndByte(),
		src[receiver.StartByte():receiver.EndByte()])
}

// applyQuestionMarkRemoval strips the trailing `?` from a try_expression.
// Grammar shape: (try_expression <inner>?) — the `?` token sits after the
// inner expression's end byte.
func applyQuestionMarkRemoval(root *sitter.Node, src []byte, site lang.MutantSite) []byte {
	try := findOnLine(root, site.Line, func(n *sitter.Node) bool {
		return n.Type() == "try_expression"
	})
	if try == nil {
		return nil
	}
	// The inner expression is the first (and only) named child.
	var inner *sitter.Node
	for i := 0; i < int(try.NamedChildCount()); i++ {
		inner = try.NamedChild(i)
		break
	}
	if inner == nil {
		return nil
	}
	return replaceRange(src, try.StartByte(), try.EndByte(),
		src[inner.StartByte():inner.EndByte()])
}

// isValidRust re-parses the mutated source and reports whether tree-sitter
// encountered any syntax errors. tree-sitter marks malformed regions with
// ERROR nodes (or sets HasError on ancestors); we check both.
func isValidRust(src []byte) bool {
	tree, err := parseBytes(src)
	if err != nil || tree == nil {
		return false
	}
	defer tree.Close()
	root := tree.RootNode()
	if root == nil {
		return false
	}
	return !root.HasError()
}

