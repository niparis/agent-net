package main

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// EmDashAnalyzer reports an em dash in a Go string literal of production code.
// A literal's value is what a user reads, so an escape such as \u2014 is reported
// too. Test files are skipped: an existing test names the character to check
// that the text a user reads has none, and that check runs in the test itself.
// Comments are not checked: B9 covers docs, messages and screens.
var EmDashAnalyzer = &analysis.Analyzer{
	Name: "emdash",
	Doc:  "reports an em dash in a Go string literal of production code (B9)",
	Run:  runEmDash,
}

func runEmDash(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		if strings.HasSuffix(pass.Fset.Position(file.Pos()).Filename, "_test.go") {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil || !strings.Contains(value, emDash) {
				return true
			}
			pass.Reportf(lit.Pos(), "em dash in a string literal (B9): use a plain hyphen")
			return true
		})
	}
	return nil, nil
}
