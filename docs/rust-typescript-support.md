# Rust + TypeScript support — implementation checklist

This is the execution checklist for adding Rust and TypeScript analyzer support to diffguard, sized so a single `diffguard` run on a mixed-language repo reports both languages side by side.

For the deep technical decisions (interface shapes, tree-sitter vs. runtime parsers, mutation isolation strategy, per-language parser notes), see `../MULTI_LANGUAGE_SUPPORT.md`. This checklist references that doc rather than duplicating it.

## Scope

- **In scope**: Rust, TypeScript (including `.tsx`). All five analyzers (complexity, sizes, deps, churn, mutation). Multi-language single-invocation support.
- **Out of scope**: Java, Python, plain JavaScript-only (JS works incidentally under the TS grammar but the TS path is the supported one). A `--test-command` override flag (add only if a fixture needs it).
- **Left alone**: Go keeps `go/ast`. Only its packaging moves — the parser does not.

## Legend

- **[F]** foundation work (blocks both languages)
- **[O]** orchestration (the "simultaneous" piece)
- **[R]** Rust analyzer
- **[T]** TypeScript analyzer
- **[X]** cross-cutting (docs, CI, evals)
- **[EVAL]** correctness-evidence work (proves diffguard catches real issues)

Parts R and T are disjoint and can be worked in parallel once F and O land.

---

## Part A — Foundation (shared, one-time) [F]

Repo reorganization so Go becomes one of several registered languages. Every step leaves `go test ./...` green.

### A1. Language abstraction layer

- [ ] Add `github.com/smacker/go-tree-sitter` (and sub-packages for `rust`, `typescript`, `tsx`) to `go.mod`.
- [ ] Create `internal/lang/lang.go` with the 9 sub-interfaces (`FileFilter`, `FunctionExtractor`, `ComplexityCalculator`, `ComplexityScorer`, `ImportResolver`, `MutantGenerator`, `MutantApplier`, `AnnotationScanner`, `TestRunner`) and the top-level `Language` interface — shapes from `MULTI_LANGUAGE_SUPPORT.md` §Interface Definitions.
- [ ] Create `internal/lang/registry.go` with `Register(Language)`, `Get(name string)`, and `All()`.
- [ ] Create `internal/lang/detect.go`. Detection rules from `MULTI_LANGUAGE_SUPPORT.md` §Language detection. Return order must be deterministic (sorted by name) so downstream report ordering is stable.
- [ ] Unit tests for registry (register/get/all, duplicate registration is an error) and detection (each manifest file → correct language, multi-language repos return multiple, empty repo returns empty).

### A2. Extract Go → `goanalyzer`

- [ ] Create `internal/lang/goanalyzer/` package.
- [ ] Move the three duplicate `funcName` helpers (`sizes.go`, `complexity.go`, `churn.go`) into `internal/lang/goanalyzer/parse.go` as a single helper.
- [ ] Implement each of the 9 interfaces in `goanalyzer/` (one file per concern; filenames from `MULTI_LANGUAGE_SUPPORT.md` §Resulting directory structure).
- [ ] `goanalyzer/goanalyzer.go` exposes a `Language` struct and an `init()` that calls `lang.Register(&Language{})`.
- [ ] Blank-import `_ "github.com/0xPolygon/diffguard/internal/lang/goanalyzer"` in `cmd/diffguard/main.go`.

### A3. Parameterize the diff parser

- [ ] Replace `isAnalyzableGoFile` (`internal/diff/diff.go:175-177`) with a `FileFilter` parameter.
- [ ] Replace hardcoded `--'*.go'` arg (`internal/diff/diff.go:92`) with globs from `FileFilter.DiffGlobs`.
- [ ] Replace the `+++` handler's `.go`/`_test.go` check (`internal/diff/diff.go:201-208`) with `FileFilter.IsTestFile` + extension check.
- [ ] Update `Parse()` and `CollectPaths()` signatures; callers in `cmd/diffguard/main.go` pass the appropriate filter.
- [ ] Keep `parseUnifiedDiff` and `parseHunkHeader` untouched — they're already language-agnostic.

### A4. Route existing analyzers through the interface

