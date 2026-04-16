package rustanalyzer

import (
	"fmt"
	"sort"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
)

// mutantGeneratorImpl implements lang.MutantGenerator for Rust. It emits
// canonical operators (conditional_boundary, negate_conditional,
// math_operator, return_value, boolean_substitution, branch_removal,
// statement_deletion) plus the Rust-specific operators defined in the
// design doc: unwrap_removal, some_to_none, question_mark_removal.
//
// `incdec` is deliberately absent — Rust has no `++`/`--` operators.
type mutantGeneratorImpl struct{}

// GenerateMutants walks the CST and emits a MutantSite for each qualifying
// node on a changed, non-disabled line. The output is deterministic: we
// sort by (line, operator, description) before returning.
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
// operator return nil — the walker simply moves on.
func mutantsFor(file string, line int, n *sitter.Node, src []byte) []lang.MutantSite {
	switch n.Type() {
	case "binary_expression":
		return binaryMutants(file, line, n, src)
	case "boolean_literal":
		return boolMutants(file, line, n, src)
	case "return_expression":
		return returnMutants(file, line, n, src)
	case "if_expression":
		return ifMutants(file, line, n, src)
	case "expression_statement":
		return exprStmtMutants(file, line, n, src)
	case "call_expression":
		return unwrapMutants(file, line, n, src)
	case "try_expression":
		return tryMutants(file, line, n)
	case "scoped_identifier", "identifier":
		return nil
	}
	return nil
}

// binaryMutants covers conditional_boundary, negate_conditional, and
// math_operator. Shape: (binary_expression operator: "<op>" ...). Skip
// unhandled operators so we don't mutate e.g. bit-shift tokens.
func binaryMutants(file string, line int, n *sitter.Node, _ []byte) []lang.MutantSite {
	opNode := n.ChildByFieldName("operator")
	if opNode == nil {
		return nil
	}
	op := opNode.Type()
	replacements := map[string]string{
		">":  ">=",
		"<":  "<=",
		">=": ">",
		"<=": "<",
		"==": "!=",
		"!=": "==",
		"+":  "-",
		"-":  "+",
		"*":  "/",
		"/":  "*",
	}
	newOp, ok := replacements[op]
	if !ok {
		return nil
	}
	return []lang.MutantSite{{
		File:        file,
		Line:        line,
		Description: fmt.Sprintf("%s -> %s", op, newOp),
		Operator:    binaryOperatorName(op, newOp),
	}}
}

// binaryOperatorName classifies a source/target operator pair into one of
// the canonical tier-1 operator names. The classification matches the Go
// analyzer so operator stats stay comparable across languages.
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
	return op == "==" || op == "!="
}

func isMath(op string) bool {
	return op == "+" || op == "-" || op == "*" || op == "/"
}

// boolMutants flips true <-> false. Tree-sitter exposes boolean literals
// as boolean_literal whose Type() is literally "boolean_literal"; the
// source text is either "true" or "false".
func boolMutants(file string, line int, n *sitter.Node, src []byte) []lang.MutantSite {
	text := nodeText(n, src)
	if text != "true" && text != "false" {
		return nil
	}
	flipped := "true"
	if text == "true" {
		flipped = "false"
	}
	return []lang.MutantSite{{
		File:        file,
		Line:        line,
		Description: fmt.Sprintf("%s -> %s", text, flipped),
		Operator:    "boolean_substitution",
	}}
}

// returnMutants covers two Rust-specific cases under the canonical
// return_value operator name: `Default::default()` substitution and
// `Some(x) -> None` (an optional-return swap called some_to_none in the
// design doc).
//
// A bare `return;` (unit return) has no expression to mutate, so we skip.
func returnMutants(file string, line int, n *sitter.Node, src []byte) []lang.MutantSite {
	// A return_expression has at most one named child — the returned value.
	var value *sitter.Node
	for i := 0; i < int(n.NamedChildCount()); i++ {
		value = n.NamedChild(i)
		break
	}
	if value == nil {
		// `return;` — nothing to mutate.
		return nil
	}

	var out []lang.MutantSite
	if someVal, ok := matchSome(value, src); ok {
		out = append(out, lang.MutantSite{
			File:        file,
			Line:        line,
			Description: fmt.Sprintf("Some(%s) -> None", someVal),
			Operator:    "some_to_none",
		})
	}
	out = append(out, lang.MutantSite{
		File:        file,
		Line:        line,
		Description: "replace return value with Default::default()",
		Operator:    "return_value",
	})
	return out
}

