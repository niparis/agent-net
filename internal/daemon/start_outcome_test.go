package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A daemon restart that cuts a Claude launch short, after the job has
// backgrounded, must leave no job behind and must still tell the caller. The
// report cannot reach the gateway while the daemon stops, so it is kept on disk
// and sent after the restart.
func TestRestartDuringClaudeLaunchStopsTheJobAndTellsTheCallerOnce(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fl := &fakeLoad{l: cpuBusy()}
	printed := filepath.Join(t.TempDir(), "printed")
	t.Setenv(envFakeHangAfterBackground, printed)
	c := startClaudeNodeWith(t, g, queueSet, withLoad(fl))
	repo := newRepo(t, "shop")
	c.register(t, repo)
	pi, owner := helloPi(t, c.node, "caller", repo, true)
	if r := startFrom(t, pi, protocol.WorkerStartRequest{Model: "haiku", Project: "shop", Harness: "claude", Task: "do work"}); r.Status != "queued" {
		t.Fatalf("start: %+v", r)
	}
	fl.set(roomyLoad())
	eventually(t, "the launch has backgrounded the job", func() bool {
		_, err := os.Stat(printed)
		return err == nil
	})

	// The restart kills the waiting claude client, as systemd does.
	c.restart()
	c.cli = cliClient(t, c.node)

	defer func() {
		if t.Failed() {
			t.Logf("logs: %v", c.logs.lines())
		}
	}()
	eventually(t, "the job the cut-short launch left was stopped", func() bool { return len(c.stops()) > 0 })
	eventually(t, "one not-started notice reached the caller after the restart", func() bool {
		return len(notStartedNotices(t, c.workerNode, owner)) == 1
	})
	if n := len(notStartedNotices(t, c.workerNode, owner)); n != 1 {
		t.Errorf("not-started notices = %d, want 1", n)
	}
}

// A not-started report that the gateway did not take before the daemon stopped
// is sent after the next connect.
func TestNotStartedReportThatDidNotReachTheGatewayIsSentAfterRestart(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	var sendCalls, reports atomic.Int32
	refuse := gatewayHook("worker.queued_start", func(ctx context.Context, n int32) (json.RawMessage, bool, error) {
		return nil, true, RemoteError("refused")
	}, &sendCalls)
	down := gatewayHook("worker.not_started", func(ctx context.Context, n int32) (json.RawMessage, bool, error) {
		if n <= notStartedTries {
			return nil, true, errGatewayTimeout
		}
		return nil, false, nil
	}, &reports)
	both := func(c *Config) {
		refuse(c)
		first := c.testGatewayCall
		down(c)
		second := c.testGatewayCall
		c.testGatewayCall = func(ctx context.Context, typ string, params any) (json.RawMessage, bool, error) {
			if typ == "worker.queued_start" {
				return first(ctx, typ, params)
			}
			return second(ctx, typ, params)
		}
	}
	w := startWorkerNode(t, g, shortSet, both)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, owner := helloPi(t, w.node, "owner", repo, true)
	startThroughGateway(t, w, pi)
	eventually(t, "every try of the report failed", func() bool { return reports.Load() >= int32(notStartedTries) })
	if n := len(notStartedNotices(t, w, owner)); n != 0 {
		t.Fatalf("the notice arrived while the gateway took no report: %d", n)
	}

	w.node.restart()
	w.cli = cliClient(t, w.node)

	eventually(t, "the kept report was sent after the restart", func() bool { return len(notStartedNotices(t, w, owner)) == 1 })
}

// A Claude session in the folder of a worker that this daemon does not know is
// what a cut-short launch leaves behind. It is stopped and never listed as an
// ordinary session of the owner.
func TestStrandedClaudeSessionInAWorkerWorktreeIsStoppedNotRegistered(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	c.register(t, repo)

	dir := filepath.Join(c.d.worktreesDir(), "w_0123456789abcdef")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sleeper := exec.Command("sleep", "300")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sleeper.Process.Kill(); sleeper.Wait() })
	pid := sleeper.Process.Pid
	raw, _ := json.Marshal(claudeSessionFile{Status: "idle", PID: pid, SessionID: "native-stranded-0001", JobID: "strandedjob", Kind: "bg", Cwd: dir})
	if err := os.WriteFile(filepath.Join(c.sessions, strconv.Itoa(pid)+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	eventually(t, "the stranded job was stopped", func() bool {
		for _, id := range c.stops() {
			if id == "strandedjob" {
				return true
			}
		}
		return false
	})
	c.d.mu.Lock()
	_, registered := c.d.sessions[key("claude", "native-stranded-0001")]
	c.d.mu.Unlock()
	if registered {
		t.Error("the stranded session was registered as an ordinary session")
	}
}

// A daemon that is killed outright cannot report the start it was launching.
// The report was held before the launch, so the next daemon sends it.
func TestStartHeldWhenTheDaemonDiedIsReportedAfterRestart(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	_, owner := helloPi(t, w.node, "caller", repo, true)

	w.d.holdAcceptedStart(protocol.NewID("a"), owner, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode"})
	if n := len(notStartedNotices(t, w, owner)); n != 0 {
		t.Fatalf("a held report was sent before any restart: %d notices", n)
	}

	w.node.restart()
	w.cli = cliClient(t, w.node)

	eventually(t, "the held report reached the caller", func() bool { return len(notStartedNotices(t, w, owner)) == 1 })
}
