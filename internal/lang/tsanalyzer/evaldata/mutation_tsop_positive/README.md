# mutation_tsop_positive

Exercises TS-specific operators `strict_equality` and
`nullish_to_logical_or`. test.mjs asserts inputs that distinguish
`===`/`==` (0 vs. false) and `??`/`||` (0 as a valid value), so the
mutants are killed.

Expected verdict: Mutation Testing PASS.

Requires `node` 22.6+ on PATH.
