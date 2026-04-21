# diffguard

A targeted code quality gate for Go, Rust, and TypeScript repositories. Analyzes either the changed regions of a git diff (CI mode) or specified files/directories (refactoring mode), and reports on complexity, size, dependency structure, churn risk, and mutation test coverage.

## Supported Languages

| Language   | Extensions              | Detection signal | Test runner for mutation testing |
|------------|-------------------------|------------------|----------------------------------|
| Go         | `.go`                   | `go.mod`         | `go test` (with `-overlay` isolation) |
| Rust       | `.rs`                   | `Cargo.toml`     | `cargo test` (temp-copy isolation) |
| TypeScript | `.ts`, `.tsx` | `package.json` | `npm test` (project-configured — vitest / jest / node) |

Languages are auto-detected from root-level manifest files; pass `--language go,rust,typescript` (comma-separated) to force a subset. See [`MULTI_LANGUAGE_SUPPORT.md`](MULTI_LANGUAGE_SUPPORT.md) for the architectural overview and [`docs/rust-typescript-support.md`](docs/rust-typescript-support.md) for the Rust+TS roadmap and parser details.

## Why

Asking an AI agent to "refactor this" or "clean up the code" is ambiguous. "Make it simpler" produces different results every run, and there's no principled way to tell the agent when it's done — or to verify that "done" actually means better. Natural language isn't a specification.

Diffguard turns "is this code good?" into a set of numbers an agent can iterate against:

- Cognitive complexity ≤ 10 per function
- Function bodies ≤ 50 lines, files ≤ 500 lines
- No new dependency cycles or Stable Dependencies Principle violations
- Tier‑1 mutation kill rate ≥ 90% (tests actually catch logic changes)

Run diffguard, read the violations, change the code, run again — loop until it exits 0. The metrics become the spec. The agent has something objective to optimize for rather than guessing at taste, and you get a reproducible definition of "good enough" instead of having to re‑judge every diff by eye. Also useful for traditional human-written CI, but the real lift is on AI-generated PRs where line-by-line review doesn't scale.

## Install

```bash
go install github.com/0xPolygon/diffguard/cmd/diffguard@latest
```

Or build from source:

```bash
git clone https://github.com/0xPolygon/diffguard.git
cd diffguard
go build -o diffguard ./cmd/diffguard/
```

### Per-language runtime dependencies

Diffguard the binary is a single Go program — but the mutation-testing
section shells out to each language's native test runner. If you only
use the structural analyzers (complexity, sizes, deps, churn) you can
skip these entirely via `--skip-mutation`.

**Go repositories:**
- Nothing extra; Go's own toolchain is assumed on PATH.

