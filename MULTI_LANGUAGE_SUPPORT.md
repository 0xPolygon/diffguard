# Multi-Language Support Guide

A comprehensive checklist for adding new language support to diffguard. This document covers the one-time repo reorganization needed to enable multi-language support, defines the interfaces each language must implement, and provides a reusable per-language checklist.

---

## Table of Contents

1. [Architecture Overview](#architecture-overview)
2. [Repo Reorganization (One-Time)](#repo-reorganization-one-time)
3. [Interface Definitions](#interface-definitions)
4. [Per-Language Implementation Checklist](#per-language-implementation-checklist)
5. [Language-Specific Notes](#language-specific-notes)
6. [Key Design Decisions](#key-design-decisions)

---

## Architecture Overview

### What's Already Language-Agnostic

These components work for any language with zero changes:

| Component | Location | What It Does |
|-----------|----------|--------------|
| Report types | `internal/report/report.go` | `Finding`, `Section`, `Severity`, text/JSON output |
| Tier classification | `internal/mutation/tiers.go` | Groups mutation operators into Tier 1/2/3 by name |
| Graph algorithms | `internal/deps/deps.go` | Cycle detection, afferent/efferent coupling, instability, SDP violations |
| Git churn counting | `internal/churn/churn.go` | `git log --oneline --follow` to count commits per file |
| Diff format parsing | `internal/diff/diff.go` | Unified diff hunk header parsing (`@@ -a,b +c,d @@`) |
| CLI/config | `cmd/diffguard/main.go` | Flag parsing, exit code logic, analyzer orchestration |

### What's Tightly Coupled to Go

Every item below must be abstracted behind an interface and re-implemented per language:

| Concern | Current Location | Go-Specific Mechanism |
|---------|------------------|-----------------------|
| File filtering | `diff/diff.go:92,175-177,201-208` | Hardcoded `*.go` glob, `_test.go` exclusion |
| Function identification | `sizes/sizes.go`, `complexity/complexity.go`, `churn/churn.go` | `*ast.FuncDecl` + receiver detection (duplicated 3x) |
| Complexity scoring | `complexity/complexity.go` | Walks `IfStmt`, `ForStmt`, `SwitchStmt`, `SelectStmt`, etc. |
| Import parsing | `deps/deps.go` | `parser.ParseDir()` + `go.mod` module path extraction |
| Mutation generation | `mutation/generate.go` | Go AST node pattern matching for 8 operator types |
| Mutation application | `mutation/apply.go` | Go AST rewriting + `go/printer` |
| Disable annotations | `mutation/annotations.go` | Scans Go comments + `*ast.FuncDecl` ranges |
| Test execution | `mutation/mutation.go` | `go test -overlay` (Go build system feature) |

---

## Repo Reorganization (One-Time)

These steps prepare the repo structure for multiple languages. Each step must leave all existing tests passing.

### Step 1: Create the language abstraction layer

- [ ] Create `internal/lang/lang.go` with all interface definitions (see [Interface Definitions](#interface-definitions))
- [ ] Create `internal/lang/detect.go` with language auto-detection logic
- [ ] Create `internal/lang/registry.go` with a `Register()`/`Get()`/`All()` registry

### Step 2: Extract Go file filtering

- [ ] Create `internal/lang/goanalyzer/` package
- [ ] Implement `FileFilter` for Go (extensions: `.go`, test exclusion: `_test.go`, diff globs: `*.go`)
- [ ] Modify `diff.Parse()` and `diff.CollectPaths()` to accept a `FileFilter` parameter instead of hardcoded `.go` checks
- [ ] Update all callers in `cmd/diffguard/main.go` to pass the Go file filter

### Step 3: Extract Go function extraction

- [ ] Move function identification logic from `sizes.go`, `complexity.go`, and `churn.go` into `internal/lang/goanalyzer/parse.go`
- [ ] Consolidate the three duplicate `funcName()` implementations into one shared helper
- [ ] Implement `FunctionExtractor` interface for Go
- [ ] Modify `internal/sizes/sizes.go` to call through the interface

### Step 4: Extract Go complexity scoring

- [ ] Implement `ComplexityCalculator` interface for Go in `internal/lang/goanalyzer/complexity.go`
- [ ] Implement `ComplexityScorer` interface for Go (can share implementation with `ComplexityCalculator`)
- [ ] Modify `internal/complexity/complexity.go` to call through the interface
- [ ] Modify `internal/churn/churn.go` to call through the `ComplexityScorer` interface
- [ ] Delete the duplicated simplified `computeComplexity()` in churn

### Step 5: Extract Go import resolution

- [ ] Implement `ImportResolver` interface for Go in `internal/lang/goanalyzer/deps.go`
- [ ] Split `internal/deps/deps.go` into `graph.go` (pure algorithms) and `deps.go` (orchestration)
- [ ] Modify `deps.go` orchestration to call through the interface

### Step 6: Extract Go mutation interfaces

- [ ] Implement `MutantGenerator` in `internal/lang/goanalyzer/mutation_generate.go`
- [ ] Implement `MutantApplier` in `internal/lang/goanalyzer/mutation_apply.go`
- [ ] Implement `AnnotationScanner` in `internal/lang/goanalyzer/mutation_annotate.go`
- [ ] Implement `TestRunner` in `internal/lang/goanalyzer/testrunner.go`
- [ ] Modify `internal/mutation/` to call through interfaces
- [ ] Keep `tiers.go` in `internal/mutation/` (it's already language-agnostic)

### Step 7: Wire up registration and detection

- [ ] Add `init()` function to `internal/lang/goanalyzer/` that calls `lang.Register()`
- [ ] Add blank import `_ "github.com/0xPolygon/diffguard/internal/lang/goanalyzer"` in `cmd/diffguard/main.go`
- [ ] Add `--language` CLI flag (default: auto-detect)
- [ ] Modify `cmd/diffguard/main.go` to resolve language and pass it through the analyzer pipeline
- [ ] Add tests for language detection and registration

### Resulting directory structure

```
internal/
  lang/
    lang.go              # Interface definitions
    detect.go            # Auto-detection from file extensions / manifest files
    registry.go          # Register/Get/All
    goanalyzer/          # Go implementation
      goanalyzer.go      # init() + Language interface impl
      parse.go           # Shared Go AST helpers (funcName, etc.)
      complexity.go      # ComplexityCalculator + ComplexityScorer
      sizes.go           # FunctionExtractor
      deps.go            # ImportResolver
      mutation_generate.go  # MutantGenerator
      mutation_apply.go     # MutantApplier
      mutation_annotate.go  # AnnotationScanner
      testrunner.go         # TestRunner (go test -overlay)
  diff/                  # Modified: parameterized file filtering
  complexity/            # Modified: delegates to lang.ComplexityCalculator
  sizes/                 # Modified: delegates to lang.FunctionExtractor
  deps/
    graph.go             # Pure graph algorithms (extracted, unchanged)
    deps.go              # Orchestration, delegates to lang.ImportResolver
  churn/                 # Modified: delegates to lang.ComplexityScorer
  mutation/              # Modified: delegates to lang interfaces
    tiers.go             # Unchanged (already language-agnostic)
  report/                # Unchanged
```

---

## Interface Definitions

Each language implementation must satisfy a top-level `Language` interface that provides access to all sub-interfaces.

### Language (top-level)

```
Language
  Name() string                          -- identifier: "go", "python", "typescript", etc.
  FileFilter() FileFilter                -- which files belong to this language
  ComplexityCalculator() ComplexityCalculator
  FunctionExtractor() FunctionExtractor
  ImportResolver() ImportResolver
  ComplexityScorer() ComplexityScorer
  MutantGenerator() MutantGenerator
  MutantApplier() MutantApplier
  AnnotationScanner() AnnotationScanner
  TestRunner() TestRunner
```

### FileFilter

Controls which files the diff parser includes and which are excluded as test files.

```
FileFilter
  Extensions   []string                 -- source extensions incl. dot: [".go"], [".py"], [".ts", ".tsx"]
  IsTestFile   func(path string) bool   -- returns true for test files to exclude from analysis
  DiffGlobs    []string                 -- globs passed to `git diff -- <globs>`
```

### FunctionExtractor

Parses source files, finds function/method declarations, reports their line ranges and sizes.

```
FunctionExtractor
  ExtractFunctions(absPath, FileChange) -> ([]FunctionSize, *FileSize, error)

FunctionInfo  { File, Line, EndLine, Name }
FunctionSize  { FunctionInfo, Lines }
FileSize      { Path, Lines }
```

### ComplexityCalculator

Computes cognitive complexity per function using the language's control flow constructs.

```
ComplexityCalculator
  AnalyzeFile(absPath, FileChange) -> ([]FunctionComplexity, error)

FunctionComplexity  { FunctionInfo, Complexity int }
```

### ComplexityScorer

Lightweight complexity scoring for churn weighting. May reuse `ComplexityCalculator` or be a faster approximation.

```
ComplexityScorer
  ScoreFile(absPath, FileChange) -> ([]FunctionComplexity, error)
```

### ImportResolver

Detects the project's module root and scans package-level imports to build the dependency graph.

```
ImportResolver
  DetectModulePath(repoPath) -> (string, error)
  ScanPackageImports(repoPath, pkgDir, modulePath) -> map[string]map[string]bool
```

### MutantGenerator

Finds mutation sites in source code within changed regions.

```
MutantGenerator
  GenerateMutants(absPath, FileChange, disabledLines map[int]bool) -> ([]MutantSite, error)

MutantSite  { File, Line, Description, Operator }
```

Operator names must use the canonical names so tiering works:
`conditional_boundary`, `negate_conditional`, `math_operator`, `return_value`,
`boolean_substitution`, `incdec`, `branch_removal`, `statement_deletion`

New language-specific operators may be added but must be registered in `tiers.go`.

### MutantApplier

Applies a mutation to a source file and returns the modified source bytes.

```
MutantApplier
  ApplyMutation(absPath, MutantSite) -> ([]byte, error)
```

### AnnotationScanner

Scans source files for `mutator-disable-*` comments and returns the set of source lines to skip.

```
AnnotationScanner
  ScanAnnotations(absPath) -> (disabledLines map[int]bool, error)
```

### TestRunner

Executes the test suite against mutated code and reports whether the mutation was killed.

```
TestRunner
  RunTest(TestRunConfig) -> (killed bool, output string, error)

TestRunConfig  { RepoPath, MutantFile, OriginalFile, Timeout, TestPattern, WorkDir, Index }
```

---

## Per-Language Implementation Checklist

Copy this checklist when adding Language X. Replace `<lang>` with the language name (e.g., `python`, `typescript`).

### Phase 0: Research and prerequisites

- [ ] **Parser selection**: Identify how to parse `<lang>` source from Go. Options:
  - Tree-sitter (`github.com/smacker/go-tree-sitter`) -- works for any language with a grammar
  - Shell out to a helper script (`python3 -c "import ast; ..."`) -- simpler but adds runtime dep
  - Language-specific Go library (if one exists)
- [ ] **Test runner**: Identify the test command for `<lang>` (e.g., `pytest`, `jest`, `cargo test`, `mvn test`)
- [ ] **Test isolation**: Determine mutation isolation strategy (see [Key Design Decisions](#key-design-decisions))
- [ ] **Module manifest**: Identify the project manifest file (`pyproject.toml`, `package.json`, `Cargo.toml`, `pom.xml`)
- [ ] **Import system**: Document how imports work -- relative vs absolute, aliasing, re-exports
- [ ] **Test file conventions**: Document how test files are identified (naming, directory, annotations)
- [ ] **Comment syntax**: Document single-line and multi-line comment syntax
- [ ] **Function declaration patterns**: Document all forms -- standalone functions, class methods, lambdas, closures, nested functions, arrow functions, etc.

### Phase 1: FileFilter

- [ ] Create `internal/lang/<lang>analyzer/` package directory
- [ ] Define source file extensions (e.g., `.py`, `.ts`+`.tsx`, `.rs`, `.java`)
- [ ] Implement `IsTestFile()`:
  - Python: `test_*.py`, `*_test.py`, files under `tests/` or `test/` directories
  - TypeScript/JS: `*.test.ts`, `*.spec.ts`, `*.test.js`, `*.spec.js`, files under `__tests__/`
  - Rust: files under `tests/` directory (inline `#[cfg(test)]` modules are harder -- may need AST)
  - Java: `*Test.java`, `*Tests.java`, files under `src/test/`
- [ ] Define `DiffGlobs` for `git diff`
- [ ] **Tests**: correct extensions included, test files excluded, edge cases (e.g., `testutils.py` should NOT be excluded)

### Phase 2: FunctionExtractor (unlocks sizes analyzer)

- [ ] Parse source files and identify function/method declarations
- [ ] Extract function name including class/module prefix:
  - Python: `ClassName.method_name`, standalone `function_name`
  - TypeScript: `ClassName.methodName`, `functionName`, arrow functions assigned to `const`
  - Rust: `impl Type::method_name`, standalone `fn function_name`
  - Java: `ClassName.methodName`
- [ ] Extract start line and end line for each function
- [ ] Compute line count (`end - start + 1`)
- [ ] Compute total file line count
- [ ] Filter to only functions overlapping the `FileChange` regions
- [ ] **Tests**: empty file, single function, multiple functions, class methods, nested functions, decorators/annotations, out-of-range filtering

### Phase 3: ComplexityCalculator (unlocks complexity analyzer)

- [ ] Implement cognitive complexity scoring. Map language constructs to increments:

| Increment | Go (reference) | Python | TypeScript/JS | Rust | Java |
|-----------|----------------|--------|---------------|------|------|
| +1 base | `if`, `for`, `switch`, `select` | `if`, `for`, `while`, `try`, `with` | `if`, `for`, `while`, `switch`, `try` | `if`, `for`, `while`, `loop`, `match` | `if`, `for`, `while`, `switch`, `try` |
| +1 nesting | per nesting level | per nesting level | per nesting level | per nesting level | per nesting level |
| +1 else | `else`, `else if` | `elif`, `else` | `else`, `else if` | `else`, `else if` | `else`, `else if` |
| +1 logical op | `&&`, `\|\|` | `and`, `or` | `&&`, `\|\|` | `&&`, `\|\|` | `&&`, `\|\|` |
| +1 op switch | operator changes in sequence | operator changes in sequence | operator changes in sequence | operator changes in sequence | operator changes in sequence |

- [ ] Handle language-specific patterns:
  - Python: comprehensions (list/dict/set/generator), `lambda`, walrus `:=` in conditions, `except` clauses
  - TypeScript/JS: ternary `? :`, optional chaining `?.`, nullish coalescing `??`, arrow functions in callbacks
  - Rust: `?` operator, `if let`/`while let`, `match` arms, closure complexity
  - Java: ternary `? :`, enhanced for-each, try-with-resources, lambda expressions, streams
- [ ] **Tests**: empty function (score=0), each control flow type, nesting penalties, logical operators, language-specific patterns

### Phase 4: ComplexityScorer (unlocks churn analyzer)

- [ ] Implement a scoring function for churn weighting
- [ ] Can be the same as `ComplexityCalculator` if fast enough, or a simplified approximation (count control flow keywords)
- [ ] **Tests**: verify scores are consistent with `ComplexityCalculator` (or document the approximation)

### Phase 5: ImportResolver (unlocks deps analyzer)

- [ ] Implement `DetectModulePath()`:
  - Python: parse `pyproject.toml` `[project] name`, or `setup.py`/`setup.cfg`, or fall back to directory name
  - TypeScript/JS: parse `package.json` `name` field
  - Rust: parse `Cargo.toml` `[package] name`
  - Java: parse `pom.xml` `<groupId>:<artifactId>`, or `build.gradle` `group` + project name
- [ ] Implement `ScanPackageImports()`:
  - Python: scan `import X` and `from X import Y` statements, resolve relative imports (`.foo` -> parent package), filter to internal packages
  - TypeScript/JS: scan `import {} from './path'` and `require('./path')`, resolve relative paths, filter to internal modules
  - Rust: scan `use crate::` and `mod` declarations, map to internal crate modules
  - Java: scan `import com.example.foo.Bar` statements, filter by project package prefix
- [ ] Define what "internal" means for this language (same module/package vs third-party)
- [ ] **Tests**: module path detection, internal import identification, external import filtering, relative import resolution

### Phase 6: AnnotationScanner (for mutation testing)

- [ ] Define annotation syntax using the language's comment style:
  - Python: `# mutator-disable-next-line`, `# mutator-disable-func`
  - TypeScript/JS: `// mutator-disable-next-line`, `// mutator-disable-func`
  - Rust: `// mutator-disable-next-line`, `// mutator-disable-func`
  - Java: `// mutator-disable-next-line`, `// mutator-disable-func`
- [ ] Implement function range detection (needed for `mutator-disable-func` to know which lines to skip)
- [ ] Return `map[int]bool` of disabled source line numbers
- [ ] **Tests**: next-line annotation disables the following line, function annotation disables all lines in function, no annotations returns empty map, irrelevant comments are ignored

### Phase 7: MutantGenerator (for mutation testing)

- [ ] Map the 8 canonical mutation operators to language-specific patterns:

| Operator | Category | Go (reference) | Applicability Notes |
|----------|----------|----------------|-------------------|
| `conditional_boundary` | Tier 1 | `>` to `>=`, `<` to `<=` | Universal across all languages |
| `negate_conditional` | Tier 1 | `==` to `!=`, `>` to `<` | Universal. TS/JS: include `===`/`!==` |
| `math_operator` | Tier 1 | `+` to `-`, `*` to `/` | Universal. Python: include `//` (floor div), `**` (power) |
| `return_value` | Tier 1 | Replace returns with `nil` | Language-specific zero values: Python `None`, JS `null`/`undefined`, Rust `Default::default()`, Java `null`/`0`/`false` |
| `boolean_substitution` | Tier 2 | `true` to `false` | Python: `True`/`False`. Rust: same. Universal otherwise |
| `incdec` | Tier 2 | `++` to `--` | Python/Rust: N/A (no `++`/`--` operators). Skip for these languages |
| `branch_removal` | Tier 3 | Empty the body of `if` | Universal. Python: replace body with `pass` |
| `statement_deletion` | Tier 3 | Remove bare function calls | Universal |

- [ ] Consider language-specific additional operators (register in `tiers.go` with appropriate tier):
  - Python: `is`/`is not` mutations, `in`/`not in` mutations
  - TypeScript: `===`/`!==` mutations, optional chaining `?.` removal, nullish coalescing `??` to `||`
  - Rust: `unwrap()` removal, `?` operator removal, `Some(x)` to `None`
  - Java: null-check removal, `equals()` to `==` swap, exception swallowing
- [ ] Filter mutants to only changed lines (respect `FileChange` regions)
- [ ] Exclude disabled lines (from `AnnotationScanner`)
- [ ] **Tests**: each operator type generates correct mutants, out-of-range lines are skipped, disabled lines are respected

### Phase 8: MutantApplier (for mutation testing)

- [ ] Choose mutation application strategy:
  - **AST-based** (preferred if a good parser is available): parse file, modify AST node, render back to source
  - **Text-based** (fallback): use line/column positions from `MutantSite` to do string replacement
- [ ] Handle edge cases: multiple operators on the same line, multi-line expressions, comment-only lines
- [ ] Verify that applied mutations produce syntactically valid source code
- [ ] **Tests**: each mutation type applied correctly, parse error returns nil, line mismatch returns nil

### Phase 9: TestRunner (for mutation testing)

- [ ] Implement test command construction:
  - Python: `pytest [--timeout=<T>] [-k <pattern>] <dir>`
  - TypeScript/JS: `npx jest [--testPathPattern <pattern>] --forceExit` or `npx vitest run`
  - Rust: `cargo test [<pattern>] -- --test-threads=1`
  - Java: `mvn test -Dtest=<pattern> -pl <module>` or `gradle test --tests <pattern>`
- [ ] Implement mutation isolation strategy:
  - **Go (reference)**: Uses `go test -overlay` -- mutant files are overlaid at build time, no file copying needed, fully parallel
  - **All other languages**: Use temp-copy strategy:
    1. Copy original file to backup location
    2. Write mutated source in place of original
    3. Run test command
    4. Restore original from backup
    5. **Critical**: Mutants on the same file must be serialized (acquire per-file lock). Mutants on different files can run in parallel.
  - Alternative per-language isolation (if available):
    - Python: `importlib` tricks or `PYTHONPATH` manipulation
    - TypeScript: Jest `moduleNameMapper` config
    - Rust: `cargo test` doesn't support overlay; temp-copy is the only option
- [ ] Handle test timeout (kill process after `TestRunConfig.Timeout`)
- [ ] Detect kill vs survive: test command exit code != 0 means killed
- [ ] **Tests**: killed mutant (test fails), survived mutant (test passes), timeout handling, file restoration after crash

### Phase 10: Integration and registration

- [ ] Create `internal/lang/<lang>analyzer/<lang>analyzer.go` implementing the `Language` interface
- [ ] Add `init()` function calling `lang.Register()`
- [ ] Add blank import to `cmd/diffguard/main.go`: `_ "github.com/.../internal/lang/<lang>analyzer"`
- [ ] Write end-to-end integration test:
  - Create a temp directory with a small `<lang>` project (2-3 files, 1 test file)
  - Run the full analyzer pipeline
  - Assert each report section has expected content
- [ ] Verify all existing Go tests still pass

### Phase 11: Documentation

- [ ] Add the language to README sections:
  - "Install" -- any additional toolchain requirements
  - "Usage" -- language-specific examples
  - "What It Measures" -- any scoring differences from the Go reference
  - "CLI Reference" -- new flags if any
  - "CI Integration" -- workflow examples for the language
- [ ] Document the annotation syntax for the language
- [ ] Document any language-specific mutation operators and their tier assignments
- [ ] Document known limitations (e.g., "Python closures are not analyzed individually")

---

## Language-Specific Notes

### Python

**Parser options**:
- **Tree-sitter** (`tree-sitter-python`): Best option from Go. No Python runtime needed. CST-based, so node types are strings (`"function_definition"`, `"if_statement"`).
- **Shell out to `python3 -c "import ast; ..."`**: Simpler for prototyping but adds Python as a runtime dependency.

**Test runner**: `pytest` (most common). Fall back to `unittest` (`python -m pytest` handles both).

**Isolation**: Temp-copy strategy. Python caches bytecode in `__pycache__/` -- set `PYTHONDONTWRITEBYTECODE=1` when running mutant tests to avoid stale cache.

**Unique complexity considerations**:
- List/dict/set/generator comprehensions should add +1 each (they're implicit loops)
- `with` statements add +1 (context manager control flow)
- `lambda` expressions: count complexity of the lambda body
- `try`/`except`/`finally`: +1 for `try`, +1 for each `except`, +1 for `finally`
- Decorators: don't count toward complexity (they're applied at definition time)

**Import system**:
- `import foo` -- absolute import
- `from foo import bar` -- absolute import
- `from . import bar` -- relative import (resolve against package path)
- `from ..foo import bar` -- relative import up two levels
- Distinguish internal vs external by checking if the import path starts with a package in the project

**Test file conventions**: `test_*.py`, `*_test.py`, files in `tests/` or `test/` directories. Also `conftest.py` (test infrastructure, not test files -- should be excluded from analysis but not treated as test files).

**Missing operators**: No `++`/`--` -- skip `incdec`. Add `is`/`is not` and `in`/`not in` as `negate_conditional` variants.

### TypeScript / JavaScript

**Parser options**:
- **Tree-sitter** (`tree-sitter-typescript`, `tree-sitter-javascript`): Works well. TypeScript and JavaScript need separate grammars.
- **Shell out to Node.js**: Could use `@babel/parser` or `typescript` compiler API via a helper script.

**Test runner**: Detect from `package.json`:
- `jest` or `@jest/core` in deps -> `npx jest`
- `vitest` in deps -> `npx vitest run`
- `mocha` in deps -> `npx mocha`
- Fall back to `npm test`

**Isolation**: Temp-copy strategy. Jest supports `moduleNameMapper` in config which could theoretically be used for overlay-like behavior, but temp-copy is simpler and more universal.

**Unique complexity considerations**:
- Ternary `condition ? a : b` adds +1 (it's a conditional)
- Optional chaining `foo?.bar` -- don't count (it's syntactic sugar, not control flow)
- Nullish coalescing `foo ?? bar` -- don't count (not branching in the cognitive sense)
- Arrow functions used as callbacks: count complexity of the body
- `async`/`await`: `try`/`catch` around `await` adds complexity; `await` alone does not
- Promise chains `.then().catch()` -- each `.catch()` adds +1

**Import system**:
- `import { x } from './local'` -- relative import (internal)
- `import { x } from 'package'` -- bare specifier (external)
- `require('./local')` -- CommonJS relative (internal)
- `require('package')` -- CommonJS bare (external)
- Distinguish internal by checking if the import path starts with `.` or `@/` (project alias)

**Test file conventions**: `*.test.ts`, `*.spec.ts`, `*.test.js`, `*.spec.js`, `*.test.tsx`, `*.spec.tsx`, files under `__tests__/` directories.

**Additional operators**: `===`/`!==` mutations (map to `negate_conditional`). Optional chaining removal (`foo?.bar` -> `foo.bar`, Tier 2). Nullish coalescing swap (`??` -> `||`, Tier 2).

### Rust

**Parser options**:
- **Tree-sitter** (`tree-sitter-rust`): Best option. Mature grammar.
- **Shell out to `rustc`**: Not practical. The `syn` crate is Rust-only.

**Test runner**: `cargo test`. Always available in Rust projects.

**Isolation**: Temp-copy strategy. `cargo test` recompiles from source, so replacing the file and running `cargo test` works. Set `CARGO_INCREMENTAL=0` to avoid stale incremental caches.

**Unique complexity considerations**:
- `match` arms: +1 for the `match` statement, +1 for each arm with a guard (`if` condition)
- `if let` / `while let`: +1 each (they're pattern-matching control flow)
- `?` operator: don't count (it's error propagation syntax, not branching)
- `loop` (infinite loop): +1
- Closures: count complexity of the closure body
- `unsafe` blocks: don't count toward complexity (they're a safety annotation, not control flow)

**Import system**:
- `use crate::foo::bar` -- internal crate import
- `use other_crate::foo` -- external crate import
- `mod foo;` -- module declaration (internal)
- Distinguish internal by checking if the path starts with `crate::` or `self::` or `super::`

**Test file conventions**: `tests/` directory contains integration tests. Unit tests are inline `#[cfg(test)] mod tests { ... }` -- these are harder to detect without parsing. For file filtering purposes, treat files in `tests/` as test files. For inline test modules, ignore them during analysis (they share the source file).

**Missing operators**: No `++`/`--` -- skip `incdec`. Add `unwrap()` removal (Tier 1, return_value variant), `?` removal (Tier 2), `Some(x)` to `None` (Tier 1, return_value variant).

### Java

**Parser options**:
- **Tree-sitter** (`tree-sitter-java`): Works well. Mature grammar.
- **Shell out to a Java parser**: Could use JavaParser as a CLI tool.

**Test runner**: Detect from build file:
- `pom.xml` present -> `mvn test -Dtest=<pattern>`
- `build.gradle` or `build.gradle.kts` present -> `gradle test --tests <pattern>`

**Isolation**: Temp-copy strategy. Both Maven and Gradle recompile from source. Replace the `.java` file, run tests, restore.

**Unique complexity considerations**:
- Enhanced for-each (`for (X x : collection)`) adds +1
- Try-with-resources: +1 for the `try` block
- `catch` clauses: +1 each
- `finally`: +1
- Ternary `? :`: +1
- Lambda expressions: count complexity of the lambda body
- Stream operations (`.filter()`, `.map()`, `.reduce()`): don't count individually (they're method calls)
- `synchronized` blocks: don't count (concurrency annotation, not control flow)
- `assert` statements: don't count

**Import system**:
- `import com.example.foo.Bar` -- fully qualified import
- `import com.example.foo.*` -- wildcard import
- Determine internal by checking if the import matches the project's group/package prefix

**Test file conventions**: `*Test.java`, `*Tests.java`, `*TestCase.java`, files under `src/test/java/`.

**Additional operators**: `null` check removal (remove `if (x == null)` guards, Tier 2). `equals()` to `==` swap (Tier 1, negate_conditional variant). Exception swallowing (empty `catch` body, Tier 3).

---

## Key Design Decisions

### Parser strategy

**Recommended: Tree-sitter for all non-Go languages.**

Tree-sitter provides Go bindings (`github.com/smacker/go-tree-sitter`) and has mature grammars for Python, TypeScript, JavaScript, Rust, Java, and many others. This avoids requiring language runtimes as dependencies (no need for Python, Node.js, etc. to be installed).

Trade-off: Tree-sitter returns a concrete syntax tree with string-based node kinds (`"if_statement"`, `"function_definition"`) rather than typed AST nodes. This means pattern matching is string-based rather than type-switch-based, but the uniformity across languages is worth it.

Go remains the exception -- it continues to use Go's standard library `go/ast` packages, which provide superior type safety and formatting preservation.

### Mutation isolation

| Language | Isolation Mechanism | Parallelism |
|----------|-------------------|-------------|
| Go | `go test -overlay` (build-level file substitution) | Fully parallel -- all mutants can run simultaneously |
| All others | Temp-copy: backup original, write mutant, run tests, restore | Parallel across files, serial within same file |

For non-Go languages, the `TestRunner` implementation must handle file locking internally. The mutation orchestrator calls `RunTest()` concurrently up to `--mutation-workers` goroutines. Each `TestRunner` acquires a per-file mutex before modifying the source file and releases it after restoration.

### Language detection

Auto-detect by scanning for manifest files at the repo root:

| File | Language |
|------|----------|
| `go.mod` | Go |
| `pyproject.toml`, `setup.py`, `setup.cfg` | Python |
| `package.json` + `.ts`/`.tsx` files | TypeScript |
| `package.json` + `.js`/`.jsx` files (no TS) | JavaScript |
| `Cargo.toml` | Rust |
| `pom.xml`, `build.gradle`, `build.gradle.kts` | Java |

If multiple languages are detected, require `--language` or analyze each language separately and merge report sections.

### Annotation syntax

Use the same annotation names across all languages, with the language-appropriate comment prefix:

| Language | Line disable | Function disable |
|----------|-------------|-----------------|
| Go | `// mutator-disable-next-line` | `// mutator-disable-func` |
| Python | `# mutator-disable-next-line` | `# mutator-disable-func` |
| TypeScript/JS | `// mutator-disable-next-line` | `// mutator-disable-func` |
| Rust | `// mutator-disable-next-line` | `// mutator-disable-func` |
| Java | `// mutator-disable-next-line` | `// mutator-disable-func` |

### New CLI flags

```
--language string    Language to analyze (default: auto-detect)
--test-command string  Custom test command override (use {file} and {dir} placeholders)
```

The `--test-command` flag is an escape hatch for projects with non-standard test setups. Example: `--test-command "python -m pytest {dir} --timeout=30"`.

---

## Adding a New Language: Quick Reference

1. Create `internal/lang/<name>analyzer/` package
2. Implement all 9 sub-interfaces of `Language`
3. Add `init()` calling `lang.Register()`
4. Add blank import in `cmd/diffguard/main.go`
5. Add any new mutation operators to `internal/mutation/tiers.go`
6. Write unit tests for each interface implementation
7. Write one end-to-end integration test
8. Update README with language-specific examples
9. Follow the detailed [Per-Language Implementation Checklist](#per-language-implementation-checklist) above
