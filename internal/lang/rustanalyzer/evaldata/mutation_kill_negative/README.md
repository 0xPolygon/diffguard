# mutation_kill_negative

Same `classify(x)` as mutation_kill_positive but the test suite covers
only one branch. Most Tier-1 mutants survive, dropping the kill rate
below the 90% threshold.

Expected verdict: Mutation Testing FAIL.

Requires `cargo` on PATH — eval_test.go skips cleanly when absent.
