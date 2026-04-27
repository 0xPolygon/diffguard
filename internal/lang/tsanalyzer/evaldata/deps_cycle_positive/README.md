# deps_cycle_positive

Seeded issue: `src/a/index.ts` imports from `../b` and `src/b/index.ts`
imports from `../a`, producing a 2-cycle in the internal dependency
graph.

Expected verdict: Dependency Structure FAIL.
