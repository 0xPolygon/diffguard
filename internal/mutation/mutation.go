package mutation

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/0xPolygon/diffguard/internal/diff"
	"github.com/0xPolygon/diffguard/internal/report"
)

// Mutant represents a single mutation applied to the source code.
type Mutant struct {
	File        string
	Line        int
	Description string
	Operator    string
	Killed      bool
	TestOutput  string
}

// Analyze applies mutation operators to changed code and runs tests.
func Analyze(repoPath string, d *diff.Result, sampleRate float64) (report.Section, error) {
	var allMutants []Mutant

	for _, fc := range d.Files {
		absPath := filepath.Join(repoPath, fc.Path)
		mutants, err := generateMutants(absPath, fc)
		if err != nil {
			continue
		}
		allMutants = append(allMutants, mutants...)
	}

	if len(allMutants) == 0 {
		return report.Section{
			Name:     "Mutation Testing",
			Summary:  "No mutants generated from changed code",
			Severity: report.SeverityPass,
		}, nil
	}

	if sampleRate < 100 {
		allMutants = sampleMutants(allMutants, sampleRate)
	}

	killed := 0
	for i := range allMutants {
		allMutants[i].Killed = runMutant(repoPath, &allMutants[i])
		if allMutants[i].Killed {
			killed++
		}
	}

	return buildSection(allMutants, killed), nil
}

// generateMutants parses a file and creates mutants for changed regions.
func generateMutants(absPath string, fc diff.FileChange) ([]Mutant, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, absPath, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	var mutants []Mutant

	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			return true
		}

		line := fset.Position(n.Pos()).Line
		if !fc.ContainsLine(line) {
			return true
		}

		switch node := n.(type) {
		case *ast.BinaryExpr:
			mutants = append(mutants, binaryMutants(fc.Path, line, node)...)
		case *ast.Ident:
			mutants = append(mutants, boolMutants(fc.Path, line, node)...)
		case *ast.ReturnStmt:
			mutants = append(mutants, returnMutants(fc.Path, line, node)...)
		}

		return true
	})

	return mutants, nil
}

// binaryMutants generates mutations for binary expressions.
func binaryMutants(file string, line int, expr *ast.BinaryExpr) []Mutant {
	replacements := map[token.Token][]token.Token{
		token.GTR: {token.GEQ},
		token.LSS: {token.LEQ},
		token.GEQ: {token.GTR},
		token.LEQ: {token.LSS},
		token.EQL: {token.NEQ},
		token.NEQ: {token.EQL},
		token.ADD: {token.SUB},
		token.SUB: {token.ADD},
		token.MUL: {token.QUO},
		token.QUO: {token.MUL},
	}

	targets, ok := replacements[expr.Op]
	if !ok {
		return nil
	}

	var mutants []Mutant
	for _, newOp := range targets {
		mutants = append(mutants, Mutant{
			File:        file,
			Line:        line,
			Description: fmt.Sprintf("%s -> %s", expr.Op, newOp),
			Operator:    operatorName(expr.Op, newOp),
		})
	}

	return mutants
}

// boolMutants generates true <-> false mutations.
func boolMutants(file string, line int, ident *ast.Ident) []Mutant {
	if ident.Name != "true" && ident.Name != "false" {
		return nil
	}

	newVal := "true"
	if ident.Name == "true" {
		newVal = "false"
	}

	return []Mutant{{
		File:        file,
		Line:        line,
		Description: fmt.Sprintf("%s -> %s", ident.Name, newVal),
		Operator:    "boolean_substitution",
	}}
}

// returnMutants generates zero-value return mutations.
func returnMutants(file string, line int, ret *ast.ReturnStmt) []Mutant {
	if len(ret.Results) == 0 {
		return nil
	}

	return []Mutant{{
		File:        file,
		Line:        line,
		Description: "replace return values with zero values",
		Operator:    "return_value",
	}}
}

// runMutant applies a mutation, runs tests, and returns whether it was killed.
func runMutant(repoPath string, m *Mutant) bool {
	absPath := filepath.Join(repoPath, m.File)

	original, err := os.ReadFile(absPath)
	if err != nil {
		return false
	}

	mutated := applyMutation(absPath, m)
	if mutated == nil {
		return false
	}

	if err := os.WriteFile(absPath, mutated, 0644); err != nil {
		return false
	}

	pkgDir := filepath.Dir(absPath)
	cmd := exec.Command("go", "test", "-count=1", "-timeout", "30s", "./...")
	cmd.Dir = pkgDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()

	os.WriteFile(absPath, original, 0644)

	if err != nil {
		m.TestOutput = stderr.String()
		return true
	}

	return false
}

// applyMutation re-parses the file and applies the specific mutation.
func applyMutation(absPath string, m *Mutant) []byte {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, absPath, nil, parser.ParseComments)
	if err != nil {
		return nil
	}

	if !applyMutationToAST(fset, f, m) {
		return nil
	}

	return renderFile(fset, f)
}

