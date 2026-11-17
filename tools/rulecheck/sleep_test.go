package main

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestSleepAnalyzer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), SleepAnalyzer, "sleepcase")
}
