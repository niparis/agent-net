package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// fakeHarnessProc starts a process that looks like the harness binary: a shell
// script named after the harness, so its argument vector is
// "sh <dir>/<harness> <args...>". dir is also its working directory.
func fakeHarnessProc(t *testing.T, harness, dir string, args ...string) (pid int, token string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), harness)
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nwhile :; do sleep 1; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go cmd.Wait()
	t.Cleanup(func() { syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	eventually(t, "script is running", func() bool {
		a := processArgs(cmd.Process.Pid)
		return len(a) >= 2 && filepath.Base(a[1]) == harness
	})
	tok, _ := procToken(cmd.Process.Pid)
	return cmd.Process.Pid, tok
}

// A live session whose registered process is missing (Codex: threads run on the
// shared app server and have no PID) or has no token must still be stopped when
// its terminal process can be found by a safe identity.
func TestSessionStopFindsHarnessProcess(t *testing.T) {
	cases := []struct {
		harness string
		args    func(id string) []string
		pid     bool // the session is registered with its PID, but no token
	}{
		{"codex", func(id string) []string { return []string{"resume", id} }, false},
		{"claude", func(id string) []string { return []string{"--resume", id} }, true},
		{"pi", func(id string) []string { return []string{"--session", id} }, true},
		{"opencode", func(id string) []string { return []string{"--session", id} }, true},
	}
	for _, tc := range cases {
		t.Run(tc.harness, func(t *testing.T) {
			dir := t.TempDir()
			native := "thread-" + tc.harness + "-0123"
			pid, _ := fakeHarnessProc(t, tc.harness, dir, tc.args(native)...)
			reg := 0
			if tc.pid {
				reg = pid
			}
			g := startGateway(t, t.TempDir(), "")
			n, _, id := enterNode(t, g, tc.harness, protocol.SessionInfo{NativeID: native, Name: "shop", Cwd: dir, PID: reg, Status: "busy", Mode: protocol.ModeForeground}, nil, nil)
			ls := n.d.sessionByID(id)
			n.d.mu.Lock()
			ls.stopToken = ""
			n.d.mu.Unlock()
			res, err := n.d.sessionStopLocal(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(res.Note, "Stopped ") {
				t.Fatal(res.Note)
			}
			eventually(t, "harness ended", func() bool { _, live := procToken(pid); return !live })
		})
	}
}

// A process of another program, or of the same harness for another
// conversation, is never signalled; the message says what to do.
func TestSessionStopFindRefusesOthers(t *testing.T) {
	dir := t.TempDir()
	other, _ := fakeHarnessProc(t, "codex", dir, "resume", "some-other-thread")
	reused, _ := startSleeper(t)
	for name, reg := range map[string]int{"no pid": 0, "reused pid": reused} {
		t.Run(name, func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			n, _, id := enterNode(t, g, "codex", protocol.SessionInfo{NativeID: "thread-mine", Cwd: filepath.Join(dir, "elsewhere"), PID: reg, Mode: protocol.ModeForeground}, nil, nil)
			ls := n.d.sessionByID(id)
			n.d.mu.Lock()
			ls.stopToken = "an earlier run"
			n.d.mu.Unlock()
			_, err := n.d.sessionStopLocal(context.Background(), id)
			if err == nil || !strings.Contains(err.Error(), "Close its terminal") {
				t.Fatalf("want a plain instruction, got %v", err)
			}
			for _, p := range []int{other, reused} {
				if _, live := procToken(p); !live {
					t.Fatalf("signalled pid %d", p)
				}
			}
		})
	}
}

// Without the native ID in its arguments, the session is found by folder, and
// only when that is unambiguous.
func TestSessionStopFindByFolderNeedsOneCandidate(t *testing.T) {
	dir := t.TempDir()
	a, _ := fakeHarnessProc(t, "codex", dir)
	b, _ := fakeHarnessProc(t, "codex", dir)
	g := startGateway(t, t.TempDir(), "")
	n, _, id := enterNode(t, g, "codex", protocol.SessionInfo{NativeID: "thread-1", Cwd: dir, Mode: protocol.ModeForeground}, nil, nil)
	if _, err := n.d.sessionStopLocal(context.Background(), id); err == nil || !strings.Contains(err.Error(), "Close its terminal") {
		t.Fatalf("two candidates: %v", err)
	}
	for _, p := range []int{a, b} {
		if _, live := procToken(p); !live {
			t.Fatalf("signalled pid %d with two candidates", p)
		}
	}
	syscall.Kill(-b, syscall.SIGKILL)
	eventually(t, "second ended", func() bool { _, live := procToken(b); return !live })
	if _, err := n.d.sessionStopLocal(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	eventually(t, "first ended", func() bool { _, live := procToken(a); return !live })
}
