# deps_cycle_negative

Negative control: same `a` and `b` packages but both import a shared
`types` module instead of each other, breaking the cycle.

Expected verdict: Dependency Structure PASS.
