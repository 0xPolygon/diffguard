package tsanalyzer

import (
	"fmt"
	"sort"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
)

// mutantGeneratorImpl implements lang.MutantGenerator for TypeScript. It
// emits canonical operators (conditional_boundary, negate_conditional,
// math_operator, return_value, boolean_substitution, incdec, branch_removal,
// statement_deletion) plus the TS-specific operators defined in the design
// doc: strict_equality, nullish_to_logical_or, optional_chain_removal.
//
// Unlike Rust, TypeScript has `++`/`--`, so incdec IS emitted.
type mutantGeneratorImpl struct{}

// GenerateMutants walks the CST and emits a MutantSite for each qualifying
// node on a changed, non-disabled line. Output is deterministic.
func (mutantGeneratorImpl) GenerateMutants(absPath string, fc diff.FileChange, disabled map[int]bool) ([]lang.MutantSite, error) {
	tree, src, err := parseFile(absPath)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	var out []lang.MutantSite
	walk(tree.RootNode(), func(n *sitter.Node) bool {
		line := nodeLine(n)
		if !fc.ContainsLine(line) || disabled[line] {
			return true
		}
		out = append(out, mutantsFor(fc.Path, line, n, src)...)
		return true
	})
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		if out[i].Operator != out[j].Operator {
			return out[i].Operator < out[j].Operator
		}
		return out[i].Description < out[j].Description
	})
	return out, nil
}

// mutantsFor dispatches on the node kind. Nodes that don't match any
// operator return nil.
func mutantsFor(file string, line int, n *sitter.Node, src []byte) []lang.MutantSite {
	switch n.Type() {
	case "binary_expression":
		return binaryMutants(file, line, n, src)
	case "true", "false":
		return boolLiteralMutants(file, line, n, src)
	case "update_expression":
		return updateMutants(file, line, n, src)
	case "return_statement":
		return returnMutants(file, line, n, src)
	case "if_statement":
		return ifMutants(file, line, n, src)
	case "expression_statement":
		return exprStmtMutants(file, line, n, src)
	case "member_expression":
		return optionalChainMutants(file, line, n, src)
	}
	return nil
}

// binaryMutants covers conditional_boundary, negate_conditional,
// math_operator, strict_equality, and nullish_to_logical_or.
//
// Rules (per design doc):
//   - `>` / `<` / `>=` / `<=` swaps  → conditional_boundary
//   - `==` / `!=` / `===` / `!==` flips → negate_conditional
//   - `===` ↔ `==`, `!==` ↔ `!=`  → strict_equality (Tier 1)
//   - `+` / `-`, `*` / `/` swaps  → math_operator
//   - `??` → `||`                 → nullish_to_logical_or (Tier 2)
//
// negate_conditional covers both loose (==/!=) and strict (===/!==)
// comparison flips, while strict_equality specifically toggles the
// strictness (===/==). Both can apply to the same source expression; we
// emit both so tests gain independent signal.
func binaryMutants(file string, line int, n *sitter.Node, _ []byte) []lang.MutantSite {
	opNode := n.ChildByFieldName("operator")
	if opNode == nil {
		return nil
	}
	op := opNode.Type()

	var out []lang.MutantSite

	// conditional_boundary + negate_conditional + math_operator: same flip
	// table as the Rust analyzer, extended with the TS-strict variants.
	flips := map[string]string{
		">":   ">=",
		"<":   "<=",
		">=":  ">",
		"<=":  "<",
		"==":  "!=",
		"!=":  "==",
		"===": "!==",
		"!==": "===",
		"+":   "-",
		"-":   "+",
		"*":   "/",
		"/":   "*",
	}
	if newOp, ok := flips[op]; ok {
		out = append(out, lang.MutantSite{
			File:        file,
			Line:        line,
			Description: fmt.Sprintf("%s -> %s", op, newOp),
			Operator:    binaryOperatorName(op, newOp),
		})
	}

	// strict_equality: toggle strictness independently of inversion.
	strictFlips := map[string]string{
		"===": "==",
		"==":  "===",
		"!==": "!=",
		"!=":  "!==",
	}
	if newOp, ok := strictFlips[op]; ok {
		out = append(out, lang.MutantSite{
			File:        file,
			Line:        line,
			Description: fmt.Sprintf("%s -> %s", op, newOp),
			Operator:    "strict_equality",
		})
	}

	// nullish_to_logical_or: `??` -> `||`. We don't emit the reverse
	// because `||` doesn't distinguish null/undefined from falsy, so
	// flipping `||` -> `??` would produce a tautological mutant on
	// non-nullable code.
	if op == "??" {
		out = append(out, lang.MutantSite{
			File:        file,
			Line:        line,
			Description: "?? -> ||",
			Operator:    "nullish_to_logical_or",
		})
	}

	return out
}

// binaryOperatorName classifies a source/target operator pair into the
// canonical tier-1 operator name. The strict (===/!==) equality operators
// fold into negate_conditional for this classifier; the strict_equality
// operator is emitted as a SEPARATE mutant by binaryMutants.
func binaryOperatorName(from, to string) string {
	if isBoundary(from) || isBoundary(to) {
		return "conditional_boundary"
	}
	if isComparison(from) || isComparison(to) {
		return "negate_conditional"
	}
	if isMath(from) || isMath(to) {
		return "math_operator"
	}
	return "unknown"
}

func isBoundary(op string) bool {
	return op == ">" || op == ">=" || op == "<" || op == "<="
}

