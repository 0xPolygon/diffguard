package tsanalyzer_test

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/0xPolygon/diffguard/internal/lang/evalharness"
)

// EVAL-3 — TypeScript correctness evaluation suite.
//
// Each test below drives the built diffguard binary against a fixture
// under evaldata/<name>/ and compares the emitted report to
// expected.json. Semantic matching (section + severity + finding
// file/function) keeps the tests robust against line-number drift.
//
// Mutation tests run the fixture's `npm test` which shells out to
// `node --experimental-strip-types` — that flag is enabled by default
// in Node 22.6+, so we require a minimum Node on PATH. When `node` is
// missing the test skips cleanly.
//
// Follow-up TODOs (for the verifier agent to pick up):
//
//   - EVAL-3 sizes (file): add a >500-LOC fixture + negative control.
//   - EVAL-3 deps (internal vs external): assert directly on the graph
//     shape (lodash excluded, ./foo included) rather than just pass/fail.
//   - EVAL-3 churn: needs seeded git history.
//   - EVAL-3 mutation (annotation respect): exercise
//     `// mutator-disable-next-line` end-to-end; currently covered at
//     unit level in mutation_annotate_test.go.

var binBuilder evalharness.BinaryBuilder

func fixtureDir(t *testing.T, name string) string {
	t.Helper()
	wd, err := filepath.Abs(filepath.Join("evaldata", name))
	if err != nil {
		t.Fatal(err)
	}
	return wd
}

func runEvalFixture(t *testing.T, name string, extraFlags []string) {
	t.Helper()

	binary := binBuilder.GetBinary(t, evalharness.RepoRoot(t))
	repo := evalharness.CopyFixture(t, fixtureDir(t, name))

	flags := append([]string{
		"--paths", ".",
		"--language", "typescript",
	}, extraFlags...)

	rpt := evalharness.RunBinary(t, binary, repo, flags)
	exp, ok := evalharness.LoadExpectation(t, fixtureDir(t, name))
	if !ok {
		t.Fatalf("fixture %s has no expected.json", name)
	}
	evalharness.AssertMatches(t, rpt, exp)
}

func TestEval_Complexity_Positive(t *testing.T) {
	runEvalFixture(t, "complexity_positive", []string{"--skip-mutation"})
}

func TestEval_Complexity_Negative(t *testing.T) {
	runEvalFixture(t, "complexity_negative", []string{"--skip-mutation"})
}

func TestEval_Sizes_Function_Positive(t *testing.T) {
	runEvalFixture(t, "sizes_positive", []string{"--skip-mutation"})
}

func TestEval_Sizes_Function_Negative(t *testing.T) {
	runEvalFixture(t, "sizes_negative", []string{"--skip-mutation"})
}

func TestEval_Deps_Cycle_Positive(t *testing.T) {
	runEvalFixture(t, "deps_cycle_positive", []string{"--skip-mutation"})
}

func TestEval_Deps_Cycle_Negative(t *testing.T) {
	runEvalFixture(t, "deps_cycle_negative", []string{"--skip-mutation"})
}

func TestEval_Mutation_Kill_Positive(t *testing.T) {
	requireNode(t)
	if testing.Short() {
		t.Skip("skipping mutation eval in -short mode")
	}
	runEvalFixture(t, "mutation_kill_positive", mutationFlags())
}

func TestEval_Mutation_Kill_Negative(t *testing.T) {
	requireNode(t)
	if testing.Short() {
		t.Skip("skipping mutation eval in -short mode")
	}
	runEvalFixture(t, "mutation_kill_negative", mutationFlags())
}

func TestEval_Mutation_TSOp_Positive(t *testing.T) {
	requireNode(t)
	if testing.Short() {
		t.Skip("skipping mutation eval in -short mode")
	}
	runEvalFixture(t, "mutation_tsop_positive", mutationFlags())
}

func TestEval_Mutation_TSOp_Negative(t *testing.T) {
	requireNode(t)
	if testing.Short() {
		t.Skip("skipping mutation eval in -short mode")
	}
	runEvalFixture(t, "mutation_tsop_negative", mutationFlags())
}

// requireNode skips the test when `node` or `npm` isn't on $PATH. The
// fixture's package.json uses `npm test` which in turn runs node; both
// must be present.
func requireNode(t *testing.T) {
	t.Helper()
	for _, cmd := range []string{"node", "npm"} {
		if _, err := exec.LookPath(cmd); err != nil {
			t.Skipf("%s not on PATH; skipping mutation eval", cmd)
		}
	}
}

func mutationFlags() []string {
	return []string{
		"--mutation-sample-rate", "100",
		"--mutation-workers", "2",
		"--test-timeout", "60s",
	}
}
