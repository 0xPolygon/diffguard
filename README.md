# diffguard

A diff-scoped code quality gate for Go repositories. Analyzes only changed code in a git diff and reports on complexity, size, dependency structure, churn risk, and mutation test coverage.

Designed as a CI gate for AI-generated PRs, where line-by-line human review doesn't scale.

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

Applies mutations to changed code and runs tests to verify they catch the change:

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

Reports a mutation score (killed / total). Mutants run fully in parallel — including mutants on the same file — using `go test -overlay` so each worker sees its own mutated copy without touching the real source tree. Concurrency defaults to `runtime.NumCPU()` and is tunable with `--mutation-workers`. Use `--skip-mutation` to skip entirely, or `--mutation-sample-rate 20` for a faster-but-noisier subset.

#### Tiered mutation scoring

The raw score is misleading for observability-heavy Go codebases: `log.*` and `metrics.*` calls generate many `statement_deletion` and `branch_removal` survivors that tests can't observe by design. Diffguard groups operators into three tiers so you can gate CI on the ones that matter:

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

**Silencing unavoidable survivors.** Some mutations can't realistically be killed (e.g., defensive error-check branches that tests can't exercise). Annotate those with comments:

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

Supported annotations:
- `// mutator-disable-next-line` — skips mutations on the following source line
- `// mutator-disable-func` — skips mutations in the enclosing function (the comment may sit inside the function or on a godoc line directly above it)

## CLI Reference

```
diffguard [flags] <repo-path>

Flags:
  --base string                   Base branch to diff against (default: auto-detect)
  --paths string                  Comma-separated files/dirs to analyze in full (refactoring mode); skips git diff
  --complexity-threshold int      Maximum cognitive complexity per function (default 10)
  --function-size-threshold int   Maximum lines per function (default 50)
  --file-size-threshold int       Maximum lines per file (default 500)
  --skip-mutation                 Skip mutation testing
  --mutation-sample-rate float    Percentage of mutants to test, 0-100 (default 100)
  --test-timeout duration         Per-mutant go test timeout (default 30s)
  --test-pattern string           Pattern passed to `go test -run` for each mutant (scopes tests to speed up slow suites)
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

## License

MIT
