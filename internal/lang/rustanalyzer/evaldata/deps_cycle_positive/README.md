# deps_cycle_positive

Seeded issue: `src/a/mod.rs` imports `crate::b::b_fn` while
`src/b/mod.rs` imports `crate::a::a_fn`, producing a 2-cycle in the
internal dependency graph.

Expected verdict: Dependency Structure FAIL with a cycle finding.