// matchSome reports whether value is a `Some(expr)` call expression and
// returns the inner expression text if so. We use this to generate a
// descriptive mutant description ("Some(x) -> None") rather than a generic
// "return_value" blurb. Tree-sitter parses `Some(x)` as a call_expression
// whose function is the identifier `Some`.
func matchSome(value *sitter.Node, src []byte) (string, bool) {
	if value == nil || value.Type() != "call_expression" {
		return "", false
	}
	fn := value.ChildByFieldName("function")
	if fn == nil || nodeText(fn, src) != "Some" {
		return "", false
	}
	args := value.ChildByFieldName("arguments")
	if args == nil {
		return "", false
	}
	// Grab the text between the parens, trimmed.
	argText := nodeText(args, src)
	argText = strings.TrimPrefix(argText, "(")
	argText = strings.TrimSuffix(argText, ")")
	return strings.TrimSpace(argText), true
}

// ifMutants empties an if_expression body (branch_removal).
func ifMutants(file string, line int, n *sitter.Node, _ []byte) []lang.MutantSite {
	body := n.ChildByFieldName("consequence")
	if body == nil || body.NamedChildCount() == 0 {
		return nil
	}
	return []lang.MutantSite{{
		File:        file,
		Line:        line,
		Description: "remove if body",
		Operator:    "branch_removal",
	}}
}

// exprStmtMutants deletes a bare call statement — the Rust analog of the
// Go statement_deletion case. A semicolon-terminated expression whose
// payload is a call_expression is the canonical candidate; other bare
// statements (assignments, let bindings) are left alone because deleting
// them tends to produce un-killable dead-code mutants.
func exprStmtMutants(file string, line int, n *sitter.Node, _ []byte) []lang.MutantSite {
	var payload *sitter.Node
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if c == nil {
			continue
		}
		payload = c
		break
	}
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

// unwrapMutants emits the Rust-specific unwrap_removal operator: a method
// call whose name is `unwrap` or `expect` has its receiver preserved but
// the trailing `.unwrap()` / `.expect(...)` stripped. Tree-sitter exposes
// `foo.unwrap()` as:
//
//	(call_expression
//	  function: (field_expression value: ... field: (field_identifier)))
//
// We look for that shape with field name "unwrap" or "expect".
func unwrapMutants(file string, line int, n *sitter.Node, src []byte) []lang.MutantSite {
	fn := n.ChildByFieldName("function")
	if fn == nil || fn.Type() != "field_expression" {
		return nil
	}
	field := fn.ChildByFieldName("field")
	if field == nil {
		return nil
	}
	name := nodeText(field, src)
	if name != "unwrap" && name != "expect" {
		return nil
	}
	return []lang.MutantSite{{
		File:        file,
		Line:        line,
		Description: fmt.Sprintf("strip .%s()", name),
		Operator:    "unwrap_removal",
	}}
}

// tryMutants emits the question_mark_removal operator for try expressions
// (`expr?`). Tree-sitter models `foo()?` as (try_expression ...), making
// detection straightforward.
func tryMutants(file string, line int, n *sitter.Node) []lang.MutantSite {
	// A try_expression always has exactly one inner expression; if that's
	// missing we have malformed input, so bail.
	if n.NamedChildCount() == 0 {
		return nil
	}
	return []lang.MutantSite{{
		File:        file,
		Line:        line,
		Description: "strip trailing ?",
		Operator:    "question_mark_removal",
	}}
}