func isComparison(op string) bool {
	return op == "==" || op == "!=" || op == "===" || op == "!=="
}

func isMath(op string) bool {
	return op == "+" || op == "-" || op == "*" || op == "/"
}

// boolLiteralMutants flips true <-> false. tree-sitter-typescript exposes
// boolean literals as nodes of type "true" and "false" (whose Type() is
// literally that token).
func boolLiteralMutants(file string, line int, n *sitter.Node, _ []byte) []lang.MutantSite {
	var flipped string
	switch n.Type() {
	case "true":
		flipped = "false"
	case "false":
		flipped = "true"
	default:
		return nil
	}
	return []lang.MutantSite{{
		File:        file,
		Line:        line,
		Description: fmt.Sprintf("%s -> %s", n.Type(), flipped),
		Operator:    "boolean_substitution",
	}}
}

// updateMutants emits the incdec operator for `++` and `--` expressions.
// Tree-sitter models `x++` / `++x` / `x--` / `--x` as update_expression.
func updateMutants(file string, line int, n *sitter.Node, _ []byte) []lang.MutantSite {
	opNode := n.ChildByFieldName("operator")
	if opNode == nil {
		return nil
	}
	op := opNode.Type()
	flipped := ""
	switch op {
	case "++":
		flipped = "--"
	case "--":
		flipped = "++"
	default:
		return nil
	}
	return []lang.MutantSite{{
		File:        file,
		Line:        line,
		Description: fmt.Sprintf("%s -> %s", op, flipped),
		Operator:    "incdec",
	}}
}

// returnMutants emits the return_value operator. TypeScript has both
// `null` and `undefined` as zero values; we use `null` when the return
// has a non-undefined expression, and `undefined` otherwise. An empty
// `return;` already returns undefined so there's nothing to mutate.
func returnMutants(file string, line int, n *sitter.Node, src []byte) []lang.MutantSite {
	// return_statement has at most one named child — the returned value.
	if n.NamedChildCount() == 0 {
		return nil
	}
	value := n.NamedChild(0)
	if value == nil {
		return nil
	}
	// Choose the target zero value. If the current expression is literally
	// `null`, swap to `undefined` so the mutant is non-equivalent.
	target := "null"
	if nodeText(value, src) == "null" {
		target = "undefined"
	}
	return []lang.MutantSite{{
		File:        file,
		Line:        line,
		Description: fmt.Sprintf("replace return value with %s", target),
		Operator:    "return_value",
	}}
}

// ifMutants empties an if_statement's consequence (branch_removal).
func ifMutants(file string, line int, n *sitter.Node, _ []byte) []lang.MutantSite {
	body := n.ChildByFieldName("consequence")
	if body == nil {
		return nil
	}
	// Only emit when the consequence actually has content (otherwise
	// there's nothing to remove and the mutant is trivially equivalent).
	if body.NamedChildCount() == 0 {
		return nil
	}
	return []lang.MutantSite{{
		File:        file,
		Line:        line,
		Description: "remove if body",
		Operator:    "branch_removal",
	}}
}

// exprStmtMutants removes a bare call statement (statement_deletion). Only
// expression_statements whose payload is a call_expression qualify — bare
// assignments, let bindings, etc. are left alone because deleting them
// tends to produce un-killable dead-code mutants.
func exprStmtMutants(file string, line int, n *sitter.Node, _ []byte) []lang.MutantSite {
	if n.NamedChildCount() == 0 {
		return nil
	}
	payload := n.NamedChild(0)
	if payload == nil || payload.Type() != "call_expression" {
		return nil
	}
	return []lang.MutantSite{{
		File:        file,
		Line:        line,
		Description: "remove call statement",
		Operator:    "statement_deletion",
	}}
}

// optionalChainMutants emits the optional_chain_removal operator for
// `foo?.bar`. Tree-sitter models optional chains as member_expression
// nodes with an optional_chain child token (a literal `?.`). We detect
// the presence of that child and emit the mutant.
func optionalChainMutants(file string, line int, n *sitter.Node, src []byte) []lang.MutantSite {
	if !hasOptionalChainToken(n, src) {
		return nil
	}
	return []lang.MutantSite{{
		File:        file,
		Line:        line,
		Description: "?. -> .",
		Operator:    "optional_chain_removal",
	}}
}

// hasOptionalChainToken reports whether a member_expression carries the
// `?.` token between its object and property. Different grammar versions
// model this differently (anonymous child vs named `optional_chain`), so
// we look at the literal source text between the object and the property.
func hasOptionalChainToken(n *sitter.Node, src []byte) bool {
	// Fast path: some grammars expose a child whose Type() is literally
	// "optional_chain" or "?.".
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		if c.Type() == "optional_chain" || c.Type() == "?." {
			return true
		}
	}
	// Fallback: inspect the raw bytes between object.EndByte and
	// property.StartByte for the literal `?.` token. tree-sitter stores
	// them as contiguous source, so a simple substring check works.
	obj := n.ChildByFieldName("object")
	prop := n.ChildByFieldName("property")
	if obj == nil || prop == nil {
		return false
	}
	start := obj.EndByte()
	end := prop.StartByte()
	if end <= start || int(end) > len(src) {
		return false
	}
	between := src[start:end]
	for i := 0; i+1 < len(between); i++ {
		if between[i] == '?' && between[i+1] == '.' {
			return true
		}
	}
	return false
}
