package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	flag.StringVar(&cfg.Language, "language", "", "Comma-separated languages to analyze (e.g. 'go' or 'rust,typescript'); empty = auto-detect")
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
	Language              string
}

// run resolves the language set (explicit --language flag or auto-detect via
// manifest scan), then invokes the analyzer pipeline once per language and
// merges the resulting sections into a single report.
func run(repoPath string, cfg Config) error {
	languages, err := resolveLanguages(repoPath, cfg.Language)
	if err != nil {
		return err
	}

	// Collect per-language analysis. suffix-per-section only when more than
	// one language contributes, so the single-language invocation stays
	// byte-identical to the pre-multi-language output.
	type langResult struct {
		lang     lang.Language
		diff     *diff.Result
		sections []report.Section
	}
	var results []langResult
	for _, l := range languages {
		d, err := loadFiles(repoPath, cfg, diffFilter(l))
		if err != nil {
			return err
		}
		if len(d.Files) == 0 {
			// Empty language: report nothing for it. When only one language
			// is in play we preserve the legacy UX with a specific message.
			if len(languages) == 1 {
				fmt.Printf("No %s files found.\n", languageNoun(l))
				return nil
			}
			fmt.Fprintf(os.Stderr, "No %s files found; skipping.\n", languageNoun(l))
			continue
		}
		announceRun(d, cfg, l, len(languages))
		sections, err := runAnalyses(repoPath, d, cfg, l)
		if err != nil {
			return err
		}
		results = append(results, langResult{lang: l, diff: d, sections: sections})
	}

	if len(results) == 0 {
		fmt.Printf("No %s files found.\n", languageNoun(languages[0]))
		return nil
	}

	var allSections []report.Section
	multi := len(results) > 1
	for _, r := range results {
		for _, s := range r.sections {
			if multi {
				s.Name = fmt.Sprintf("%s [%s]", s.Name, r.lang.Name())
			}
			allSections = append(allSections, s)
		}
	}

	// When multi-language, sort by (language, metric) lexicographically so
	// section ordering is stable across runs and hosts.
	if multi {
		sort.SliceStable(allSections, func(i, j int) bool {
			return allSections[i].Name < allSections[j].Name
		})
	}

	rpt := report.Report{Sections: allSections}
	if err := writeReport(rpt, cfg.Output); err != nil {
		return err
	}
	return checkExitCode(rpt, cfg.FailOn)
}

// resolveLanguages turns the --language flag value (or auto-detect) into a
// concrete list of Language implementations. Unknown names in the flag are
// a hard error; an empty detection set is a hard error with a suggestion
// to pass --language.
func resolveLanguages(repoPath, flagValue string) ([]lang.Language, error) {
	if flagValue == "" {
		langs := lang.Detect(repoPath)
		if len(langs) == 0 {
			return nil, fmt.Errorf("no supported language detected; pass --language to override (see --help)")
		}
		return langs, nil
	}

	var out []lang.Language
	seen := map[string]bool{}
	for _, name := range strings.Split(flagValue, ",") {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		l, ok := lang.Get(name)
		if !ok {
			return nil, fmt.Errorf("unknown language %q (registered: %s)", name, strings.Join(registeredNames(), ", "))
		}
		out = append(out, l)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty --language flag")
	}
	// Sort for determinism, matching lang.All()/Detect() behavior.
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

func registeredNames() []string {
	all := lang.All()
	names := make([]string, len(all))
	for i, l := range all {
		names[i] = l.Name()
	}
	return names
}

// languageNoun returns the human-friendly noun for status messages. For Go
// we preserve the legacy capitalized form ("No Go files found.") so
// single-language output stays byte-identical.
func languageNoun(l lang.Language) string {
	switch l.Name() {
	case "go":
		return "Go"
	case "rust":
		return "Rust"
	case "typescript":
		return "TypeScript"
	default:
		return l.Name()
	}
}

func announceRun(d *diff.Result, cfg Config, l lang.Language, numLanguages int) {
	noun := languageNoun(l)
	// For a single-language run, preserve the legacy message exactly:
	// "Analyzing N changed Go files against main..." / refactoring-mode
	// phrasing. Multi-language adds a bracketed suffix.
	suffix := ""
	if numLanguages > 1 {
		suffix = fmt.Sprintf(" [%s]", l.Name())
	}
	if cfg.Paths != "" {
		fmt.Fprintf(os.Stderr, "Analyzing %d %s files (refactoring mode)%s...\n", len(d.Files), noun, suffix)
	} else {
		fmt.Fprintf(os.Stderr, "Analyzing %d changed %s files against %s%s...\n", len(d.Files), noun, cfg.BaseBranch, suffix)
	}
}

func runAnalyses(repoPath string, d *diff.Result, cfg Config, l lang.Language) ([]report.Section, error) {
	var sections []report.Section

	complexitySection, err := complexity.Analyze(repoPath, d, cfg.ComplexityThreshold, l.ComplexityCalculator())
	if err != nil {
		return nil, fmt.Errorf("complexity analysis: %w", err)
	}
	sections = append(sections, complexitySection)

	sizesSection, err := sizes.Analyze(repoPath, d, cfg.FunctionSizeThreshold, cfg.FileSizeThreshold, l.FunctionExtractor())
	if err != nil {
		return nil, fmt.Errorf("size analysis: %w", err)
	}
	sections = append(sections, sizesSection)

	depsSection, err := deps.Analyze(repoPath, d, l.ImportResolver())
	if err != nil {
		return nil, fmt.Errorf("dependency analysis: %w", err)
	}
	sections = append(sections, depsSection)

	churnSection, err := churn.Analyze(repoPath, d, cfg.ComplexityThreshold, l.ComplexityScorer())
	if err != nil {
		return nil, fmt.Errorf("churn analysis: %w", err)
	}
	sections = append(sections, churnSection)

	if !cfg.SkipMutation {
		mutationSection, err := mutation.Analyze(repoPath, d, l, mutation.Options{
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