- [ ] `internal/complexity/complexity.go`: take a `lang.ComplexityCalculator` parameter, delete the embedded AST walk, call `calc.AnalyzeFile(...)` instead.
- [ ] `internal/sizes/sizes.go`: take a `lang.FunctionExtractor`; delegate.
- [ ] `internal/churn/churn.go`: take a `lang.ComplexityScorer`; delete the simplified `computeComplexity` duplicate; keep `git log --oneline --follow` counting (language-agnostic).
- [ ] `internal/deps/`: split into `graph.go` (pure graph math — cycles, afferent/efferent coupling, instability, SDP) and `deps.go` (orchestration taking `lang.ImportResolver`).
- [ ] `internal/mutation/`: route `Analyze` through `MutantGenerator`, `MutantApplier`, `AnnotationScanner`, `TestRunner`. `tiers.go` stays put; `operatorTier` gets new entries for Rust/TS operators (TBD in R/T phases).

### A5. Regression gate

- [ ] `go test ./...` green.
- [ ] `diffguard` binary on a self-diff of this repo produces byte-identical output before and after the reorg (record the baseline first).
- [ ] Wall-clock regression <5% on the self-diff.

---

## Part B — Multi-language orchestration [O]

The "simultaneous" requirement. Lands after A, before R and T.

### B1. CLI

- [ ] Add `--language` flag to `cmd/diffguard/main.go`. Default empty → auto-detect. Accepts comma-separated values (`--language rust,typescript`).
- [ ] Error messages cite the detected manifest files to help users debug "why did you pick that language".

### B2. Orchestration loop

- [ ] In `run()` (currently `main.go:79-102`), resolve the language set:
  - [ ] If `--language` empty: call `lang.Detect(repoPath)`.
  - [ ] Else: split the flag and call `lang.Get()` for each; unknown names are a hard error.
  - [ ] Empty language set is a hard error with a clear message ("no supported language detected; pass --language to override").
- [ ] For each resolved language, call `diff.Parse(repoPath, baseBranch, language.FileFilter())` → per-language `diff.Result`.
- [ ] For each `(language, Result)` with non-empty `Files`, run the full analyzer pipeline using the language's interfaces.
- [ ] Merge sections from all languages into the single `report.Report`. No concurrency at this layer — analyzers already parallelize where it matters.

### B3. Section naming

- [ ] Section names are suffixed `[<lang>]` (e.g., `Complexity [rust]`, `Mutation [typescript]`). `report.Section.Name` is already `string`, so no struct change.
- [ ] Text output groups by language first, then metric, so mixed reports stay readable.
- [ ] JSON output is stable: sections ordered `(language, metric)` lexicographically.

### B4. Empty-languages behavior

- [ ] If a detected language has no changed files in the diff, it produces no sections (no empty PASS rows). This matches existing Go behavior (`No Go files found.` early return generalizes to "No \<lang\> files found." per language, collapsing to the existing message when only one language is present).

### B5. Exit-code aggregation

- [ ] `checkExitCode` unchanged: it already takes a merged `Report` and returns the worst severity. Add a test that a FAIL in any language escalates the whole run.

### B6. Mixed-repo smoke test

- [ ] `cmd/diffguard/main_test.go` gains a test using a temp git repo with a Go file and stub Rust/TS files: run `main()` and assert all three language sections appear. (The Rust/TS analyzer impls are stubs at this point — they register, they return empty results. The point of this test is orchestration, not analysis.)

---

## Part C — Rust analyzer [R]

`internal/lang/rustanalyzer/`. See `MULTI_LANGUAGE_SUPPORT.md` §Rust for parser, complexity, import, and mutation notes.

### C0. Research prerequisites

- [ ] Confirm `github.com/smacker/go-tree-sitter/rust` grammar versions support the Rust edition(s) we care about.
- [ ] Decide: integration-test crates under `tests/` treated as test files? Inline `#[cfg(test)] mod tests { ... }` treated as live code? (Design doc recommends: `tests/` = test files, inline modules = live code ignored during analysis.)

### C1. FileFilter

- [ ] `.rs` extension. `IsTestFile`: any path segment equal to `tests`.
- [ ] `DiffGlobs`: `*.rs`.
- [ ] Tests: fixtures include `src/lib.rs`, `tests/integration.rs`, `src/foo/bar.rs`; assert expected inclusions/exclusions.

### C2. FunctionExtractor

- [ ] Tree-sitter query for `function_item`, `impl_item` → `function_item` (methods), `trait_item` → default methods.
- [ ] Name extraction: standalone `fn foo` → `foo`; `impl Type { fn bar }` → `Type::bar`; `impl Trait for Type { fn baz }` → `Type::baz`.
- [ ] Line range: node start/end lines. File line count from byte count.
- [ ] Filter to functions overlapping `FileChange.Regions`.
- [ ] Tests: each function form, filtering, nested functions (treated as separate).

