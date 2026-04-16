# Multi-language support — follow-ups

Tracked outside `docs/rust-typescript-support.md` (the spec) and
`MULTI_LANGUAGE_SUPPORT.md` (the design) so future changes have a single
visible backlog and so these items don't drift into invisible tech debt.

Filed during the multi-language sign-off on the `feat/multi-language-support`
branch (commits `c4bced2..1a7ac4a`). Parts A through E are complete and all
unit / eval / mixed tests are green. The items below are explicit carve-outs
from that work.

## Deferred evaluation work

### EVAL-5 — Pre-flight calibration (pre-ship)

Spec reference: `docs/rust-typescript-support.md` §EVAL-5.

The plan calls for running the built `diffguard` binary against two
open-source Rust crates (one small, one mid-sized) and two TypeScript
projects (one app, one library), triaging every FAIL/WARN, and recording
the baseline noise rate under a "Baseline noise rate" appendix.

**Status**: not run. This is a human-in-the-loop activity (requires
picking representative repos, curating the triage write-up) rather than
something the agent pipeline can automate, so it was explicitly deferred.

**Exit criteria before declaring Rust/TS support shipped**: <20% noise
rate per language, with the triage notes appended to
`docs/rust-typescript-support.md`.

### EVAL-2 / EVAL-3 MVP carve-outs

The Rust and TypeScript eval harnesses ship the MVP subset:
complexity (pos/neg), sizes function (pos/neg), deps cycle (pos/neg),
mutation kill (pos/neg), and one language-specific mutation operator
(pos/neg). The following sub-cases from the spec are deferred and
called out as in-code TODO blocks at the top of each `eval_test.go`:

- `EVAL-2 sizes (file)` — >500-LOC Rust fixture + negative control.
- `EVAL-2 deps (SDP)` — stable→unstable Rust fixture + reversed
  negative control.
- `EVAL-2 churn` — hot_complex / hot_simple Rust fixtures with seeded
  git history; requires a shell-based git helper so the history isn't
  committed as a nested `.git` dir.
- `EVAL-2 mutation (annotation respect)` — end-to-end run exercising
  `// mutator-disable-func` and `// mutator-disable-next-line` on Rust.
  (Unit-level coverage exists in `mutation_annotate_test.go`.)
- Mirror carve-outs on the TypeScript side in
  `internal/lang/tsanalyzer/eval_test.go`.

These are MVP-ready because the structural shape (fixtures,
`expected.json`, semantic compare) is in place; the missing rows are
more fixture content, not missing pipeline.

## Known QA-flagged limitations

### Rust workspace-crate path resolution

`parseCargoPackageName` returns `""` for a bare `[workspace]` manifest
without a `[package]` section (see
`internal/lang/rustanalyzer/deps_test.go`). Repos whose root
`Cargo.toml` is a pure workspace manifest (common for multi-crate
projects) currently analyze each member crate but do not thread the
workspace root into module-path resolution, which can under-report
cross-crate imports in a workspace.

**Impact**: single-crate repos are unaffected. Workspace repos get a
correct per-crate report but may miss dep edges between sibling crates.

**Fix sketch**: resolve `workspace.members` globs, walk each member's
`Cargo.toml` for its `[package] name`, and union the module-path
registry before running `ScanPackageImports`.

## How to close these out

1. For EVAL-5, pick the calibration repos, run `diffguard` against each,
   triage, and append a "Baseline noise rate" appendix to
   `docs/rust-typescript-support.md`.
2. For the EVAL-2 / EVAL-3 sub-cases, add fixtures under each
   analyzer's `evaldata/` and drop the corresponding TODO lines from
   the header comment in `eval_test.go`.
3. For workspace-crate resolution, extend `ImportResolver` in
   `internal/lang/rustanalyzer/deps.go` and add a workspace fixture to
   the deps test suite.
