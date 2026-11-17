package main

import (
	"go/ast"
	"go/constant"
	"go/types"
	"strings"
	"time"

	"golang.org/x/tools/go/analysis"
)

// maxTestSleep is the longest constant sleep a test may make (B4). A test that
// needs more time polls for the state it waits for, with a timeout.
const maxTestSleep = 2 * time.Second

// SleepAnalyzer reports time.Sleep with a constant duration over maxTestSleep in
// a _test.go file. A sleep that is not constant, such as one from a variable,
// is not reported.
var SleepAnalyzer = &analysis.Analyzer{
	Name: "sleep",
	Doc:  "reports a constant time.Sleep over 2s in a _test.go file (B4)",
	Run:  runSleep,
}

func runSleep(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		if !strings.HasSuffix(pass.Fset.Position(file.Pos()).Filename, "_test.go") {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 || !isTimeSleep(pass.TypesInfo, call) {
				return true
			}
			tv, ok := pass.TypesInfo.Types[call.Args[0]]
			if !ok || tv.Value == nil {
				return true
			}
			nanos, exact := constant.Int64Val(constant.ToInt(tv.Value))
			if !exact || time.Duration(nanos) <= maxTestSleep {
				return true
			}
			pass.Reportf(call.Pos(), "time.Sleep(%s) in a test (B4): poll for the state with a timeout; no test sleeps more than 2s", time.Duration(nanos))
			return true
		})
	}
	return nil, nil
}

func isTimeSleep(info *types.Info, call *ast.CallExpr) bool {
	fn := calledFunc(info, call)
	return fn != nil && fn.Pkg() != nil && fn.Pkg().Path() == "time" && fn.Name() == "Sleep"
}
