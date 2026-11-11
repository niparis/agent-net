package daemon

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/harnesses"
	"github.com/niparis/agent-net/internal/protocol"
)

// fakeHarnessScript writes an executable that answers --version, like a
// harness installed on the machine.
func fakeHarnessScript(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho \"2.1.0\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestHarnessBinaryBeforeDetectionFindsClaude covers a start right after the
// daemon starts: the inventory loop has not finished yet, so harnessBinary must
// find the binary by itself.
func TestHarnessBinaryBeforeDetectionFindsClaude(t *testing.T) {
	dir := t.TempDir()
	bin := fakeHarnessScript(t, dir, "claude")
	d := &Daemon{cfg: Config{HarnessSearch: &harnesses.Options{
		PATH: dir, Dirs: []string{}, LoginShellPATH: func(context.Context) string { return "" },
	}}}
	if list, _ := d.inventory(); len(list) != 0 {
		t.Fatalf("detection already ran: %+v", list)
	}
	got, err := d.harnessBinary("claude")
	if err != nil {
		t.Fatalf("harnessBinary before detection: %v", err)
	}
	if got != bin {
		t.Fatalf("harnessBinary = %q, want %q", got, bin)
	}
}

// TestHarnessDetectionKeepsClaudeWhenProbeMisses covers a detection run whose
// claude probe timed out: the previous entry must stay until a probe shows the
// binary is really gone, and the failure must be logged.
func TestHarnessDetectionKeepsClaudeWhenProbeMisses(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	bin := fakeHarnessScript(t, dir, "claude")
	logs := &logBuf{}
	var runs atomic.Int32
	detect := func(context.Context) []protocol.HarnessInfo {
		if runs.Add(1) == 1 {
			return []protocol.HarnessInfo{{Name: "claude", Path: bin, Version: "2.1.0"}}
		}
		// The claude probe failed or timed out, so this run does not report it.
		return []protocol.HarnessInfo{{Name: "opencode", Path: bin, Version: "fake"}}
	}
	logger := log.New(logs, "", 0)
	d := &Daemon{cfg: Config{DetectHarnesses: detect, Logger: logger}, log: logger}
	ctx := context.Background()
	d.refreshHarnessInventory(ctx)
	d.refreshHarnessInventory(ctx)
	list, _ := d.inventory()
	found := false
	for _, h := range list {
		if h.Name == "claude" && h.Path == bin {
			found = true
		}
	}
	if !found {
		t.Fatalf("the previous claude entry was not kept: %+v", list)
	}
	if len(logs.lines("claude")) == 0 {
		t.Fatalf("the missed probe was not logged:\n%s", logs.b.String())
	}
}

// TestKeepMissingHarnessesUsesThePreviousCopy covers a probe that answers with
// an entry it could not fill in: the earlier path is used again.
func TestKeepMissingHarnessesUsesThePreviousCopy(t *testing.T) {
	dir := t.TempDir()
	bin := fakeHarnessScript(t, dir, "claude")
	prev := []protocol.HarnessInfo{{Name: "claude", Path: bin, Version: "2.1.0"}}
	next := []protocol.HarnessInfo{{Name: "claude"}}
	got := keepMissingHarnesses(prev, next, nil)
	if len(got) != 1 || got[0].Path != bin || got[0].Version != "2.1.0" {
		t.Fatalf("kept %+v, want the previous copy at %s", got, bin)
	}
}

// TestHarnessBinaryMissingGivesClearMessage keeps the existing message when no
// copy of the harness can be found or run.
func TestHarnessBinaryMissingGivesClearMessage(t *testing.T) {
	d := &Daemon{cfg: Config{HarnessSearch: &harnesses.Options{
		PATH: t.TempDir(), Dirs: []string{}, LoginShellPATH: func(context.Context) string { return "" },
	}}}
	_, err := d.harnessBinary("claude")
	if err == nil || !strings.Contains(err.Error(), "claude is not installed on this machine") {
		t.Fatalf("missing harness error = %v", err)
	}
}

// TestHarnessLoopListsModelsOnce verifies the loop detects the harness once and
// never runs a periodic model scan again.
func TestHarnessLoopListsModelsOnce(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	var runs atomic.Int32
	detect := func(context.Context) []protocol.HarnessInfo {
		runs.Add(1)
		return []protocol.HarnessInfo{{Name: "opencode", Path: "/fake/opencode", Models: []string{"provider/model"}}}
	}
	logger := log.New(&logBuf{}, "", 0)
	d := &Daemon{cfg: Config{DetectHarnesses: detect, Logger: logger}, log: logger}
	d.harnessLoop(context.Background())
	time.Sleep(100 * time.Millisecond)
	if got := runs.Load(); got != 1 {
		t.Fatalf("detection ran %d times, want exactly 1 (no periodic model scan)", got)
	}
	list, _ := d.inventory()
	if len(list) != 1 || len(list[0].Models) != 1 {
		t.Fatalf("inventory after the one detection: %+v", list)
	}
}

func TestHarnessDetectionKeepsPreviousModels(t *testing.T) {
	for _, name := range []string{"opencode", "pi", "agy", "claude"} {
		t.Run(name, func(t *testing.T) {
			prev := []protocol.HarnessInfo{{Name: name, Path: "/old", Models: []string{"provider/model"}}}
			next := []protocol.HarnessInfo{{Name: name, Path: "/new"}}
			got := keepMissingHarnesses(prev, next, nil)
			want := 1
			if name == "claude" {
				want = 0
			}
			if len(got) != 1 || got[0].Path != "/new" || len(got[0].Models) != want {
				t.Fatalf("got %+v, want new path and %d models", got, want)
			}
		})
	}
}
