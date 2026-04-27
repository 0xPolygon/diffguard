# mutation_tsop_negative

Negative control: same code as mutation_tsop_positive but tests don't
distinguish strict equality from loose or nullish coalescing from
logical-or. The operators fire, mutants survive, Tier-1 drops below
threshold.

Expected verdict: Mutation Testing FAIL — confirms the operators
generate meaningful mutants whose signal depends on test quality.

Requires `node` 22.6+ on PATH.
