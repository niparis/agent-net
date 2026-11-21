package daemon

import (
	"bytes"
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/harnesses"
	"github.com/niparis/agent-net/internal/protocol"
)

// fakeTools puts an executable file for each name in a folder and makes it the
// only PATH, so the test sees exactly the tools it names. The login shell is
// not asked: a test must not read the owner's profile.
func fakeTools(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

// noLoginShell is the Daemon settings of a test that must not run a login shell.
func noLoginShell() *harnesses.Options {
	return &harnesses.Options{LoginShellPATH: func(context.Context) string { return "" }}
}

func TestClaudeSandboxMissingNamesToolsNotOnPATH(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"bwrap"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := claudeSandboxMissing("linux", dir); strings.Join(got, ",") != "socat" {
		t.Fatalf("missing on linux with bwrap only: %v, want [socat]", got)
	}
	if got := claudeSandboxMissing("darwin", dir); got != nil {
		t.Fatalf("missing on macOS: %v, want none (its sandbox ships with the OS)", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "socat"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := claudeSandboxMissing("linux", dir); strings.Join(got, ",") != "socat" {
		t.Fatalf("a socat without the execute bit: %v, want it missing", got)
	}
	if err := os.Chmod(filepath.Join(dir, "socat"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := claudeSandboxMissing("linux", dir); len(got) != 0 {
		t.Fatalf("both tools present: %v, want none", got)
	}
}

// A Claude worker starts even without socat, and its worker is marked as not
// sandboxed. With both tools it carries no mark.
func TestOSSandboxChoiceMarksClaudeWithoutItsTools(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the Claude sandbox tools are checked on Linux only")
	}
	d := &Daemon{cfg: Config{HarnessSearch: noLoginShell()}}

	fakeTools(t, "bwrap")
	got, err := d.osSandboxChoice("shop", "claude", "", false)
	if err != nil || got != protocol.OSSandboxNoneClaude([]string{"socat"}) {
		t.Fatalf("without socat: %q, %v", got, err)
	}

	fakeTools(t, "bwrap", "socat")
	got, err = d.osSandboxChoice("shop", "claude", "", false)
	if err != nil || got != "" {
		t.Fatalf("with both tools: %q, %v; want no mark", got, err)
	}
}

// The gap is logged once per daemon start, not at every start of a worker.
func TestClaudeSandboxGapIsLoggedOnce(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the Claude sandbox tools are checked on Linux only")
	}
	var buf bytes.Buffer
	d := &Daemon{cfg: Config{HarnessSearch: noLoginShell()}, log: log.New(&buf, "", 0)}
	fakeTools(t, "bwrap")
	for i := 0; i < 3; i++ {
		if _, err := d.osSandboxChoice("shop", "claude", "", false); err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(buf.String(), "needs socat"); n != 1 {
		t.Fatalf("logged %d times, want once:\n%s", n, buf.String())
	}
}

// agent_machine_get reads the machine's helpers: the missing sandbox tool is
// listed there, so the owner and the CTO see it.
func TestMachineHealthListsTheClaudeSandboxTools(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the Claude sandbox tools are reported on Linux only")
	}
	d := &Daemon{cfg: Config{HarnessSearch: noLoginShell(), StateDir: t.TempDir()}}
	fakeTools(t, "bwrap")
	h := d.machineHealth()
	if h.Helpers["socat"] != "not found" {
		t.Fatalf("socat helper: %q, want not found", h.Helpers["socat"])
	}
	if h.Helpers["bwrap"] == "not found" || h.Helpers["bwrap"] == "" {
		t.Fatalf("bwrap helper: %q, want its path", h.Helpers["bwrap"])
	}
}

// A Claude worker starts on a machine without socat, and the start says the
// worker runs with no OS sandbox. The start is not refused.
func TestClaudeWorkerStartsUnsandboxedWithoutSocat(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the Claude sandbox tools are checked on Linux only")
	}
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNodeWith(t, g, shortSet, func(c *Config) {
		c.Workers.OSSandboxOff = nil
	})
	c.register(t, newRepo(t, "shop"))

	// The start needs git only; bwrap is there, socat is not.
	bin := t.TempDir()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(git, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "bwrap"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	res, err := c.start(protocol.WorkerStartRequest{Project: "shop", Harness: "claude", Model: "haiku", Task: "say ok"})
	if err != nil {
		t.Fatalf("a Claude worker without socat was refused: %v", err)
	}
	if want := protocol.OSSandboxNoneClaude([]string{"socat"}); res.OSSandbox != want {
		t.Fatalf("os_sandbox %q, want %q", res.OSSandbox, want)
	}
}
