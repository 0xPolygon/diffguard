package tsanalyzer

import (
	"sort"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
)

// complexityImpl implements both lang.ComplexityCalculator and
// lang.ComplexityScorer for TypeScript via tree-sitter. Same reuse
// strategy as the Go and Rust analyzers: the per-file walk is fast enough
// that the churn analyzer shares the full algorithm instead of a lighter
// approximation.
type complexityImpl struct{}

// AnalyzeFile returns per-function cognitive complexity for every function
// overlapping the diff's changed regions.
func (complexityImpl) AnalyzeFile(absPath string, fc diff.FileChange) ([]lang.FunctionComplexity, error) {
	return scoreFile(absPath, fc)
}

// ScoreFile is the ComplexityScorer entry point used by the churn
// analyzer.
func (complexityImpl) ScoreFile(absPath string, fc diff.FileChange) ([]lang.FunctionComplexity, error) {
	return scoreFile(absPath, fc)
}

func scoreFile(absPath string, fc diff.FileChange) ([]lang.FunctionComplexity, error) {
	tree, src, err := parseFile(absPath)
	if err != nil {
		return nil, nil
	}
	defer tree.Close()

	fns := collectFunctions(tree.RootNode(), src)

	var results []lang.FunctionComplexity
	for _, fn := range fns {
		if !fc.OverlapsRange(fn.startLine, fn.endLine) {
			continue
		}
		results = append(results, lang.FunctionComplexity{
			FunctionInfo: lang.FunctionInfo{
				File:    fc.Path,
				Line:    fn.startLine,
				EndLine: fn.endLine,
				Name:    fn.name,
			},
			Complexity: cognitiveComplexity(fn.body, src),
		})
	}

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Line != results[j].Line {
			return results[i].Line < results[j].Line
		}
		return results[i].Name < results[j].Name
	})
	return results, nil
}

// cognitiveComplexity computes the TypeScript cognitive-complexity score
// for the body of a function. Per the design doc:
//
//   - Base +1 on: if_statement, for_statement, for_in_statement,
//     for_of_statement, while_statement, switch_statement, try_statement,
//     ternary_expression.
//   - +1 per catch_clause.
//   - +1 per else branch.
//   - +1 per case clause with content (empty fall-through cases don't count).
//   - +1 per `.catch(` promise-chain call (string-match on the method name).
//   - +1 per operator-sequence switch inside &&/|| chains.
//   - Do NOT count: optional chaining `?.`, nullish coalescing `??`,
//     `await` alone, `async` keyword, stream method calls.
//   - Nesting penalty: +1 per nesting level when descending into bodies of
//     scope-introducing constructs.
//
// A nil body (abstract method, overload signature) has complexity 0.
func cognitiveComplexity(body *sitter.Node, src []byte) int {
	if body == nil {
		return 0
	}
	return walkComplexity(body, src, 0)
}

// walkComplexity is the recursive heart of the algorithm. `nesting` is the
// depth penalty applied when an increment fires.
func walkComplexity(n *sitter.Node, src []byte, nesting int) int {
	if n == nil {
		return 0
	}
	total := 0
	switch n.Type() {
	case "if_statement":
		total += 1 + nesting
		total += conditionLogicalOps(n.ChildByFieldName("condition"))
		// else branch (when present) contributes +1 plus its own body walk.
		if alt := n.ChildByFieldName("alternative"); alt != nil {
			total += 1
		}
		total += walkChildrenWithNesting(n, src, nesting)
		return total
	case "for_statement", "for_in_statement", "for_of_statement",
		"while_statement":
		total += 1 + nesting
		total += conditionLogicalOps(n.ChildByFieldName("condition"))
		total += walkChildrenWithNesting(n, src, nesting)
		return total
	case "switch_statement":
		total += 1 + nesting
		total += countNonEmptyCases(n)
		total += walkChildrenWithNesting(n, src, nesting)
		return total
	case "try_statement":
		total += 1 + nesting
		total += walkChildrenWithNesting(n, src, nesting)
		return total
	case "catch_clause":
		total += 1
		total += walkChildrenWithNesting(n, src, nesting)
		return total
	case "ternary_expression":
		total += 1 + nesting
		total += walkChildrenWithNesting(n, src, nesting)
		return total
	case "call_expression":
		// Promise-chain .catch() contributes +1 per occurrence.
		if isDotCatchCall(n, src) {
			total += 1
		}
		// Keep descending to count stuff inside the arguments.
		for i := 0; i < int(n.ChildCount()); i++ {
			total += walkComplexity(n.Child(i), src, nesting)
		}
		return total
	case "arrow_function", "function_expression", "function_declaration",
		"method_definition", "generator_function", "generator_function_declaration":
		// A nested function has its own complexity tracked separately (as a
		// distinct entry from collectFunctions). Don't add the inner
		// complexity to the outer function.
		return 0
	}

	// Descend into children without adjusting nesting.
	for i := 0; i < int(n.ChildCount()); i++ {
		total += walkComplexity(n.Child(i), src, nesting)
	}
	return total
}

