package main

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestLockAnalyzer runs the check on a package whose path is in scope. The
// fixture's want comments name every site that must be reported, and any other
// finding fails the test.
func TestLockAnalyzer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), LockAnalyzer, "internal/daemon")
}

// TestLockAnalyzerSkipsOtherPackages checks that the same shape outside
// internal/daemon and internal/gateway is not reported.
func TestLockAnalyzerSkipsOtherPackages(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), LockAnalyzer, "other")
}
