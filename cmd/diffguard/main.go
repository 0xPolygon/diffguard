package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/0xPolygon/diffguard/internal/churn"
	"github.com/0xPolygon/diffguard/internal/complexity"
	"github.com/0xPolygon/diffguard/internal/deps"
	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
	_ "github.com/0xPolygon/diffguard/internal/lang/goanalyzer"
	"github.com/0xPolygon/diffguard/internal/mutation"
	"github.com/0xPolygon/diffguard/internal/report"
	"github.com/0xPolygon/diffguard/internal/sizes"
)

func main() {
	var cfg Config
	flag.IntVar(&cfg.ComplexityThreshold, "complexity-threshold", 10, "Maximum cognitive complexity per function")
	flag.IntVar(&cfg.FunctionSizeThreshold, "function-size-threshold", 50, "Maximum lines per function")
	flag.IntVar(&cfg.FileSizeThreshold, "file-size-threshold", 500, "Maximum lines per file")
	flag.BoolVar(&cfg.SkipMutation, "skip-mutation", false, "Skip mutation testing")
	flag.Float64Var(&cfg.MutationSampleRate, "mutation-sample-rate", 100, "Percentage of mutants to test, 0-100")
	flag.DurationVar(&cfg.TestTimeout, "test-timeout", 30*time.Second, "Per-mutant test binary timeout (e.g. 60s, 2m)")
	flag.StringVar(&cfg.TestPattern, "test-pattern", "", "Test name pattern passed to `go test -run` for each mutant (speeds up mutation testing on packages with slow suites)")
	flag.IntVar(&cfg.MutationWorkers, "mutation-workers", 0, "Max packages processed concurrently during mutation testing; 0 = runtime.NumCPU()")
	flag.Float64Var(&cfg.Tier1Threshold, "tier1-threshold", 90, "Minimum kill % for Tier-1 (logic) mutations; below triggers FAIL")
	flag.Float64Var(&cfg.Tier2Threshold, "tier2-threshold", 70, "Minimum kill % for Tier-2 (semantic) mutations; below triggers WARN. Tier-3 (observability) is report-only.")
	flag.StringVar(&cfg.Output, "output", "text", "Output format: text, json")
	flag.StringVar(&cfg.FailOn, "fail-on", "warn", "Exit non-zero if thresholds breached: none, warn, all")
	flag.StringVar(&cfg.BaseBranch, "base", "", "Base branch to diff against (default: auto-detect)")
	flag.StringVar(&cfg.Paths, "paths", "", "Comma-separated files/dirs to analyze in full (refactoring mode); skips git diff")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Usage: diffguard [flags] <repo-path>\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	repoPath, err := filepath.Abs(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving repo path: %v\n", err)
		os.Exit(1)
	}

	if cfg.Paths == "" && cfg.BaseBranch == "" {
		cfg.BaseBranch = detectBaseBranch(repoPath)
	}

	if err := run(repoPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// Config holds CLI configuration.
type Config struct {
	ComplexityThreshold   int
	FunctionSizeThreshold int
	FileSizeThreshold     int
	SkipMutation          bool
	MutationSampleRate    float64
	TestTimeout           time.Duration
	TestPattern           string
	MutationWorkers       int
	Tier1Threshold        float64
	Tier2Threshold        float64
	Output                string
	FailOn                string
	BaseBranch            string
	Paths                 string
}

func run(repoPath string, cfg Config) error {
	goLang, ok := lang.Get("go")
	if !ok {
		return fmt.Errorf("go analyzer not registered")
	}
	filter := diffFilter(goLang)

	d, err := loadFiles(repoPath, cfg, filter)
	if err != nil {
		return err
	}

	if len(d.Files) == 0 {
		fmt.Println("No Go files found.")
		return nil
	}

	announceRun(d, cfg)

	sections, err := runAnalyses(repoPath, d, cfg)
	if err != nil {
		return err
	}

	r := report.Report{Sections: sections}
	if err := writeReport(r, cfg.Output); err != nil {
		return err
	}
	return checkExitCode(r, cfg.FailOn)
}

func announceRun(d *diff.Result, cfg Config) {
	if cfg.Paths != "" {
		fmt.Fprintf(os.Stderr, "Analyzing %d Go files (refactoring mode)...\n", len(d.Files))
	} else {
		fmt.Fprintf(os.Stderr, "Analyzing %d changed Go files against %s...\n", len(d.Files), cfg.BaseBranch)
	}
}

func runAnalyses(repoPath string, d *diff.Result, cfg Config) ([]report.Section, error) {
	var sections []report.Section

	complexitySection, err := complexity.Analyze(repoPath, d, cfg.ComplexityThreshold)
	if err != nil {
		return nil, fmt.Errorf("complexity analysis: %w", err)
	}
	sections = append(sections, complexitySection)

	sizesSection, err := sizes.Analyze(repoPath, d, cfg.FunctionSizeThreshold, cfg.FileSizeThreshold)
	if err != nil {
		return nil, fmt.Errorf("size analysis: %w", err)
	}
	sections = append(sections, sizesSection)

	depsSection, err := deps.Analyze(repoPath, d)
	if err != nil {
		return nil, fmt.Errorf("dependency analysis: %w", err)
	}
	sections = append(sections, depsSection)

	churnSection, err := churn.Analyze(repoPath, d, cfg.ComplexityThreshold)
	if err != nil {
		return nil, fmt.Errorf("churn analysis: %w", err)
	}
	sections = append(sections, churnSection)

	if !cfg.SkipMutation {
		mutationSection, err := mutation.Analyze(repoPath, d, mutation.Options{
			SampleRate:     cfg.MutationSampleRate,
			TestTimeout:    cfg.TestTimeout,
			TestPattern:    cfg.TestPattern,
			Workers:        cfg.MutationWorkers,
			Tier1Threshold: cfg.Tier1Threshold,
			Tier2Threshold: cfg.Tier2Threshold,
		})
		if err != nil {
			return nil, fmt.Errorf("mutation analysis: %w", err)
		}
		sections = append(sections, mutationSection)
	}
	return sections, nil
}

func writeReport(r report.Report, output string) error {
	if output == "json" {
		if err := report.WriteJSON(os.Stdout, r); err != nil {
			return fmt.Errorf("writing JSON report: %w", err)
		}
		return nil
	}
	report.WriteText(os.Stdout, r)
	return nil
}

func checkExitCode(r report.Report, failOn string) error {
	switch failOn {
	case "none":
		return nil
	case "all":
		if r.WorstSeverity() != report.SeverityPass {
			return fmt.Errorf("quality thresholds breached")
		}
	default: // "warn"
		if r.WorstSeverity() == report.SeverityFail {
			return fmt.Errorf("quality thresholds breached")
		}
	}
	return nil
}

func loadFiles(repoPath string, cfg Config, filter diff.Filter) (*diff.Result, error) {
	if cfg.Paths != "" {
		paths := strings.Split(cfg.Paths, ",")
		for i := range paths {
			paths[i] = strings.TrimSpace(paths[i])
		}
		d, err := diff.CollectPaths(repoPath, paths, filter)
		if err != nil {
			return nil, fmt.Errorf("collecting paths: %w", err)
		}
		return d, nil
	}
	d, err := diff.Parse(repoPath, cfg.BaseBranch, filter)
	if err != nil {
		return nil, fmt.Errorf("parsing diff: %w", err)
	}
	return d, nil
}

// diffFilter converts a language's lang.FileFilter into the diff.Filter
// shape the parser expects. The two shapes are intentionally different:
// lang.FileFilter exposes the fields languages need to declare their
// territory (extensions, IsTestFile, DiffGlobs), while diff.Filter only
// carries what the parser itself reads on each file (Includes + DiffGlobs).
func diffFilter(l lang.Language) diff.Filter {
	f := l.FileFilter()
	return diff.Filter{
		DiffGlobs: f.DiffGlobs,
		Includes:  f.IncludesSource,
	}
}

func detectBaseBranch(repoPath string) string {
	for _, branch := range []string{"develop", "main", "master"} {
		cmd := exec.Command("git", "rev-parse", "--verify", branch)
		cmd.Dir = repoPath
		if err := cmd.Run(); err == nil {
			return branch
		}
	}
	return "main"
}
