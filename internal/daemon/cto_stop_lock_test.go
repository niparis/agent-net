package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hungClaudeStop is a live Claude CTO job on a daemon without a socket. Its
// `claude stop` does not answer until the test releases it, as a wedged Claude
// service does.
type hungClaudeStop struct {
	d       *Daemon
	child   *exec.Cmd
	reaped  chan struct{}
	entered string
	release string
	stops   string
}

func newHungClaudeStop(t *testing.T) *hungClaudeStop {
	t.Helper()
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	d := newCTOStateTestDaemon(t, func(*Daemon) {})
	latest := d.cfg.testGatewayCall
	d.cfg.testGatewayCall = func(ctx context.Context, typ string, params any) (json.RawMessage, bool, error) {
		if res, handled, err := latest(ctx, typ, params); handled {
			return res, true, err
		}
		return nil, true, errors.New("no gateway in this test")
	}
	base := t.TempDir()
	d.cfg.StateDir = filepath.Join(base, "state")
	d.cfg.ClaudeSessionsDir = filepath.Join(base, "claude", "sessions")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	d.ctoCtx = ctx
	f := &hungClaudeStop{
		d:       d,
		reaped:  make(chan struct{}),
		entered: filepath.Join(base, "entered"),
		release: filepath.Join(base, "release"),
		stops:   filepath.Join(base, "stops.log"),
	}
	bin := filepath.Join(base, "bin", "claude")
	if err := os.MkdirAll(filepath.Dir(bin), 0o700); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = stop ]; then\n  echo \"$2\" >> %q\n  touch %q\n  while [ ! -f %q ]; do sleep 0.05; done\nfi\n", f.stops, f.entered, f.release)
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	d.cfg.HarnessPaths = map[string]string{"claude": bin}

	f.child = exec.Command("sleep", "60")
	if err := f.child.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = f.child.Wait(); close(f.reaped) }()
	// Cleanups run last first: the hung stop is released before anything else
	// is torn down.
	t.Cleanup(func() {
		_ = f.child.Process.Kill()
		<-f.reaped
	})
	t.Cleanup(func() { _ = os.WriteFile(f.release, nil, 0o600) })
	token, alive := procToken(f.child.Process.Pid)
	if !alive {
		t.Fatal("the fake Claude job is not alive")
	}
	// The session file makes the job findable by an adoption, as Claude Code's
	// own file does.
	ctoDir := filepath.Join(d.cfg.StateDir, "cto", "claude")
	if err := os.MkdirAll(ctoDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(d.cfg.ClaudeSessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sf, _ := json.Marshal(map[string]any{"pid": f.child.Process.Pid, "sessionId": "ses_hung", "jobId": "job_hung", "kind": "bg", "name": "agent-net-cto", "cwd": ctoDir})
	if err := os.WriteFile(filepath.Join(d.cfg.ClaudeSessionsDir, fmt.Sprintf("%d.json", f.child.Process.Pid)), sf, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.setCTOConversation("claude", "ses_hung"); err != nil {
		t.Fatal(err)
	}
	d.cto.mu.Lock()
	d.claudeCTO.ref, d.claudeCTO.model, d.claudeCTO.token = "job_hung", "fixture-model", token
	d.claudeCTO.setSession("ses_hung")
	d.claudeCTO.root.Store(&claudeCTORoot{pid: f.child.Process.Pid, token: token})
	d.cto.mu.Unlock()
	return f
}

// waitStopEntered waits until a stop is inside the hung `claude stop`.
func (f *hungClaudeStop) waitStopEntered(t *testing.T) {
	t.Helper()
	eventually(t, "a stop inside claude stop", func() bool {
		_, err := os.Stat(f.entered)
		return err == nil
	})
}

// crashStopHangs ends the job's process, so the crash monitor stops the CTO,
// and waits until that stop hangs in `claude stop`.
func (f *hungClaudeStop) crashStopHangs(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	d := f.d
	d.cto.mu.Lock()
	root := d.claudeCTO.root.Load()
	d.cto.mu.Unlock()
	go d.monitorClaudeCTO(ctx, "job_hung", root.pid, root.token)
	_ = f.child.Process.Kill()
	<-f.reaped
	f.waitStopEntered(t)
}

// stopCalls is the job ids `claude stop` was called with.
func (f *hungClaudeStop) stopCalls() []string {
	raw, _ := os.ReadFile(f.stops)
	return strings.Fields(string(raw))
}

// assertStopped checks that the run is gone from memory and its process is
// gone, and that `claude stop` ran once for it.
func (f *hungClaudeStop) assertStopped(t *testing.T) {
	t.Helper()
	d := f.d
	d.cto.mu.Lock()
	ref, session, root := d.claudeCTO.ref, d.claudeCTO.sessionID(), d.claudeCTO.root.Load()
	d.cto.mu.Unlock()
	if ref != "" || session != "" || root != nil {
		t.Fatalf("after the stop: ref %q session %q root %v, want none", ref, session, root)
	}
	select {
	case <-f.reaped:
	case <-time.After(5 * time.Second):
		t.Fatal("the stopped job's process is still alive")
	}
	if calls := f.stopCalls(); len(calls) != 1 || calls[0] != "job_hung" {
		t.Fatalf("claude stop calls = %v, want [job_hung]", calls)
	}
}

func TestCTOOwnerStopDoesNotWaitForAHungClaudeStop(t *testing.T) {
	f := newHungClaudeStop(t)
	f.crashStopHangs(t)
	waitWithin(t, "owner stop during a hung claude stop", func() {
		if _, err := f.d.stopCTOSession(context.Background()); err != nil {
			t.Error(err)
		}
	})
}

func TestCTOResetDoesNotWaitForAHungClaudeStop(t *testing.T) {
	f := newHungClaudeStop(t)
	f.crashStopHangs(t)
	waitWithin(t, "new conversation during a hung claude stop", func() {
		if _, err := (claudeCTOHarness{}).newConversation(context.Background(), f.d); err != nil {
			t.Error(err)
		}
	})
}

func TestCTOHandoverDoesNotWaitForAHungClaudeStop(t *testing.T) {
	f := newHungClaudeStop(t)
	f.crashStopHangs(t)
	waitWithin(t, "handover during a hung claude stop", func() {
		if err := f.d.ctoHandover(context.Background(), "claude", "opencode", true, false); err != nil {
			t.Error(err)
		}
	})
}

// While the owner stop waits for `claude stop`, the job is still alive and its
// session file is still there. An adoption (a CTO MCP child saying hello) must
// not bind that job again, and the stop must end the job.
func TestCTOStopDuringSlowStopLeavesNoHalfStoppedRun(t *testing.T) {
	f := newHungClaudeStop(t)
	d := f.d
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_, _ = d.stopCTOSession(context.Background())
	}()
	f.waitStopEntered(t)
	waitWithin(t, "adoption during a slow owner stop", func() {
		if d.adoptLiveClaudeCTO() {
			t.Error("an adoption bound the job that the owner stop is ending")
		}
	})
	waitWithin(t, "presence during a slow owner stop", func() {
		if d.ctoRunning() {
			t.Error("presence says the CTO runs while the owner stops it")
		}
	})
	if err := os.WriteFile(f.release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	<-stopped
	f.assertStopped(t)
}

// A new conversation ends the run with the same stop. During it the job may
// not be adopted, and after it the stored conversation is empty.
func TestCTOStopDuringSlowResetLeavesNoHalfStoppedRun(t *testing.T) {
	f := newHungClaudeStop(t)
	d := f.d
	reset := make(chan error, 1)
	go func() {
		_, err := (claudeCTOHarness{}).newConversation(context.Background(), d)
		reset <- err
	}()
	f.waitStopEntered(t)
	waitWithin(t, "adoption during a slow reset", func() {
		if d.adoptLiveClaudeCTO() {
			t.Error("an adoption bound the job that the reset is ending")
		}
	})
	if err := os.WriteFile(f.release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-reset; err != nil {
		t.Fatal(err)
	}
	f.assertStopped(t)
	if id, err := d.claudeCTOSession(); err != nil || id != "" {
		t.Fatalf("stored conversation after the reset = %q, %v; want empty", id, err)
	}
}
