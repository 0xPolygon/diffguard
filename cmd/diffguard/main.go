package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/0xPolygon/diffguard/internal/churn"
	"github.com/0xPolygon/diffguard/internal/complexity"
	"github.com/0xPolygon/diffguard/internal/deps"
	"github.com/0xPolygon/diffguard/internal/diff"
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
	flag.StringVar(&cfg.Output, "output", "text", "Output format: text, json")
	flag.StringVar(&cfg.FailOn, "fail-on", "warn", "Exit non-zero if thresholds breached: none, warn, all")
	flag.StringVar(&cfg.BaseBranch, "base", "", "Base branch to diff against (default: auto-detect)")
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

	if cfg.BaseBranch == "" {
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
	Output                string
	FailOn                string
	BaseBranch            string
}

func run(repoPath string, cfg Config) error {
	d, err := diff.Parse(repoPath, cfg.BaseBranch)
	if err != nil {
		return fmt.Errorf("parsing diff: %w", err)
	}

	if len(d.Files) == 0 {
		fmt.Println("No changed Go files found in diff.")
		return nil
	}

	fmt.Fprintf(os.Stderr, "Analyzing %d changed Go files against %s...\n", len(d.Files), cfg.BaseBranch)

	var sections []report.Section

	complexitySection, err := complexity.Analyze(repoPath, d, cfg.ComplexityThreshold)
	if err != nil {
		return fmt.Errorf("complexity analysis: %w", err)
	}
	sections = append(sections, complexitySection)

	sizesSection, err := sizes.Analyze(repoPath, d, cfg.FunctionSizeThreshold, cfg.FileSizeThreshold)
	if err != nil {
		return fmt.Errorf("size analysis: %w", err)
	}
	sections = append(sections, sizesSection)

	depsSection, err := deps.Analyze(repoPath, d)
	if err != nil {
		return fmt.Errorf("dependency analysis: %w", err)
	}
	sections = append(sections, depsSection)

	churnSection, err := churn.Analyze(repoPath, d, cfg.ComplexityThreshold)
	if err != nil {
		return fmt.Errorf("churn analysis: %w", err)
	}
	sections = append(sections, churnSection)

	if !cfg.SkipMutation {
		mutationSection, err := mutation.Analyze(repoPath, d, cfg.MutationSampleRate)
		if err != nil {
			return fmt.Errorf("mutation analysis: %w", err)
		}
		sections = append(sections, mutationSection)
	}

	r := report.Report{Sections: sections}

	switch cfg.Output {
	case "json":
		if err := report.WriteJSON(os.Stdout, r); err != nil {
			return fmt.Errorf("writing JSON report: %w", err)
		}
	default:
		report.WriteText(os.Stdout, r)
	}

	return checkExitCode(r, cfg.FailOn)
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
