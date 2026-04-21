# mutation_kill_positive

Well-tested `classify(x)` with boundary + sign coverage. Tier-1 mutation
operators (conditional_boundary, negate_conditional, math_operator,
return_value) should be killed by the inline `tests` module.

Expected verdict: Mutation Testing PASS; Tier-1 kill rate ≥ 90%.

Requires `cargo` on PATH — eval_test.go skips cleanly when absent.
