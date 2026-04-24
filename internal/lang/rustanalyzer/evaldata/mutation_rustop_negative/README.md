# mutation_rustop_negative

Negative control for mutation_rustop_positive. `wrap(x)` returns
`Some(x * 2)` but the test never inspects the Option variant, so the
`some_to_none` mutant survives and the Tier-1 kill rate falls below
threshold.

Expected verdict: Mutation Testing FAIL — confirms the operator
generates meaningful mutants whose signal depends on test quality.

Requires `cargo` on PATH — eval_test.go skips cleanly when absent.
