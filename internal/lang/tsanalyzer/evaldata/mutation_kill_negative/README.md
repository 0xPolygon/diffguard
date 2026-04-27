# mutation_kill_negative

Same `classify(x)` as mutation_kill_positive, but test.mjs only exercises
the positive branch. Most Tier-1 mutants survive.

Expected verdict: Mutation Testing FAIL.

Requires `node` 22.6+ on PATH. eval_test.go skips cleanly if node is absent.