**Rust repositories:**
- A working `cargo` on `$PATH` (stable channel recommended). Install
  via [rustup](https://rustup.rs).
- Mutation testing copies the crate into a temp dir per mutant, so
  sufficient disk space matters more than RAM. First-run `cargo test`
  populates `~/.cargo` and is the slowest; subsequent runs are cached.
- `CARGO_INCREMENTAL=0` is recommended in CI for determinism.

**TypeScript repositories:**
- `node` ≥ 22.6 and `npm` on `$PATH`. Node 22.6 is the minimum because
  mutation testing relies on `--experimental-strip-types` being default.
  Install via [nvm](https://github.com/nvm-sh/nvm), [mise](https://mise.jdx.dev),
  [fnm](https://github.com/Schniz/fnm), or your distro's package manager.
- A project-local `package.json` with a working `"scripts": { "test": ... }`
  (vitest, jest, or plain `node --test` all work). The mutation runner
  invokes `npm test` and watches the exit code.

Install the matching toolchain once, and `diffguard --paths . .` in a
multi-language monorepo will fan out to all of them in parallel.

## Usage

```bash
# Analyze changes against the auto-detected base branch
diffguard /path/to/repo

# Specify a base branch
diffguard --base main /path/to/repo

# Refactoring mode: analyze entire files/dirs (no diff required)
diffguard --paths internal/foo/bar.go /path/to/repo
diffguard --paths internal/foo/,internal/bar/ /path/to/repo

# Skip mutation testing (fastest)
diffguard --skip-mutation /path/to/repo

# Or sample a subset of mutants for faster-but-still-useful signal
diffguard --mutation-sample-rate 20 /path/to/repo

# JSON output for CI ingestion
diffguard --output json /path/to/repo

# Custom thresholds
diffguard \
  --complexity-threshold 15 \
  --function-size-threshold 80 \
  --file-size-threshold 800 \
  /path/to/repo
```

### Modes

**Diff mode (default):** Analyzes only the regions changed between `HEAD` and the base branch. Use this as a CI gate for PRs.

**Refactoring mode (`--paths`):** Analyzes the full content of the specified files or directories, ignoring git diff entirely. Use this when iterating on an existing file's quality without a base to compare against.

## What It Measures

### Cognitive Complexity

Computes [cognitive complexity](https://www.sonarsource.com/resources/cognitive-complexity/) (gocognit-style) for every function touched in the diff. Unlike cyclomatic complexity, cognitive complexity penalizes nested control flow more heavily than flat structures, better reflecting how hard code is to read.

Scoring rules:
- +1 for each `if`, `else if`, `else`, `for`, `switch`, `select`
- +1 nesting penalty per nesting level for nested control structures
- +1 per sequence of `&&`/`||` operators, +1 each time the operator switches

Default threshold: **10** per function.

### Function and File Sizes

Measures lines of code for changed functions and files. Large functions and files are harder to review and more prone to bugs.

Default thresholds: **50 lines** per function, **500 lines** per file.

### Dependency Structure

Builds a directed graph of internal package imports from changed packages and reports:

- **Circular dependencies** (cycles) as errors.
- **Afferent coupling** (Ca) -- how many packages depend on this one.
- **Efferent coupling** (Ce) -- how many packages this one depends on.
- **Instability** -- `Ce / (Ca + Ce)`. Packages with high instability (close to 1.0) change easily but shouldn't be depended on by many others.
- **Stable Dependencies Principle violations** -- flags when a stable package depends on a less stable one.

### Churn-Weighted Complexity

Cross-references git history with complexity scores. Functions that are both complex AND frequently modified are the highest-risk targets for bugs. Reports the top 10 by `commits * complexity`.

### Mutation Testing

Applies mutations to changed code and runs tests to verify they catch the change. The canonical operator set is shared across all languages:

| Operator | Example |
|----------|---------|
| Conditional boundary | `>` to `>=`, `<` to `<=` |
| Negate conditional | `==` to `!=`, `>` to `<` |
| Math operator | `+` to `-`, `*` to `/` |
| Boolean substitution | `true` to `false` |
| Return value | Replace returns with zero values |
| Increment/decrement | `x++` to `x--` and vice versa |
| Branch removal | Empty the body of an `if` |
| Statement deletion | Remove a bare function-call statement |

Per-language operators on top of the canonical set:

- **Rust**: `unwrap_removal` (`.unwrap()` / `.expect(...)` → propagate via `?`), `some_to_none` (`Some(x)` → `None` in return contexts).
- **TypeScript**: `strict_equality` (`==` ↔ `===`, `!=` ↔ `!==`), `nullish_to_logical_or` (`??` → `||`).

Reports a mutation score (killed / total). Mutants run fully in parallel — including mutants on the same file — using language-native isolation strategies:

- **Go**: `go test -overlay` so each worker sees its own mutated copy without touching the real source tree.
- **Rust**: per-mutant temp-copy of the crate directory (isolated `target/`).
- **TypeScript**: per-mutant in-place text edit with restore-on-defer, serialized by file.

Concurrency defaults to `runtime.NumCPU()` and is tunable with `--mutation-workers`. Use `--skip-mutation` to skip entirely, or `--mutation-sample-rate 20` for a faster-but-noisier subset.

#### Tiered mutation scoring

The raw score is misleading for observability-heavy codebases: logging / metrics calls (`log.*`, `metrics.*`, `console.*`, `tracing::info!`) generate many `statement_deletion` and `branch_removal` survivors that tests can't observe by design. Diffguard groups operators into three tiers so you can gate CI on the ones that matter:

| Tier | Operators | Gating |
|------|-----------|--------|
| **Tier 1 — logic** | `negate_conditional`, `conditional_boundary`, `return_value`, `math_operator` | FAIL below `--tier1-threshold` (default 90%) |
| **Tier 2 — semantic** | `boolean_substitution`, `incdec` | WARN below `--tier2-threshold` (default 70%) |
| **Tier 3 — observability** | `statement_deletion`, `branch_removal` | Reported only — never gates CI |

The summary line surfaces the raw score followed by per-tier breakdowns:

```
Score: 74.0% (148/200 killed, 52 survived) | T1 logic: 92.0% (46/50) | T2 semantic: 78.0% (14/18) | T3 observability: 45.0% (40/90)
```

Tiers with zero mutants are omitted from the summary. Recommended CI policy: use the defaults (strict on Tier 1, advisory on Tier 2, ignore Tier 3). For gradual rollout on codebases with many pre-existing gaps, start with a lower `--tier1-threshold` and ratchet it up over time.

**Silencing unavoidable survivors.** Some mutations can't realistically be killed (e.g., defensive error-check branches that tests can't exercise). Annotate those with comments — each language uses its native single-line comment syntax, but the directive names are identical.

Go:

```go
// mutator-disable-next-line
if err != nil {
    return fmt.Errorf("parse failed: %w", err)
}

// mutator-disable-func
func defensiveHelper() error {
    // ... entire function skipped
}
```

Rust:

```rust
// mutator-disable-next-line
if cfg.is_none() {
    return Err("config required".into());
}

// mutator-disable-func
fn defensive_helper() -> Result<(), Error> {
    // ... entire function skipped
}
```

TypeScript:

```ts
// mutator-disable-next-line
if (token == null) {
  throw new Error("token required");
}

// mutator-disable-func
function defensiveHelper(): void {
  // ... entire function skipped
}
```

Supported annotations (all languages):
- `mutator-disable-next-line` — skips mutations on the following source line
- `mutator-disable-func` — skips mutations in the enclosing function (the comment may sit inside the function or on a doc-comment line directly above it)

## CLI Reference

```
diffguard [flags] <repo-path>

Flags:
  --language string               Comma-separated languages to analyze (go,rust,typescript).
                                  Default: auto-detect from root manifests (go.mod / Cargo.toml / package.json).
  --base string                   Base branch to diff against (default: auto-detect)
  --paths string                  Comma-separated files/dirs to analyze in full (refactoring mode); skips git diff
  --complexity-threshold int      Maximum cognitive complexity per function (default 10)
  --function-size-threshold int   Maximum lines per function (default 50)
  --file-size-threshold int       Maximum lines per file (default 500)
  --skip-mutation                 Skip mutation testing
  --mutation-sample-rate float    Percentage of mutants to test, 0-100 (default 100)
  --test-timeout duration         Per-mutant test timeout (default 30s)
  --test-pattern string           Pattern passed to the per-language test runner (scopes tests to speed up slow suites;
                                  Go: `go test -run`, Rust: `cargo test --`, TS: forwarded as npm_config_test_pattern)
  --mutation-workers int          Max packages processed concurrently during mutation testing; 0 = runtime.NumCPU() (default 0)
  --tier1-threshold float         Minimum kill % for Tier-1 (logic) mutations; below triggers FAIL (default 90)
  --tier2-threshold float         Minimum kill % for Tier-2 (semantic) mutations; below triggers WARN (default 70)
  --output string                 Output format: text, json (default "text")
  --fail-on string                Exit non-zero if thresholds breached: none, warn, all (default "warn")
```

### Exit codes

- `0` -- all checks passed (or `--fail-on none`)
- `1` -- thresholds breached

The `--fail-on` flag controls sensitivity:
- `none` -- always exit 0
- `warn` -- exit 1 only on FAIL severity (default)
- `all` -- exit 1 on any WARN or FAIL

## CI Integration

The recommended pattern is a two-tier setup:

1. **Per-PR gate** — diff mode with a sampled mutation run (~20%) for fast feedback on changed code.
2. **Scheduled full sweep** — refactoring mode with 100% mutation across the whole codebase, once a week or on-demand.

### GitHub Actions

**Per-PR gate (diff mode, sampled mutation):**

```yaml
name: diffguard
on: [pull_request]

jobs:
  quality:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0  # needed for git diff and churn history

      - uses: actions/setup-go@v5
        with:
          go-version: '1.26.1'

      # Add any language runtimes your repo actually uses — these are
      # only needed for mutation testing. Drop the unused ones.
      - uses: dtolnay/rust-toolchain@stable  # Rust repos
      - uses: actions/setup-node@v4          # TS repos
        with:
          node-version: '22'

      - name: Install diffguard
        run: go install github.com/0xPolygon/diffguard/cmd/diffguard@latest

      - name: Run diffguard
        run: diffguard --mutation-sample-rate 20 --base origin/${{ github.base_ref }} .
```

**Scheduled full sweep (refactoring mode, 100% mutation):**

```yaml
name: mutation
on:
  schedule:
    - cron: '0 6 * * 1'   # Mondays at 06:00 UTC
  workflow_dispatch:

jobs:
  mutate:
    runs-on: ubuntu-latest
    timeout-minutes: 30
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26.1'
      - run: go install github.com/0xPolygon/diffguard/cmd/diffguard@latest
      - name: Full quality gate with 100% mutation
        run: diffguard --paths internal/,cmd/ .
```

### GitLab CI

```yaml
diffguard:
  stage: test
  script:
    - go install github.com/0xPolygon/diffguard/cmd/diffguard@latest
    - diffguard --mutation-sample-rate 20 --base origin/$CI_MERGE_REQUEST_TARGET_BRANCH_NAME .
  rules:
    - if: $CI_PIPELINE_SOURCE == "merge_request_event"
```

### Local pre-push hook

```bash
#!/bin/sh
# .git/hooks/pre-push  —  fast local check, skip mutation
diffguard --skip-mutation --base develop .
```

### Local refactoring loop

When iterating on a single file or package outside of a PR:

```bash
# Quality gate on the file being refactored
diffguard --paths internal/foo/bar.go .

# Or an entire subtree, e.g. while cleaning up a package
diffguard --paths internal/foo/ .
```

## Example Output

```
=== Cognitive Complexity ===
12 functions analyzed | Mean: 4.2 | Median: 3 | Max: 22 | 2 over threshold (10)  [FAIL]
Violations:
  pkg/handler/routes.go:45:HandleRequest                       complexity=22  [FAIL]
  pkg/auth/token.go:112:ValidateToken                          complexity=14  [FAIL]

=== Code Sizes ===
12 functions, 3 files analyzed | 1 over threshold (func>50, file>500)  [FAIL]
Violations:
  pkg/handler/routes.go:45:HandleRequest                       function=87 lines  [FAIL]

=== Dependency Structure ===
3 packages analyzed | 0 cycles | 0 SDP violations  [PASS]

=== Churn-Weighted Complexity ===
12 functions analyzed | Top churn*complexity score: 440  [WARN]
Warnings:
  pkg/handler/routes.go:45:HandleRequest                       commits=20 complexity=22 score=440  [WARN]
```

## Further reading

- [`MULTI_LANGUAGE_SUPPORT.md`](MULTI_LANGUAGE_SUPPORT.md) — how the
  multi-language orchestrator fans a single run out across the
  registered analyzers, and how to add a new language.
- [`docs/rust-typescript-support.md`](docs/rust-typescript-support.md)
  — Rust and TypeScript roadmap, parser internals, and the checklist
  used to validate correctness.

## License

MIT
