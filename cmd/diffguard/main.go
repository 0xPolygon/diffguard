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
	"github.com/0xPolygon/diffguard/internal/deadcode"
	"github.com/0xPolygon/diffguard/internal/deps"
	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/lang"
	_ "github.com/0xPolygon/diffguard/internal/lang/goanalyzer"
	_ "github.com/0xPolygon/diffguard/internal/lang/rustanalyzer"
	_ "github.com/0xPolygon/diffguard/internal/lang/tsanalyzer"
	"github.com/0xPolygon/diffguard/internal/mutation"
	"github.com/0xPolygon/diffguard/internal/report"
	"github.com/0xPolygon/diffguard/internal/sizes"
)

func main() {
	var cfg Config
	flag.IntVar(&cfg.ComplexityThreshold, "complexity-threshold", 10, "Maximum cognitive complexity per function")
	flag.IntVar(&cfg.ComplexityDeltaTolerance, "complexity-delta-tolerance", 3, "In diff mode, ignore complexity regressions where head exceeds base by this much or less; brand-new functions still gated by --complexity-threshold")
	flag.IntVar(&cfg.FunctionSizeThreshold, "function-size-threshold", 50, "Maximum lines per function")
	flag.IntVar(&cfg.FileSizeThreshold, "file-size-threshold", 500, "Maximum lines per file")
	flag.BoolVar(&cfg.SkipMutation, "skip-mutation", false, "Skip mutation testing")
	flag.BoolVar(&cfg.SkipDeadCode, "skip-deadcode", false, "Skip dead code (unused symbol) detection")
	flag.BoolVar(&cfg.SkipGenerated, "skip-generated", true, "Skip files marked as generated (for example `Code generated ... DO NOT EDIT`)")
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
	ComplexityThreshold      int
	ComplexityDeltaTolerance int
	FunctionSizeThreshold    int
	FileSizeThreshold        int
	SkipMutation          bool
	SkipDeadCode          bool
	SkipGenerated         bool
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

// langResult bundles the per-language analysis output so the orchestrator
// can merge sections after every language has been processed.
type langResult struct {
	lang     lang.Language
	diff     *diff.Result
	sections []report.Section
}

// run resolves the language set (explicit --language flag or auto-detect via
// manifest scan), then invokes the analyzer pipeline once per language and
// merges the resulting sections into a single report.
func run(repoPath string, cfg Config) error {
	languages, err := resolveLanguages(repoPath, cfg.Language)
	if err != nil {
		return err
	}

	results, done, err := collectLanguageResults(repoPath, cfg, languages)
	if err != nil || done {
		return err
	}
	if len(results) == 0 {
		fmt.Printf("No %s files found.\n", languageNoun(languages[0]))
		return nil
	}

	rpt := report.Report{Sections: mergeLanguageSections(results)}
	if err := writeReport(rpt, cfg.Output); err != nil {
		return err
	}
	return checkExitCode(rpt, cfg.FailOn)
}

// collectLanguageResults runs the analyzer pipeline once per language and
// returns the per-language sections. `done` is true when a single-language
// run discovered no files (the legacy byte-identical "No X files found."
// message has been emitted and run() should exit without writing a report).
func collectLanguageResults(repoPath string, cfg Config, languages []lang.Language) ([]langResult, bool, error) {
	var results []langResult
	for _, l := range languages {
		r, skip, done, err := analyzeLanguage(repoPath, cfg, l, len(languages))
		if err != nil {
			return nil, false, err
		}
		if done {
			return nil, true, nil
		}
		if skip {
			continue
		}
		results = append(results, r)
	}
	return results, false, nil
}

// analyzeLanguage runs the pipeline for one language. Returns:
//   - (result, false, false, nil) when analysis ran and produced sections.
//   - (_, true, false, nil)       when the language contributed no files in a
//     multi-language run (skipped, a status line is emitted to stderr).
//   - (_, _, true, nil)           when a single-language run found no files
//     (the caller should exit without writing a report — legacy UX).
//   - (_, _, _, err)              on pipeline failure.
func analyzeLanguage(repoPath string, cfg Config, l lang.Language, numLanguages int) (langResult, bool, bool, error) {
	d, err := loadFiles(repoPath, cfg, diffFilter(repoPath, cfg, l))
	if err != nil {
		return langResult{}, false, false, err
	}
	if len(d.Files) == 0 {
		if numLanguages == 1 {
			fmt.Printf("No %s files found.\n", languageNoun(l))
			return langResult{}, false, true, nil
		}
		fmt.Fprintf(os.Stderr, "No %s files found; skipping.\n", languageNoun(l))
		return langResult{}, true, false, nil
	}
	announceRun(d, cfg, l, numLanguages)
	sections, err := runAnalyses(repoPath, d, cfg, l)
	if err != nil {
		return langResult{}, false, false, err
	}
	return langResult{lang: l, diff: d, sections: sections}, false, false, nil
}

// mergeLanguageSections flattens per-language sections into a single list.
// In a multi-language run each section name is suffixed with `[<lang>]` and
// the combined list is sorted lexicographically for stable ordering.
func mergeLanguageSections(results []langResult) []report.Section {
	multi := len(results) > 1
	var allSections []report.Section
	for _, r := range results {
		for _, s := range r.sections {
			if multi {
				s.Name = fmt.Sprintf("%s [%s]", s.Name, r.lang.Name())
			}
			allSections = append(allSections, s)
		}
	}
	if multi {
		sort.SliceStable(allSections, func(i, j int) bool {
			return allSections[i].Name < allSections[j].Name
		})
	}
	return allSections
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

	complexitySection, err := complexity.Analyze(repoPath, d, cfg.ComplexityThreshold, cfg.ComplexityDeltaTolerance, l.ComplexityCalculator())
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

	if !cfg.SkipDeadCode {
		deadcodeSection, err := deadcode.Analyze(repoPath, d, l.DeadCodeDetector())
		if err != nil {
			return nil, fmt.Errorf("dead code analysis: %w", err)
		}
		sections = append(sections, deadcodeSection)
	}

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
func diffFilter(repoPath string, cfg Config, l lang.Language) diff.Filter {
	f := l.FileFilter()
	includes := f.IncludesSource
	if cfg.SkipGenerated {
		includes = func(path string) bool {
			if !f.IncludesSource(path) {
				return false
			}
			return !diff.IsGeneratedFile(repoPath, path)
		}
	}
	return diff.Filter{
		DiffGlobs: f.DiffGlobs,
		Includes:  includes,
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
