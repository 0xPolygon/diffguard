package tsanalyzer

import (
	"path/filepath"
	"slices"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/0xPolygon/diffguard/internal/lang"
)

// mutantApplierImpl implements lang.MutantApplier for TypeScript. We
// operate on source bytes directly — same strategy as the Rust analyzer —
// because tree-sitter gives us exact byte offsets for every node and
// text-level edits preserve formatting without a dedicated TS formatter.
//
// After every mutation we re-parse with the correct grammar (.ts vs .tsx
// based on the file's extension) and check for parse errors. If the
// mutated source fails to parse we return nil so the orchestrator treats
// the mutant as skipped rather than running invalid code.
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
	if !isValidTS(mutated, absPath) {
		return nil, nil
	}
	return mutated, nil
}

// applyBySite dispatches to the operator-specific helper.
func applyBySite(root *sitter.Node, src []byte, site lang.MutantSite) []byte {
	switch site.Operator {
	case "conditional_boundary", "negate_conditional", "math_operator",
		"strict_equality", "nullish_to_logical_or":
		return applyBinary(root, src, site)
	case "boolean_substitution":
		return applyBool(root, src, site)
	case "incdec":
		return applyIncDec(root, src, site)
	case "return_value":
		return applyReturnValue(root, src, site)
	case "branch_removal":
		return applyBranchRemoval(root, src, site)
	case "statement_deletion":
		return applyStatementDeletion(root, src, site)
	case "optional_chain_removal":
		return applyOptionalChainRemoval(root, src, site)
	}
	return nil
}

// findOnLine returns the first node matching `pred` whose start line
// equals `line`.
func findOnLine(root *sitter.Node, line int, pred func(*sitter.Node) bool) *sitter.Node {
	var hit *sitter.Node
	walk(root, func(n *sitter.Node) bool {
		if hit != nil {
			return false
		}
		if nodeLine(n) != line {
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

// replaceRange returns src with [start, end) replaced by `with`.
func replaceRange(src []byte, start, end uint32, with []byte) []byte {
	return slices.Concat(src[:start], with, src[end:])
}

// applyBinary swaps the operator of a binary_expression on the target
// line, honoring the description ("X -> Y") so overlapping binaries on
// the same line mutate the exact one the generator emitted.
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
		return n.Type() == "true" || n.Type() == "false"
	})
	if n == nil {
		return nil
	}
	flipped := "false"
	if n.Type() == "false" {
		flipped = "true"
	}
	return replaceRange(src, n.StartByte(), n.EndByte(), []byte(flipped))
}

// applyIncDec swaps ++ and -- on an update_expression on the target line.
// We rewrite just the operator token, keeping pre/postfix position intact.
func applyIncDec(root *sitter.Node, src []byte, site lang.MutantSite) []byte {
	n := findOnLine(root, site.Line, func(n *sitter.Node) bool {
		if n.Type() != "update_expression" {
			return false
		}
		op := n.ChildByFieldName("operator")
		return op != nil && (op.Type() == "++" || op.Type() == "--")
	})
	if n == nil {
		return nil
	}
	op := n.ChildByFieldName("operator")
	flipped := "--"
	if op.Type() == "--" {
		flipped = "++"
	}
	return replaceRange(src, op.StartByte(), op.EndByte(), []byte(flipped))
}

// applyReturnValue replaces the returned expression with `null` or
// `undefined` based on the description. We read the target from the
// description so the applier and generator agree on which value to write.
func applyReturnValue(root *sitter.Node, src []byte, site lang.MutantSite) []byte {
	ret := findOnLine(root, site.Line, func(n *sitter.Node) bool {
		return n.Type() == "return_statement"
	})
	if ret == nil {
		return nil
	}
	if ret.NamedChildCount() == 0 {
		return nil
	}
	value := ret.NamedChild(0)
	if value == nil {
		return nil
	}
	target := "null"
	if strings.Contains(site.Description, "undefined") {
		target = "undefined"
	}
	return replaceRange(src, value.StartByte(), value.EndByte(), []byte(target))
}

// applyBranchRemoval empties the consequence block of an if_statement.
// We preserve the outer braces; remove only the inner bytes so the
// resulting source still parses.
func applyBranchRemoval(root *sitter.Node, src []byte, site lang.MutantSite) []byte {
	ifNode := findOnLine(root, site.Line, func(n *sitter.Node) bool {
		return n.Type() == "if_statement"
	})
	if ifNode == nil {
		return nil
	}
	body := ifNode.ChildByFieldName("consequence")
	if body == nil {
		return nil
	}
	inner := bodyInnerRange(body, src)
	if inner == nil {
		// If the consequence is a single statement (no braces), replace
		// the whole statement with an empty block `{}` so the `if`
		// structure stays intact and parseable.
		return replaceRange(src, body.StartByte(), body.EndByte(), []byte("{}"))
	}
	return replaceRange(src, inner[0], inner[1], []byte{})
}

// bodyInnerRange returns [openBracePlusOne, closeBrace) for a block node,
// or nil if the node doesn't look like a braced block.
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

// applyStatementDeletion replaces a bare call statement with an empty
// statement (`;`). Keeps the source parseable and kills any side effect.
func applyStatementDeletion(root *sitter.Node, src []byte, site lang.MutantSite) []byte {
	stmt := findOnLine(root, site.Line, func(n *sitter.Node) bool {
		return n.Type() == "expression_statement"
	})
	if stmt == nil {
		return nil
	}
	return replaceRange(src, stmt.StartByte(), stmt.EndByte(), []byte(";"))
}

// applyOptionalChainRemoval replaces a `?.` token between the object and
// property of a member_expression on the target line with a plain `.`.
// Token scanning is delegated to optionalChainTokenOffset so detection and
// application share one implementation.
func applyOptionalChainRemoval(root *sitter.Node, src []byte, site lang.MutantSite) []byte {
	n := findOnLine(root, site.Line, func(n *sitter.Node) bool {
		return n.Type() == "member_expression" && hasOptionalChainToken(n, src)
	})
	if n == nil {
		return nil
	}
	tokenStart, ok := optionalChainTokenOffset(n, src)
	if !ok {
		return nil
	}
	return replaceRange(src, tokenStart, tokenStart+2, []byte("."))
}

// isValidTS re-parses the mutated source with the grammar matching the
// original file extension and reports whether tree-sitter encountered any
// syntax errors. We pick the grammar from the ABSOLUTE path so `.tsx`
// files are validated with the tsx grammar.
func isValidTS(src []byte, absPath string) bool {
	grammar := typescriptLanguage()
	if strings.ToLower(filepath.Ext(absPath)) == ".tsx" {
		grammar = tsxLanguage()
	}
	tree, err := parseBytesAs(src, grammar)
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
