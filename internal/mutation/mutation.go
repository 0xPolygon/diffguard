// Package mutation orchestrates mutation testing across a diff's changed
// files. The AST-level work (generating mutants, applying them, scanning
// annotations, running tests) is provided by the language back-end. This
// package owns planning, scheduling, tiering, caching and report formatting
// — pieces that don't depend on any particular language.
package mutation

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
	"github.com/0xPolygon/diffguard/internal/report"
)

// Mutant represents a single mutation applied to the source code.
type Mutant struct {
	File        string
	Line        int
	Offset      int
	Description string
	Operator    string
	Killed      bool
	// Outcome is the classified result. Unset means "derive from Killed",
	// which keeps hand-built mutants in tests and legacy runners working.
	Outcome    lang.MutantOutcome
	Cached     bool
	TestOutput string
}

func (m Mutant) outcome() lang.MutantOutcome {
	if m.Outcome != lang.OutcomeUnset {
		return m.Outcome
	}
	if m.Killed {
		return lang.OutcomeKilled
	}
	return lang.OutcomeSurvived
}

func (m *Mutant) setResult(r lang.MutantResult) {
	m.Outcome = r.Outcome
	m.Killed = isKill(r.Outcome)
	if m.Killed {
		m.TestOutput = r.Output
	}
}

func (m Mutant) site() lang.MutantSite {
	return lang.MutantSite{
		File:        m.File,
		Line:        m.Line,
		Offset:      m.Offset,
		Description: m.Description,
		Operator:    m.Operator,
	}
}

func isKill(o lang.MutantOutcome) bool {
	return o == lang.OutcomeKilled || o == lang.OutcomeTimedOut
}

func isSurvivor(o lang.MutantOutcome) bool {
	return o == lang.OutcomeSurvived || o == lang.OutcomeNotCovered
}

// Options configures a mutation testing run.
type Options struct {
	// SampleRate is the percentage (0-100) of generated mutants to actually test.
	SampleRate float64
	// TestTimeout is the per-mutant timeout.
	// Zero means use the default (30s).
	TestTimeout time.Duration
	// TestPattern, if non-empty, is passed to the language's test runner to
	// scope tests.
	TestPattern string
	// Tier1Threshold is the minimum killed-percentage for Tier 1 operators
	// below which the section is reported as FAIL. Zero falls back to
	// defaultTier1Threshold.
	Tier1Threshold float64
	// Tier2Threshold is the minimum killed-percentage for Tier 2 operators
	// below which the section is reported as WARN. Zero falls back to
	// defaultTier2Threshold.
	Tier2Threshold float64
	// Workers caps the number of mutants processed concurrently. Zero or
	// negative means use runtime.NumCPU().
	Workers int
	// Context cancels the run; mutants not yet tested are reported as not
	// run. Nil means context.Background().
	Context context.Context
	// Budget caps the wall-clock time of the run. Zero means no cap.
	Budget time.Duration
	// Seed drives sampling and early-stop ordering. Zero means 1, so runs
	// are reproducible by default.
	Seed int64
	// ShardIndex/ShardCount select the 1-based ShardIndex-th of ShardCount
	// slices of the mutant list. ShardCount <= 1 disables sharding.
	ShardIndex int
	ShardCount int
	// CacheDir, if non-empty, stores outcomes keyed by package fingerprint
	// so unchanged packages are not re-tested.
	CacheDir string
	// Confidence (0-1), if positive, stops a gating tier early once its
	// pass/fail verdict is settled at this confidence level.
	Confidence float64
	// Progress receives periodic status lines. Nil disables them.
	Progress io.Writer
}

const (
	defaultTier1Threshold = 90.0
	defaultTier2Threshold = 70.0
	defaultTestTimeout    = 30 * time.Second
)

func (o Options) timeout() time.Duration {
	if o.TestTimeout <= 0 {
		return defaultTestTimeout
	}
	return o.TestTimeout
}

func (o Options) tier1Threshold() float64 {
	if o.Tier1Threshold <= 0 {
		return defaultTier1Threshold
	}
	return o.Tier1Threshold
}

func (o Options) tier2Threshold() float64 {
	if o.Tier2Threshold <= 0 {
		return defaultTier2Threshold
	}
	return o.Tier2Threshold
}

func (o Options) workers() int {
	if o.Workers <= 0 {
		return runtime.NumCPU()
	}
	return o.Workers
}

func (o Options) seed() int64 {
	if o.Seed == 0 {
		return 1
	}
	return o.Seed
}

// runContext returns the context mutants run under: the caller's context,
// bounded by the budget when one is set.
func (o Options) runContext() (context.Context, context.CancelFunc) {
	ctx := o.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if o.Budget <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, o.Budget)
}

// Analyze applies mutation operators to changed code (via the language's
// MutantGenerator) and tests each mutant, through the language's
// SessionRunner when it has one and its MutantApplier/TestRunner otherwise.
func Analyze(repoPath string, d *diff.Result, l lang.Language, opts Options) (report.Section, error) {
	mutants := planMutants(collectMutants(repoPath, d, l), opts)

	if len(mutants) == 0 {
		return report.Section{
			Name:     "Mutation Testing",
			Summary:  "No mutants generated from changed code",
			Severity: report.SeverityPass,
		}, nil
	}

	workDir, err := os.MkdirTemp("", "diffguard-mutation-")
	if err != nil {
		return report.Section{}, fmt.Errorf("creating mutation work dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	cache := openCache(opts, repoPath, l)
	cache.load(mutants)
	if err := runMutantsParallel(repoPath, mutants, l, opts, workDir); err != nil {
		return report.Section{}, err
	}
	cache.store(mutants)
	return buildSection(mutants, opts), nil
}

// collectMutants gathers mutation sites for every changed file, honoring
// the language's annotation scanner so lines marked
// `// mutator-disable-*` never produce mutants.
func collectMutants(repoPath string, d *diff.Result, l lang.Language) []Mutant {
	gen := l.MutantGenerator()
	scanner := l.AnnotationScanner()

	var all []Mutant
	for _, fc := range d.Files {
		absPath := filepath.Join(repoPath, fc.Path)
		disabled, err := scanner.ScanAnnotations(absPath)
		if err != nil {
			continue
		}
		sites, err := gen.GenerateMutants(absPath, fc, disabled)
		if err != nil {
			continue
		}
		for _, s := range sites {
			all = append(all, Mutant{
				File:        s.File,
				Line:        s.Line,
				Offset:      s.Offset,
				Description: s.Description,
				Operator:    s.Operator,
			})
		}
	}
	return all
}