// walkChildrenWithNesting recurses into the sub-trees that belong to the
// construct at `n`, bumping nesting only for body-like children. This
// mirrors the Rust analyzer's behavior.
func walkChildrenWithNesting(n *sitter.Node, src []byte, nesting int) int {
	total := 0
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		fieldName := n.FieldNameForChild(i)
		switch fieldName {
		case "condition", "value", "left", "right":
			// Conditions / operands stay at the current nesting. Their
			// sub-expressions (nested ternaries, logical chains) are
			// counted via their own node-type cases.
			total += walkComplexity(c, src, nesting)
		case "body", "consequence", "alternative":
			total += walkComplexity(c, src, nesting+1)
		default:
			total += walkComplexity(c, src, nesting)
		}
	}
	return total
}

// countNonEmptyCases walks a switch_statement's body and returns the
// number of case clauses that contain at least one statement. Empty
// fall-through cases (`case 1:` with no body that falls into the next
// arm) don't count, matching the design doc.
//
// In tree-sitter-typescript, a switch body has `switch_case` children with
// a value field and a body field (the body field's NamedChildCount tells
// us whether the case has content). `default` clauses are modeled as
// `switch_default`.
func countNonEmptyCases(switchNode *sitter.Node) int {
	body := switchNode.ChildByFieldName("body")
	if body == nil {
		return 0
	}
	count := 0
	for i := 0; i < int(body.NamedChildCount()); i++ {
		c := body.NamedChild(i)
		if c == nil {
			continue
		}
		if c.Type() != "switch_case" && c.Type() != "switch_default" {
			continue
		}
		if caseHasContent(c) {
			count++
		}
	}
	return count
}

// caseHasContent returns true when a switch_case/switch_default has at
// least one statement-like named child beyond the value expression.
func caseHasContent(c *sitter.Node) bool {
	for i := 0; i < int(c.NamedChildCount()); i++ {
		child := c.NamedChild(i)
		if child == nil {
			continue
		}
		// Skip the case's value expression — grammars expose it as the
		// first named child for switch_case. We count anything else.
		fname := c.FieldNameForChild(i)
		if fname == "value" {
			continue
		}
		return true
	}
	return false
}

// isDotCatchCall reports whether a call_expression is `something.catch(...)`
// — i.e. a promise-chain `.catch` invocation. We match on the member
// expression's property name being literally `catch`. The spec calls out
// string-matching on the identifier explicitly to avoid CST depth tuning.
func isDotCatchCall(call *sitter.Node, src []byte) bool {
	fn := call.ChildByFieldName("function")
	if fn == nil || fn.Type() != "member_expression" {
		return false
	}
	prop := fn.ChildByFieldName("property")
	if prop == nil {
		return false
	}
	return nodeText(prop, src) == "catch"
}

// conditionLogicalOps returns the operator-switch count for the chain of
// `&&` / `||` operators inside a condition. Matches the Rust algorithm: a
// run of the same operator counts as 1, each switch to the other adds 1.
func conditionLogicalOps(cond *sitter.Node) int {
	if cond == nil {
		return 0
	}
	ops := flattenLogicalOps(cond)
	if len(ops) == 0 {
		return 0
	}
	count := 1
	for i := 1; i < len(ops); i++ {
		if ops[i] != ops[i-1] {
			count++
		}
	}
	return count
}

// flattenLogicalOps collects the `&&` / `||` sequence from a
// binary_expression tree, left-to-right. Non-logical binary ops stop the
// recursion.
//
// Tree-sitter TypeScript wraps `if (cond)` conditions in a
// `parenthesized_expression` (`( binary_expression )`). We strip that
// wrapper when we first see it so a condition chain like
// `if (a && b || c)` is traversed as the inner binary tree.
//
// Tree-sitter TypeScript models `a && b` as
//
//	(binary_expression left: ... operator: "&&" right: ...)
//
// — the operator is an anonymous child whose Type() is the operator token.
func flattenLogicalOps(n *sitter.Node) []string {
	n = unwrapParens(n)
	if n == nil || n.Type() != "binary_expression" {
		return nil
	}
	op := n.ChildByFieldName("operator")
	if op == nil {
		return nil
	}
	opText := op.Type()
	if opText != "&&" && opText != "||" {
		return nil
	}
	var out []string
	out = append(out, flattenLogicalOps(n.ChildByFieldName("left"))...)
	out = append(out, opText)
	out = append(out, flattenLogicalOps(n.ChildByFieldName("right"))...)
	return out
}

// unwrapParens strips a leading parenthesized_expression wrapper so
// condition handling doesn't have to special-case the if/while grammar
// shape. Returns n unchanged when no wrapping is present.
func unwrapParens(n *sitter.Node) *sitter.Node {
	for n != nil && n.Type() == "parenthesized_expression" {
		// The inner expression is the first (and only) named child.
		if n.NamedChildCount() == 0 {
			return n
		}
		n = n.NamedChild(0)
	}
	return n
}
