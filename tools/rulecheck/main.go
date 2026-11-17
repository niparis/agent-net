// Command rulecheck checks the project rules that reviewers used to check by
// reading. It runs in CI and in scripts/test-changed.sh:
//
//   - B4: no test calls time.Sleep with a constant duration over 2 seconds.
//   - B9: no em dash in a production Go string literal or in a Markdown file
//     under docs/.
//   - B12: no call that waits on the gateway or another process while a mutex
//     of internal/daemon or internal/gateway is held. Sites that already exist
//     are listed in lockcall.baseline. The list may only shrink.
//
// Usage. The checker is its own module, so build it from here and run the binary
// from the repository root, where the package patterns and the baseline path are
// relative:
//
//	go -C tools/rulecheck build -o "${TMPDIR:-/tmp}/rulecheck" .
//	"${TMPDIR:-/tmp}/rulecheck" [-docs DIR] [-lockbaseline FILE] [PACKAGE...]
//	"${TMPDIR:-/tmp}/rulecheck" -print-lock-baseline PACKAGE...
//
// It exits 1 when it finds a violation and 2 when it cannot run. Nothing in the
// agent-net binary imports the checker.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/packages"
)

// defaultLockBaseline is read relative to the working directory, the repository root.
const defaultLockBaseline = "tools/rulecheck/lockcall.baseline"

// finding is one violation, with a path relative to the working directory.
// key is set only for a lock-across-call site, and names it in the baseline.
type finding struct {
	file string
	line int
	col  int
	rule string
	msg  string
	key  string
}

func (f finding) String() string {
	return fmt.Sprintf("%s:%d:%d: [%s] %s", f.file, f.line, f.col, f.rule, f.msg)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("rulecheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	docs := flags.String("docs", "", "check the Markdown files under this directory for em dashes; empty skips the check")
	baselinePath := flags.String("lockbaseline", defaultLockBaseline, "the reviewed list of lock-across-call sites")
	printBaseline := flags.Bool("print-lock-baseline", false, "print the current lock-across-call sites in baseline format and exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	patterns := flags.Args()
	if *docs == "" && len(patterns) == 0 {
		fmt.Fprintln(stderr, "usage: rulecheck [-docs DIR] [-lockbaseline FILE] [PACKAGE...]")
		return 2
	}

	var findings []finding
	if *docs != "" {
		found, err := checkDocs(*docs)
		if err != nil {
			fmt.Fprintln(stderr, "rulecheck:", err)
			return 2
		}
		findings = append(findings, found...)
	}
	if len(patterns) > 0 {
		res, err := checkPackages(patterns)
		if err != nil {
			fmt.Fprintln(stderr, "rulecheck:", err)
			return 2
		}
		if *printBaseline {
			printLockBaseline(stdout, res.lockSites)
			return 0
		}
		baseline, err := loadBaseline(*baselinePath)
		if err != nil {
			fmt.Fprintln(stderr, "rulecheck:", err)
			return 2
		}
		findings = append(findings, res.findings...)
		findings = append(findings, ratchetLocks(res.lockSites, baseline, res.lockAnalysed, *baselinePath)...)
	}

	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.file != b.file {
			return a.file < b.file
		}
		if a.line != b.line {
			return a.line < b.line
		}
		return a.col < b.col
	})
	for _, f := range findings {
		fmt.Fprintln(stdout, f)
	}
	if len(findings) > 0 {
		fmt.Fprintf(stderr, "rulecheck: %d finding(s)\n", len(findings))
		return 1
	}
	fmt.Fprintln(stdout, "rulecheck: PASS")
	return 0
}

// analyzers are the checks that run on Go packages.
var analyzers = []*analysis.Analyzer{SleepAnalyzer, EmDashAnalyzer, LockAnalyzer}

// packageResult holds what the analyzers found in the loaded packages.
type packageResult struct {
	findings []finding
	// lockSites are the lock-across-call sites, each with its baseline key.
	lockSites []finding
	// lockAnalysed names the packages the lock check looked at. The baseline
	// entries of other packages are not judged.
	lockAnalysed map[string]bool
}

// checkPackages loads the packages with their tests and runs each analyzer on
// them. A package that does not load or type-check is an error, because an
// analyzer that sees broken types can miss a violation without saying so.
func checkPackages(patterns []string) (packageResult, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedTypesSizes |
			packages.NeedImports | packages.NeedDeps,
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return packageResult{}, err
	}
	var loadErrs []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			loadErrs = append(loadErrs, e.Error())
		}
	})
	if len(loadErrs) > 0 {
		return packageResult{}, fmt.Errorf("packages do not load:\n%s", strings.Join(loadErrs, "\n"))
	}

	res := packageResult{lockAnalysed: map[string]bool{}}
	// A package with tests appears twice: once as itself and once with its
	// test files. The production files then report the same finding twice.
	seen := map[string]bool{}
	for _, pkg := range pkgs {
		if inLockScope(pkg.PkgPath) {
			res.lockAnalysed[pkg.PkgPath] = true
		}
		for _, a := range analyzers {
			pass := &analysis.Pass{
				Analyzer:   a,
				Fset:       pkg.Fset,
				Files:      pkg.Syntax,
				Pkg:        pkg.Types,
				TypesInfo:  pkg.TypesInfo,
				TypesSizes: pkg.TypesSizes,
				ResultOf:   map[*analysis.Analyzer]any{},
				ReadFile:   os.ReadFile,
				Report: func(d analysis.Diagnostic) {
					p := pkg.Fset.Position(d.Pos)
					f := finding{file: relPath(p.Filename), line: p.Line, col: p.Column, rule: a.Name, msg: d.Message, key: d.Category}
					if seen[f.String()] {
						return
					}
					seen[f.String()] = true
					if a == LockAnalyzer {
						res.lockSites = append(res.lockSites, f)
						return
					}
					res.findings = append(res.findings, f)
				},
			}
			if _, err := a.Run(pass); err != nil {
				return packageResult{}, fmt.Errorf("%s: %w", a.Name, err)
			}
		}
	}
	return res, nil
}

// relPath shortens an absolute file name to a path under the working directory.
func relPath(name string) string {
	wd, err := os.Getwd()
	if err != nil {
		return name
	}
	rel, err := filepath.Rel(wd, name)
	if err != nil || strings.HasPrefix(rel, "..") {
		return name
	}
	return rel
}