### C3. ComplexityCalculator + ComplexityScorer

- [ ] Base +1 on: `if_expression`, `while_expression`, `for_expression`, `loop_expression`, `match_expression`, `if_let_expression`, `while_let_expression`.
- [ ] +1 per arm of `match_expression` with a guard (the `if` in `pattern if cond =>`).
- [ ] +1 per logical-op token sequence change inside a binary_expression chain (`&&` / `||`).
- [ ] +1 per nesting level for each scope-introducing ancestor.
- [ ] Do **not** count: `?` operator, `unsafe` blocks.
- [ ] `ComplexityScorer` reuses `ComplexityCalculator` (fast enough).
- [ ] Tests: empty fn (0), `match` with N guarded arms (N), nested `if let` inside `for`, logical chains.

### C4. ImportResolver

- [ ] `DetectModulePath`: parse `Cargo.toml` `[package] name`.
- [ ] `ScanPackageImports`: find `use_declaration` nodes. Internal iff the path starts with `crate::`, `self::`, or `super::`. Also treat `mod foo;` declarations as an edge to the child module.
- [ ] Map discovered paths back to package directories so the graph uses directory-level nodes consistent with Go's behavior.
- [ ] Tests: crate root detection, relative-path resolution (`super::foo`), external imports filtered out.

### C5. AnnotationScanner

- [ ] Scan `line_comment` tokens for `mutator-disable-next-line` and `mutator-disable-func`.
- [ ] Function ranges sourced from C2 so `mutator-disable-func` can expand to every line in the fn.
- [ ] Tests: next-line, func-wide, unrelated comments ignored, disabled-line map is complete.

### C6. MutantGenerator

- [ ] Canonical operators (names from `MULTI_LANGUAGE_SUPPORT.md` §MutantGenerator):
  - [ ] `conditional_boundary`: `>` / `>=` / `<` / `<=` swaps.
  - [ ] `negate_conditional`: `==` / `!=` swap; relational flips.
  - [ ] `math_operator`: `+` / `-`, `*` / `/` swaps.
  - [ ] `return_value`: replace return with `Default::default()` / `None` when the return type is an `Option` / unit.
  - [ ] `boolean_substitution`: `true` / `false` swap.
  - [ ] `branch_removal`: empty `if` body.
  - [ ] `statement_deletion`: remove bare expression statements.
- [ ] Skip `incdec` (Rust has no `++` / `--`).
- [ ] Rust-specific additions:
  - [ ] `unwrap_removal` (Tier 1 via `operatorTier` override): strip `.unwrap()` / `.expect(...)`. Register in `internal/mutation/tiers.go`.
  - [ ] `some_to_none` (Tier 1): `Some(x)` → `None`.
  - [ ] `question_mark_removal` (Tier 2): strip trailing `?`. Register in tiers.
- [ ] Filter mutants to changed regions; exclude disabled lines.
- [ ] Tests: each operator produces the expected mutant, out-of-range skipped, disabled lines honored.

### C7. MutantApplier

- [ ] Text-based application using node byte ranges from the CST. Tree-sitter gives us exact byte offsets; simpler than re-rendering the tree.
- [ ] After application, re-parse with tree-sitter and assert no syntax errors; return `nil` if the mutated source doesn't parse (silently skip corrupt mutants rather than running broken tests).
- [ ] Tests: each mutation type applied, re-parse check catches malformed output.

### C8. TestRunner

- [ ] Temp-copy isolation strategy (from `MULTI_LANGUAGE_SUPPORT.md` §Mutation isolation).
- [ ] Per-file `sync.Mutex` map so concurrent mutations on the same file serialize but different files run in parallel.
- [ ] Test command: `cargo test` with `CARGO_INCREMENTAL=0`. Honor `TestRunConfig.TestPattern` (pass as positional filter).
- [ ] Kill original file from a backup on restore; panic-safe via `defer`.
- [ ] Honor `TestRunConfig.Timeout` via `exec.CommandContext`.
- [ ] Tests: killed mutant (test fails → killed), survived (test passes → survived), timeout, crash-during-run leaves source restored (simulate via deliberate panic in a helper test).

### C9. Register + wire-up

- [ ] `rustanalyzer/rustanalyzer.go`: `Language` struct, `Name() string { return "rust" }`, `init()` calling `lang.Register`.
- [ ] Blank import in `cmd/diffguard/main.go`.

