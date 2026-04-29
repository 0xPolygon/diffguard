# deps_cycle_negative

Negative control: same modules as deps_cycle_positive but both depend on
a shared `types` module instead of each other, breaking the cycle.

Expected verdict: Dependency Structure PASS, no cycle findings.
