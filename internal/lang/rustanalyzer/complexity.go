package rustanalyzer

import (
	"sort"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
)

// complexityImpl implements both lang.ComplexityCalculator and
// lang.ComplexityScorer for Rust. Tree-sitter walks are fast enough that we
// use the same full-cognitive-complexity algorithm for both interfaces —
// matching the Go analyzer's reuse strategy.
type complexityImpl struct{}

// AnalyzeFile returns per-function cognitive complexity for every function
// that overlaps the diff's changed regions.
func (complexityImpl) AnalyzeFile(absPath string, fc diff.FileChange) ([]lang.FunctionComplexity, error) {
	return scoreFile(absPath, fc)
}

// ScoreFile is the ComplexityScorer entry point used by the churn analyzer.
// It shares an implementation with AnalyzeFile; the per-file cost is small
// enough that a separate "faster" scorer would not be worth the divergence.
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

// cognitiveComplexity computes the Rust cognitive-complexity score for the
// body block of a function. The algorithm, per the design doc:
//
//   - +1 base on each control-flow construct (if, while, for, loop, match,
//     if let, while let)
//   - +1 per guarded match arm (the `if` guard in `pattern if cond => ...`)
//   - +1 per logical-op token-sequence switch (a `||` that follows an `&&`
//     chain or vice versa)
//   - +1 nesting penalty for each scope-introducing ancestor
//
// The `?` operator and `unsafe` blocks do NOT contribute — they're
// error-propagation and safety annotations respectively, not cognitive
// control flow.
//
// A nil body (trait method with no default) has complexity 0.
func cognitiveComplexity(body *sitter.Node, src []byte) int {
	if body == nil {
		return 0
	}
	return walkComplexity(body, src, 0)
}

// walkComplexity is the recursive heart of the algorithm. `nesting` is the
// depth penalty to apply when an increment fires — it goes up every time
// we descend into a control-flow construct and does NOT go up for
// non-control-flow blocks like `unsafe`.
func walkComplexity(n *sitter.Node, src []byte, nesting int) int {
	if n == nil {
		return 0
	}
	total := 0
	switch n.Type() {
	case "if_expression":
		total += 1 + nesting
		total += conditionLogicalOps(n.ChildByFieldName("condition"))
		total += walkChildrenWithNesting(n, src, nesting)
		return total
	case "while_expression":
		total += 1 + nesting
		total += conditionLogicalOps(n.ChildByFieldName("condition"))
		total += walkChildrenWithNesting(n, src, nesting)
		return total
	case "for_expression":
		total += 1 + nesting
		total += walkChildrenWithNesting(n, src, nesting)
		return total
	case "loop_expression":
		total += 1 + nesting
		total += walkChildrenWithNesting(n, src, nesting)
		return total
	case "match_expression":
		total += 1 + nesting
		total += countGuardedArms(n)
		total += walkChildrenWithNesting(n, src, nesting)
		return total
	case "if_let_expression":
		// Older grammar versions model `if let` as a distinct node; current
		// versions fold it into if_expression with a `let_condition` child.
		// We cover both so the walker is resilient across grammar updates.
		total += 1 + nesting
		total += walkChildrenWithNesting(n, src, nesting)
		return total
	case "while_let_expression":
		total += 1 + nesting
		total += walkChildrenWithNesting(n, src, nesting)
		return total
	case "closure_expression":
		// A closure body introduces its own nesting context and doesn't
		// inherit the outer nesting depth — same treatment as Go's FuncLit.
		if body := n.ChildByFieldName("body"); body != nil {
			total += walkComplexity(body, src, 0)
		}
		return total
	case "function_item":
		// Nested function declarations are treated as separate functions
		// for the size extractor and should not contribute here.
		return 0
	}

	// Descend into children without adding nesting for plain blocks,
	// expressions, statements, etc.
	for i := 0; i < int(n.ChildCount()); i++ {
		total += walkComplexity(n.Child(i), src, nesting)
	}
	return total
}

// walkChildrenWithNesting recurses into the subtrees whose bodies belong to
// the construct at `n`. We identify those by looking at `body`, `alternative`
// ('else' branch), and `consequence` fields where present; other children
// (the condition expression, the header) keep the current nesting level so
// logical-op counting doesn't get a bonus point for being inside an `if`.
func walkChildrenWithNesting(n *sitter.Node, src []byte, nesting int) int {
	total := 0
	// Tree-sitter exposes the sub-trees we want via named fields. Any
	// field we haven't handled explicitly is walked as a body for safety.
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		fieldName := n.FieldNameForChild(i)
		switch fieldName {
		case "condition", "value", "pattern", "type":
			// Condition expressions stay at the current nesting: a && chain
			// inside an `if` is already being counted by conditionLogicalOps;
			// re-descending here would double-count.
			total += walkComplexity(c, src, nesting)
		case "body", "consequence", "alternative":
			total += walkComplexity(c, src, nesting+1)
		default:
			total += walkComplexity(c, src, nesting)
		}
	}
	return total
}

// countGuardedArms walks the arms of a match_expression and counts how many
// have an `if` guard. Grammar shape:
//
//	(match_expression
//	  value: ...
//	  body: (match_block
//	          (match_arm pattern: (...) [(match_arm_guard ...)] value: (...))))
//
// We look for any child named `match_arm` whose subtree includes a
// `match_arm_guard` node. This is grammar-robust: older variants nest the
// guard directly as an `if` keyword sibling, newer ones wrap it in an
// explicit guard node — both show up under the arm when we walk.
func countGuardedArms(match *sitter.Node) int {
	block := match.ChildByFieldName("body")
	if block == nil {
		return 0
	}
	count := 0
	walk(block, func(n *sitter.Node) bool {
		if n.Type() == "match_arm" {
			if hasGuard(n) {
				count++
			}
			// Descend: arms can contain nested match expressions.
			return true
		}
		return true
	})
	return count
}

// hasGuard reports whether a match_arm node carries an `if` guard.
func hasGuard(arm *sitter.Node) bool {
	for i := 0; i < int(arm.ChildCount()); i++ {
		c := arm.Child(i)
		if c == nil {
			continue
		}
		if c.Type() == "match_arm_guard" {
			return true
		}
	}
	return false
}

// conditionLogicalOps returns the operator-switch count for the chain of
// `&&`/`||` operators directly inside an `if`/`while` condition. See
// countLogicalOps in the Go analyzer for the algorithm — a run of the same
// operator counts as 1, each switch to the other adds 1.
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

// flattenLogicalOps collects the `&&` / `||` operator sequence of a
// binary_expression tree, left-to-right. Non-logical binary ops stop the
// recursion (their operands don't contribute to the logical-chain count).
//
// Tree-sitter Rust models `a && b` as
//
//	(binary_expression left: ... operator: "&&" right: ...)
//
// — the operator is an anonymous child whose type literal is the operator
// symbol. We discover it via ChildByFieldName("operator").
func flattenLogicalOps(n *sitter.Node) []string {
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