---

## Part D — TypeScript analyzer [T]

`internal/lang/tsanalyzer/`. See `MULTI_LANGUAGE_SUPPORT.md` §TypeScript for parser and operator notes.

### D0. Research prerequisites

- [ ] `github.com/smacker/go-tree-sitter/typescript/typescript` for `.ts`, `.../typescript/tsx` for `.tsx`. Use the grammar matching the file extension.
- [ ] Test runner detection: parse `package.json` devDependencies — prefer `vitest`, then `jest`, then fall back to `npm test`.

### D1. FileFilter

- [ ] Extensions: `.ts`, `.tsx`. Deliberately exclude `.js`, `.jsx`, `.mjs`, `.cjs` for now (JS-only repos out of scope).
- [ ] `IsTestFile`: suffixes `.test.ts`, `.test.tsx`, `.spec.ts`, `.spec.tsx`; any path segment `__tests__` or `__mocks__`.
- [ ] `DiffGlobs`: `*.ts`, `*.tsx`.
- [ ] Tests: glob matches, test-file exclusion, `utils.test-helper.ts` is NOT a test file (edge case).

### D2. FunctionExtractor

- [ ] Tree-sitter queries for: `function_declaration`, `method_definition`, `arrow_function` assigned to `variable_declarator`, `function` expressions assigned similarly, `generator_function`.
- [ ] Name extraction: `ClassName.method`, `functionName`, arrow assigned to `const x = () =>` → `x`.
- [ ] Line ranges, filtering, file LOC.
- [ ] Tests: each form, class methods (including static + private), nested arrow functions, exported vs. local.

### D3. ComplexityCalculator + ComplexityScorer

