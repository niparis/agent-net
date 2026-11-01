package main

import (
	"context"
	"strings"
	"testing"
)

func TestBareAgentNetOpensTUIOnTerminal(t *testing.T) {
	oldIn, oldOut, oldTUI := stdinIsTerminal, stdoutIsTerminal, runTUI
	t.Cleanup(func() { stdinIsTerminal, stdoutIsTerminal, runTUI = oldIn, oldOut, oldTUI })
	stdinIsTerminal = func() bool { return true }
	stdoutIsTerminal = func() bool { return true }
	called := false
	runTUI = func(context.Context, []string) error { called = true; return nil }
	var stderr strings.Builder
	if code := run(context.Background(), nil, &stderr); code != 0 {
		t.Fatalf("exit code %d, stderr %q", code, stderr.String())
	}
	if !called {
		t.Fatal("bare agent-net did not open the TUI on a terminal")
	}
	if stderr.Len() != 0 {
		t.Fatalf("bare agent-net printed to stderr: %q", stderr.String())
	}
}

func TestBareAgentNetWithoutTerminalPrintsUsage(t *testing.T) {
	oldIn, oldOut, oldTUI := stdinIsTerminal, stdoutIsTerminal, runTUI
	t.Cleanup(func() { stdinIsTerminal, stdoutIsTerminal, runTUI = oldIn, oldOut, oldTUI })
	stdinIsTerminal = func() bool { return false }
	stdoutIsTerminal = func() bool { return false }
	runTUI = func(context.Context, []string) error {
		t.Fatal("bare agent-net opened the TUI without a terminal")
		return nil
	}
	var stderr strings.Builder
	if code := run(context.Background(), nil, &stderr); code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "usage: agent-net") {
		t.Fatalf("usage not printed: %q", stderr.String())
	}
}

func TestBareAgentNetNeedsBothTerminals(t *testing.T) {
	oldIn, oldOut, oldTUI := stdinIsTerminal, stdoutIsTerminal, runTUI
	t.Cleanup(func() { stdinIsTerminal, stdoutIsTerminal, runTUI = oldIn, oldOut, oldTUI })
	stdinIsTerminal = func() bool { return true }
	stdoutIsTerminal = func() bool { return false }
	runTUI = func(context.Context, []string) error {
		t.Fatal("bare agent-net opened the TUI with a piped stdout")
		return nil
	}
	var stderr strings.Builder
	if code := run(context.Background(), nil, &stderr); code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
}
