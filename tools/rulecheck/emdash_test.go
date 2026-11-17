package main

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestEmDashAnalyzer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), EmDashAnalyzer, "emdashcase")
}