- [ ] Base +1 on: `if_statement`, `for_statement`, `for_in_statement`, `for_of_statement`, `while_statement`, `switch_statement`, `try_statement`, `ternary_expression`.
- [ ] +1 per `catch_clause`; +1 per `else` branch; +1 per `case` with content (empty fall-through cases don't count).
- [ ] +1 per `.catch(` promise-chain method call (string-match on identifier to avoid CST depth).
- [ ] +1 per `&&` / `||` run change.
- [ ] Do **not** count: optional chaining `?.`, nullish coalescing `??`, `await` alone, `async` keyword, stream method calls.
- [ ] Tests: ternary nest, `try/catch/finally`, logical chains, optional chaining ignored.

### D4. ImportResolver

- [ ] `DetectModulePath`: parse `package.json` `name` field.
- [ ] `ScanPackageImports`: `import` and `require(...)`. Internal iff the specifier starts with `.` or a registered project alias (`@/`, `~/`). Resolve relative paths against the source file's directory, fold to dir-level for the graph.
- [ ] Tests: internal vs. external classification, relative resolution, barrel re-exports count as one edge.

### D5. AnnotationScanner

- [ ] `// mutator-disable-next-line` and `// mutator-disable-func` comments.
- [ ] Function ranges from D2 for func-scope disables.
- [ ] Tests: same shape as Rust's C5.

### D6. MutantGenerator

- [ ] Canonical operators: `conditional_boundary`, `negate_conditional` (include `===` / `!==`), `math_operator`, `return_value` (use `null` / `undefined` appropriately), `boolean_substitution`, `incdec` (JS/TS has `++` / `--`), `branch_removal`, `statement_deletion`.
- [ ] TS-specific additions — register in `internal/mutation/tiers.go`:
  - [ ] `strict_equality` (Tier 1): flip `===` ↔ `==` and `!==` ↔ `!=`.
  - [ ] `nullish_to_logical_or` (Tier 2): `??` → `||`.
  - [ ] `optional_chain_removal` (Tier 2): `foo?.bar` → `foo.bar`.
- [ ] Filter to changed regions, skip disabled lines.
- [ ] Tests: each operator emits mutants; TS-specific operators exercised.

### D7. MutantApplier

- [ ] Same text-based strategy as Rust's C7. Re-parse check after mutation.
- [ ] Tests: each mutation applied, re-parse catches corrupt output.

### D8. TestRunner

- [ ] Temp-copy + per-file lock, identical to Rust.
- [ ] Command selection by detected runner (vitest / jest / npm test). Compose with `--testPathPattern` or `-t` honoring `TestPattern`.
- [ ] Honor `TestRunConfig.Timeout`.
- [ ] Set `CI=true` to suppress interactive prompts.
- [ ] Tests: killed, survived, timeout, restoration after crash.

### D9. Register + wire-up

- [ ] `tsanalyzer/tsanalyzer.go`: `Language` with `Name() string { return "typescript" }`, `init()` calls `lang.Register`.
- [ ] Blank import in `cmd/diffguard/main.go`.

---

## Part E — Integration & verification [X]

### E1. Mixed-repo end-to-end

- [ ] Fixture at `cmd/diffguard/testdata/mixed-repo/` containing a minimal Cargo crate, a minimal TS package, and (for completeness) a Go file.
- [ ] End-to-end test invoking the built binary (`go build` then `exec`) against the fixture. Assert each language's sections appear with correct suffixes.
- [ ] Negative control: same fixture stripped of violations must produce `WorstSeverity() == PASS`.

### E2. CI

- [ ] Extend `.github/workflows/` to install Rust (`rustup`) and Node (for test runners) before running the eval suites.
- [ ] Add `make eval-rust`, `make eval-ts`, `make eval-mixed` targets wrapping the eval Go tests with the right env (e.g., `CARGO_INCREMENTAL=0`, `CI=true`).
- [ ] Cache Cargo and npm artifacts so CI stays fast.

### E3. README + docs

- [ ] Update `README.md` top section: tagline no longer says Go-only; list supported languages.
- [ ] Add a per-language "Install" subsection (required toolchain: Rust + cargo, Node + npm).
- [ ] Add `--language` to the CLI reference.
- [ ] Document annotation syntax per language.
- [ ] Cross-link from `README.md` to this checklist and to `MULTI_LANGUAGE_SUPPORT.md`.

---

## Evaluation suite [EVAL] — does diffguard actually catch real issues

Structural tests (Parts A–E) prove the plumbing works. This section proves the analyzers produce correct verdicts on real, seeded problems. Every case is a **positive / negative control pair**: the positive must be flagged with the right severity, the negative must pass. Negative controls are the firewall against rubber-stamping.

### EVAL-1. Harness

- [ ] `internal/lang/<lang>analyzer/evaldata/` holds fixtures.
- [ ] `eval_test.go` in each analyzer package runs the full pipeline (built binary, full CLI path) against each fixture and diff-compares emitted findings to `expected.json`.
- [ ] Comparison is semantic (file + function + severity), not byte-for-byte, so cosmetic line shifts don't break the eval.
- [ ] Eval runs are deterministic: `--mutation-sample-rate 100`, fixed `--mutation-workers`, a stable seed for any randomized orderings.
- [ ] Each fixture directory has a `README.md` documenting the seeded issue and the expected verdict.

### EVAL-2. Rust cases

- [ ] **complexity**:
  - Positive `complex_positive.rs`: nested `match` + `if let` + guarded arms, cognitive ≥11 → section FAIL with finding on that fn.
  - Negative `complex_negative.rs`: same behavior split into helpers, each <10 → section PASS, zero findings.
- [ ] **sizes (function)**:
  - Positive: single `fn` >50 lines → FAIL.
  - Negative: same behavior factored across fns, each <50 → PASS.
- [ ] **sizes (file)**:
  - Positive: `large_file.rs` >500 LOC → FAIL.
  - Negative: <500 LOC → PASS.
- [ ] **deps (cycle)**:
  - Positive: `a.rs` ↔ `b.rs` → FAIL with cycle finding.
  - Negative: same modules with a shared `types.rs` breaking the cycle → PASS.
- [ ] **deps (SDP)**:
  - Positive: unstable concrete module imported by stable abstract one → WARN/FAIL per current SDP severity.
  - Negative: reversed dependency direction → PASS.
- [ ] **churn**:
  - Positive `hot_complex.rs` with a baked `.git` dir showing 8+ commits on a complex fn → finding present.
  - Negative `hot_simple.rs` same commit count, trivial fn → no finding.
- [ ] **mutation (kill)**:
  - Positive `well_tested.rs`: arithmetic fn + tests covering boundary and sign → Tier-1 ≥90% → PASS.
  - Negative `untested.rs`: same fn, test covers only one branch → Tier-1 <90% → FAIL.
- [ ] **mutation (Rust-specific operator)**:
  - Positive: `unwrap_removal` / `some_to_none` on a tested fn is killed; on an untested fn survives.
  - Proof that the operator adds signal, not noise.
- [ ] **mutation (annotation respect)**:
  - Positive `# mutator-disable-func` suppresses all mutants in that fn.
  - Negative (same file, annotation removed) regenerates them.

### EVAL-3. TypeScript cases

- [ ] **complexity**:
  - Positive `complex_positive.ts`: nested ternaries + try/catch + `&&`/`||` chains ≥11 → FAIL.
  - Negative `complex_negative.ts`: refactored into named helpers → PASS.
- [ ] **sizes (function)**:
  - Positive: arrow fn assigned to `const` >50 LOC → FAIL.
  - Negative: same logic across named exports → PASS.
- [ ] **sizes (file)**:
  - Positive `large_file.ts` >500 LOC → FAIL.
  - Negative: split across files → PASS.
- [ ] **deps (cycle)**:
  - Positive `a.ts` ↔ `b.ts` → FAIL.
  - Negative: shared `types.ts` breaking cycle → PASS.
- [ ] **deps (internal vs external)**:
  - Positive: `./foo` appears in internal graph; `import 'lodash'` does NOT.
  - Assert directly on the graph shape, not just pass/fail.
- [ ] **churn**:
  - Positive `hot_complex.ts` with seeded history → finding.
  - Negative `hot_simple.ts` same history → no finding.
- [ ] **mutation (kill, with configured runner)**:
  - Positive: `arithmetic.ts` + tests covering boundary + sign → Tier-1 ≥90% → PASS.
  - Negative: same fn, test covers one branch → Tier-1 <90% → FAIL.
- [ ] **mutation (TS-specific operators)**:
  - Positive: `strict_equality` flip killed by tests that rely on strict equality; `nullish_to_logical_or` killed by tests that distinguish `null` from `undefined`.
  - Negative: same operators survive when the test only asserts non-distinguishing inputs. Confirms the operators generate meaningful mutants, not noise.
- [ ] **mutation (annotation respect)**:
  - Positive `// mutator-disable-next-line` suppresses the next-line mutant.
  - Negative: annotation removed, mutant regenerated.

### EVAL-4. Cross-cutting

- [ ] **Mixed-repo severity propagation**:
  - Rust FAIL + TS PASS → overall FAIL; TS section independently reports PASS.
  - Flip: Rust PASS + TS FAIL → overall FAIL; Rust section independently reports PASS.
  - Proves language sections don't contaminate each other.
- [ ] **Mutation concurrency safety**:
  - Fixture with 3+ Rust and 3+ TS files, each with multiple mutants. Run `--mutation-workers 4`.
  - Assert `git status --porcelain` is empty after the run (no temp-copy corruption).
  - Assert repeated runs produce identical reports.
  - Sweep `--mutation-workers` 1, 2, 4, 8 and assert report stability.
- [ ] **Disabled-line respect under concurrency**:
  - A file with `mutator-disable-func` on one fn and live code on another, `--mutation-workers 4`.
  - Assert zero mutants generated for the disabled fn; live fn's mutants execute.
- [ ] **False-positive ceiling**:
  - Known-clean fixture (well-tested small Rust crate + well-tested small TS module) → `WorstSeverity() == PASS`, zero FAIL findings across all analyzers.
  - This is the "does it cry wolf" gate.

### EVAL-5. Pre-flight calibration (pre-ship)

- [ ] Rust: run the built diffguard against two open-source crates (one small, one mid-sized). Triage every FAIL and WARN. If >20% are noise, iterate on thresholds/detection before declaring Rust support shipped.
- [ ] TypeScript: repeat with one app and one library project.
- [ ] Record triage findings in this document under a "Baseline noise rate" appendix so future changes know what "good" looks like.

---

## Execution order summary

```
A (foundation) ──► B (orchestration) ──┬──► C (Rust)        ──┬──► E (integration + CI)
                                       └──► D (TypeScript)  ──┘
                                             │
                                             └──► EVAL runs alongside C/D, per analyzer
```

Parts C and D are disjoint packages and can be implemented in parallel by separate agents / PRs, rebased onto the B branch. Part E holds the merge point and the final evaluation gate.

---

## Sign-off criteria

Before calling this done:

- [ ] All checklist items above checked.
- [ ] `go test ./...` green.
- [ ] `make eval-rust`, `make eval-ts`, `make eval-mixed` all green in CI.
- [ ] Pre-flight calibration triage documented with <20% noise rate per language.
- [ ] README reflects multi-language support with install instructions for each toolchain.
- [ ] `diffguard` run on this repo's own HEAD produces identical output before and after the reorg (the Go path must be byte-stable).