func applyMutationToAST(fset *token.FileSet, f *ast.File, m *Mutant) bool {
	applied := false
	ast.Inspect(f, func(n ast.Node) bool {
		if applied || n == nil {
			return false
		}
		if fset.Position(n.Pos()).Line != m.Line {
			return true
		}
		applied = tryApplyMutation(n, m)
		return !applied
	})
	return applied
}

func tryApplyMutation(n ast.Node, m *Mutant) bool {
	switch m.Operator {
	case "conditional_boundary", "negate_conditional", "math_operator":
		return applyBinaryMutation(n, m)
	case "boolean_substitution":
		return applyBoolMutation(n, m)
	case "return_value":
		return applyReturnMutation(n)
	}
	return false
}

func applyBinaryMutation(n ast.Node, m *Mutant) bool {
	expr, ok := n.(*ast.BinaryExpr)
	if !ok {
		return false
	}
	newOp := parseMutationOp(m.Description)
	if newOp == token.ILLEGAL {
		return false
	}
	expr.Op = newOp
	return true
}

func applyBoolMutation(n ast.Node, m *Mutant) bool {
	ident, ok := n.(*ast.Ident)
	if !ok || (ident.Name != "true" && ident.Name != "false") {
		return false
	}
	if strings.Contains(m.Description, "-> true") {
		ident.Name = "true"
	} else {
		ident.Name = "false"
	}
	return true
}

func applyReturnMutation(n ast.Node) bool {
	ret, ok := n.(*ast.ReturnStmt)
	if !ok {
		return false
	}
	for i := range ret.Results {
		ret.Results[i] = zeroValueExpr(ret.Results[i])
	}
	return true
}

func parseMutationOp(desc string) token.Token {
	parts := strings.Split(desc, " -> ")
	if len(parts) != 2 {
		return token.ILLEGAL
	}

	opMap := map[string]token.Token{
		">": token.GTR, ">=": token.GEQ,
		"<": token.LSS, "<=": token.LEQ,
		"==": token.EQL, "!=": token.NEQ,
		"+": token.ADD, "-": token.SUB,
		"*": token.MUL, "/": token.QUO,
	}

	if op, ok := opMap[parts[1]]; ok {
		return op
	}
	return token.ILLEGAL
}

func zeroValueExpr(expr ast.Expr) ast.Expr {
	return &ast.Ident{Name: "nil", NamePos: expr.Pos()}
}

func renderFile(fset *token.FileSet, f *ast.File) []byte {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, f); err != nil {
		return nil
	}
	return buf.Bytes()
}

func operatorName(from, to token.Token) string {
	switch {
	case isBoundary(from) || isBoundary(to):
		return "conditional_boundary"
	case isComparison(from) || isComparison(to):
		return "negate_conditional"
	case isMath(from) || isMath(to):
		return "math_operator"
	default:
		return "unknown"
	}
}

func isBoundary(t token.Token) bool {
	return t == token.GTR || t == token.GEQ || t == token.LSS || t == token.LEQ
}

func isComparison(t token.Token) bool {
	return t == token.EQL || t == token.NEQ
}

func isMath(t token.Token) bool {
	return t == token.ADD || t == token.SUB || t == token.MUL || t == token.QUO
}

func sampleMutants(mutants []Mutant, rate float64) []Mutant {
	if rate >= 100 {
		return mutants
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	n := int(float64(len(mutants)) * rate / 100)
	if n == 0 {
		n = 1
	}
	rng.Shuffle(len(mutants), func(i, j int) {
		mutants[i], mutants[j] = mutants[j], mutants[i]
	})
	return mutants[:n]
}

func buildSection(mutants []Mutant, killed int) report.Section {
	total := len(mutants)
	survived := total - killed

	score := 0.0
	if total > 0 {
		score = float64(killed) / float64(total) * 100
	}

	sev := report.SeverityPass
	if score < 60 {
		sev = report.SeverityFail
	} else if score < 80 {
		sev = report.SeverityWarn
	}

	var findings []report.Finding
	for _, m := range mutants {
		if !m.Killed {
			findings = append(findings, report.Finding{
				File:     m.File,
				Line:     m.Line,
				Message:  fmt.Sprintf("SURVIVED: %s (%s)", m.Description, m.Operator),
				Severity: report.SeverityFail,
			})
		}
	}

	summary := fmt.Sprintf("Score: %.1f%% (%d/%d killed, %d survived)",
		score, killed, total, survived)

	return report.Section{
		Name:     "Mutation Testing",
		Summary:  summary,
		Severity: sev,
		Findings: findings,
		Stats: map[string]any{
			"total":    total,
			"killed":   killed,
			"survived": survived,
			"score":    score,
		},
	}
}
