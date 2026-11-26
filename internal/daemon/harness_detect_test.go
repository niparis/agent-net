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

// TestHarnessCheckFindsAHarnessInstalledAfterStart drops a harness binary in a
// folder on the search path after detection ran, and expects the periodic check
// to list it. While nothing changes the check only stats files.
func TestHarnessCheckFindsAHarnessInstalledAfterStart(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	search := &harnesses.Options{PATH: dir, Dirs: []string{}, LoginShellPATH: func(context.Context) string { return "" }}
	var runs atomic.Int32
	detect := func(ctx context.Context) []protocol.HarnessInfo {
		runs.Add(1)
		if p := harnesses.Executable(ctx, "codex", *search); p != "" {
			return []protocol.HarnessInfo{{Name: "codex", Path: p, Version: "fake"}}
		}
		return []protocol.HarnessInfo{}
	}
	logger := log.New(&logBuf{}, "", 0)
	d := &Daemon{cfg: Config{DetectHarnesses: detect, HarnessSearch: search, HarnessCheckInterval: 5 * time.Millisecond, Logger: logger}, log: logger}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); d.harnessLoop(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(scaled(5 * time.Second)):
			t.Fatal("the harness loop did not stop")
		}
	}()
	eventually(t, "the first detection", func() bool {
		list, _ := d.inventory()
		return list != nil
	})
	if d.harnessCheck(ctx) {
		t.Fatal("the check refreshed while no harness was installed")
	}
	if got := runs.Load(); got != 1 {
		t.Fatalf("detection ran %d times while nothing changed, want 1", got)
	}
	fakeHarnessScript(t, dir, "codex")
	eventually(t, "codex in the inventory", func() bool {
		list, _ := d.inventory()
		for _, h := range list {
			if h.Name == "codex" && h.Path != "" {
				return true
			}
		}
		return false
	})
	if got := runs.Load(); got != 2 {
		t.Fatalf("detection ran %d times, want 2 (the check and the new binary)", got)
	}
}

// TestHarnessBinaryDetectsAHarnessInstalledAfterStart covers a start that asks
// for a harness the inventory does not list: the daemon looks it up, detects
// again, and later starts find it in the inventory.
func TestHarnessBinaryDetectsAHarnessInstalledAfterStart(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	search := &harnesses.Options{PATH: dir, Dirs: []string{}, LoginShellPATH: func(context.Context) string { return "" }}
	detect := func(ctx context.Context) []protocol.HarnessInfo {
		if p := harnesses.Executable(ctx, "codex", *search); p != "" {
			return []protocol.HarnessInfo{{Name: "codex", Path: p, Version: "fake"}}
		}
		return []protocol.HarnessInfo{}
	}
	logger := log.New(&logBuf{}, "", 0)
	d := &Daemon{cfg: Config{DetectHarnesses: detect, HarnessSearch: search, Logger: logger}, log: logger}
	d.refreshHarnessInventory(context.Background())
	bin := fakeHarnessScript(t, dir, "codex")
	got, err := d.harnessBinary("codex")
	if err != nil {
		t.Fatalf("harnessBinary: %v", err)
	}
	if got != bin {
		t.Fatalf("harnessBinary = %q, want %q", got, bin)
	}
	list, _ := d.inventory()
	for _, h := range list {
		if h.Name == "codex" && h.Path == bin {
			return
		}
	}
	t.Fatalf("the start did not refresh the inventory: %+v", list)
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
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); d.harnessLoop(ctx) }()
	eventually(t, "the first detection", func() bool {
		list, _ := d.inventory()
		return list != nil
	})
	cancel()
	select {
	case <-done:
	case <-time.After(scaled(5 * time.Second)):
		t.Fatal("the harness loop did not stop")
	}
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
