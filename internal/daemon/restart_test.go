package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// restartHooks replaces the exit and exec points of RestartSelf so a test never
// ends the test process. A stop that blocks is released at the end, and the
// restart goroutine is waited for before the hooks are put back.
type restartHooks struct {
	mu     sync.Mutex
	exits  []int
	execs  [][]string
	logs   []string
	stopCh chan struct{}
	done   chan struct{}
}

func newRestartHooks(t *testing.T) *restartHooks {
	t.Helper()
	h := &restartHooks{stopCh: make(chan struct{})}
	oldExit, oldExec, oldTimeout := restartSelfExit, restartSelfExec, restartStopTimeout
	restartStopTimeout = scaled(150 * time.Millisecond)
	restartSelfExit = func(code int) {
		h.mu.Lock()
		h.exits = append(h.exits, code)
		h.mu.Unlock()
	}
	restartSelfExec = func(exe string, args, env []string) error {
		h.mu.Lock()
		h.execs = append(h.execs, args)
		h.mu.Unlock()
		return nil
	}
	t.Cleanup(func() {
		close(h.stopCh)
		if h.done != nil {
			select {
			case <-h.done:
			case <-time.After(scaled(10 * time.Second)):
			}
		}
		restartSelfExit, restartSelfExec, restartStopTimeout = oldExit, oldExec, oldTimeout
	})
	return h
}

// run starts a restart whose stop blocks until the end of the test, and returns
// a channel that is closed when RestartSelf itself returned.
func (h *restartHooks) run(exe string) <-chan struct{} {
	h.done = make(chan struct{})
	go func() {
		RestartSelf(exe, func() { <-h.stopCh }, h.logf)
		close(h.done)
	}()
	return h.done
}

func (h *restartHooks) logf(format string, args ...any) {
	h.mu.Lock()
	h.logs = append(h.logs, strings.TrimSpace(format))
	h.mu.Unlock()
}

func (h *restartHooks) exited() []int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]int(nil), h.exits...)
}

func (h *restartHooks) executed() [][]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([][]string(nil), h.execs...)
}

func (h *restartHooks) logged() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(h.logs, "\n")
}

func (h *restartHooks) wait(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(scaled(2 * time.Second)):
		t.Fatalf("RestartSelf did not reach %s while stop blocked; log:\n%s", what, h.logged())
	}
}

// The live hang: stop waits on something that never returns, so launchd never
// sees this process exit and KeepAlive never starts the new binary. The restart
// must reach the exit point anyway.
func TestRestartSelfExitsWhenStopBlocks(t *testing.T) {
	t.Setenv("XPC_SERVICE_NAME", "com.agent-net.daemon")
	t.Setenv("AGENT_NET_NO_SERVICE", "")
	t.Setenv("INVOCATION_ID", "")
	h := newRestartHooks(t)

	h.wait(t, h.run("/bin/true"), "the exit point")
	if got := h.exited(); len(got) != 1 || got[0] != 0 {
		t.Fatalf("exit calls = %v, want [0]", got)
	}
	if log := h.logged(); !strings.Contains(log, "did not return") || !strings.Contains(log, "goroutine") {
		t.Fatalf("the daemon log has no stop timeout and goroutine dump:\n%s", log)
	}
}

// The exec path (no service manager) gets the same bound.
func TestRestartSelfExecsWhenStopBlocks(t *testing.T) {
	t.Setenv("XPC_SERVICE_NAME", "")
	t.Setenv("INVOCATION_ID", "")
	t.Setenv("AGENT_NET_NO_SERVICE", "1")
	h := newRestartHooks(t)

	h.wait(t, h.run("/bin/true"), "the exec point")
	if got := h.executed(); len(got) != 1 || len(got[0]) == 0 || got[0][0] != "/bin/true" {
		t.Fatalf("exec calls = %v, want [/bin/true]", got)
	}
	if log := h.logged(); !strings.Contains(log, "did not return") || !strings.Contains(log, "goroutine") {
		t.Fatalf("the daemon log has no stop timeout and goroutine dump:\n%s", log)
	}
}

// A stop that returns in time keeps the clean path: no timeout line.
func TestRestartSelfCleanStopExits(t *testing.T) {
	t.Setenv("XPC_SERVICE_NAME", "com.agent-net.daemon")
	t.Setenv("AGENT_NET_NO_SERVICE", "")
	t.Setenv("INVOCATION_ID", "")
	h := newRestartHooks(t)
	h.done = make(chan struct{})
	RestartSelf("/bin/true", func() {}, h.logf)
	close(h.done)

	if got := h.exited(); len(got) != 1 || got[0] != 0 {
		t.Fatalf("exit calls = %v, want [0]", got)
	}
	if log := h.logged(); log != "" {
		t.Fatalf("a clean stop logged: %s", log)
	}
}

// systemd owns the process stop, and the same bound must apply before the
// restart command is asked for.
func TestRestartSelfSystemdBoundsStop(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "restarted")
	script := filepath.Join(dir, "systemctl")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n: > "+marker+"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("INVOCATION_ID", "test")
	t.Setenv("XPC_SERVICE_NAME", "")
	t.Setenv("AGENT_NET_NO_SERVICE", "")
	h := newRestartHooks(t)

	h.wait(t, h.run("/bin/true"), "systemctl")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("systemctl was not run: %v", err)
	}
	if got := h.exited(); len(got) != 0 {
		t.Fatalf("the systemd path exited the process: %v", got)
	}
}
