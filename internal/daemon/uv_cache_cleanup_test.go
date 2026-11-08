package daemon

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeUV puts an executable `uv` in a temp folder and returns that folder.
func fakeUV(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "uv")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func countLines(text, part string) int {
	n := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, part) {
			n++
		}
	}
	return n
}

func TestUVCachePruneSuccessLogsBytes(t *testing.T) {
	isolateDaemonCaches(t)
	argsFile := filepath.Join(t.TempDir(), "args")
	lockFile := filepath.Join(t.TempDir(), "lock-timeout")
	binDir := fakeUV(t, `printf '%s\n' "$@" > "$UV_ARGS_FILE"
printf '%s' "$UV_LOCK_TIMEOUT" > "$UV_LOCK_FILE"
echo "Pruning cache at: /tmp/cache"
echo "Removed 2 files (1.5MiB)"`)
	t.Setenv("PATH", binDir)
	t.Setenv("UV_ARGS_FILE", argsFile)
	t.Setenv("UV_LOCK_FILE", lockFile)
	t.Setenv("UV_CACHE_DIR", t.TempDir())

	var logs bytes.Buffer
	d := &Daemon{log: log.New(&logs, "", 0)}
	d.pruneUVCache(context.Background(), time.Now())

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(raw); got != "cache\nprune\n" {
		t.Fatalf("uv args = %q, want cache prune", got)
	}
	lockTimeout, err := os.ReadFile(lockFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(lockTimeout) != "10" {
		t.Fatalf("UV_LOCK_TIMEOUT = %q, want 10", lockTimeout)
	}
	if !strings.Contains(logs.String(), "uv cache prune: Removed 2 files (1.5MiB)") {
		t.Fatalf("log = %q, want uv's summary line as given", logs.String())
	}
}

func TestUVCachePruneNoUnusedEntries(t *testing.T) {
	isolateDaemonCaches(t)
	binDir := fakeUV(t, `echo "Pruning cache at: /tmp/cache"
echo "No unused entries found"`)
	t.Setenv("PATH", binDir)
	t.Setenv("UV_CACHE_DIR", t.TempDir())

	var logs bytes.Buffer
	d := &Daemon{log: log.New(&logs, "", 0)}
	d.pruneUVCache(context.Background(), time.Now())
	if !strings.Contains(logs.String(), "uv cache prune: No unused entries found") {
		t.Fatalf("log = %q, want uv's summary line as given", logs.String())
	}
}

func TestUVCachePruneLockTimeoutLoggedOnceADay(t *testing.T) {
	isolateDaemonCaches(t)
	binDir := fakeUV(t, `echo "Cache is currently in-use, waiting for other uv processes to finish (use --force to override)" >&2
echo "error: Timeout (10s) when waiting for lock on /tmp/cache at /tmp/cache/.lock, is another uv process running?" >&2
exit 2`)
	t.Setenv("PATH", binDir)
	t.Setenv("UV_CACHE_DIR", t.TempDir())

	var logs bytes.Buffer
	d := &Daemon{log: log.New(&logs, "", 0)}
	now := time.Now()
	d.pruneUVCache(context.Background(), now)
	d.pruneUVCache(context.Background(), now.Add(time.Hour))
	if got := countLines(logs.String(), "uv cache prune: lock timeout"); got != 1 {
		t.Fatalf("lock timeout lines in one day = %d, want 1: %q", got, logs.String())
	}
	d.pruneUVCache(context.Background(), now.Add(25*time.Hour))
	if got := countLines(logs.String(), "uv cache prune: lock timeout"); got != 2 {
		t.Fatalf("lock timeout lines after a day = %d, want 2: %q", got, logs.String())
	}
	if strings.Contains(logs.String(), "bytes freed") {
		t.Fatalf("a timeout must not log a success: %q", logs.String())
	}
}

func TestUVCachePruneSkipsMissingUv(t *testing.T) {
	isolateDaemonCaches(t)
	t.Setenv("PATH", t.TempDir())

	var logs bytes.Buffer
	d := &Daemon{log: log.New(&logs, "", 0)}
	d.pruneUVCache(context.Background(), time.Now())
	if logs.Len() != 0 {
		t.Fatalf("missing uv logged %q", logs.String())
	}
}
