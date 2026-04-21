# mutation_kill_positive

Well-tested `classify(x)` with boundary + sign + zero coverage in
test.mjs. Tier-1 operators (conditional_boundary, negate_conditional,
math_operator, return_value) should be killed.

Expected verdict: Mutation Testing PASS; Tier-1 kill rate ≥ 90%.

Requires `node` 22.6+ on PATH (uses `--experimental-strip-types` to
import `.ts` directly). eval_test.go skips cleanly if node is absent.
