package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkerHandbackFilesAcrossNodes(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	a := startWorkerNode(t, g, shortSet, func(c *Config) { c.Files.Home = t.TempDir() })
	remote := &fakeAdapter{harness: "fake"}
	b := startNode(t, g, nodeOpts{adapter: remote, cfg: func(c *Config) {
		c.Files.Home = t.TempDir()
		c.Files.Poll, c.Files.PollMax = 20*time.Millisecond, 50*time.Millisecond
	}})
	remote.setSessions(protocol.SessionInfo{Harness: "fake", NativeID: "caller", PID: 1, Cwd: t.TempDir(), Status: "idle"})
	var caller string
	eventually(t, "remote caller", func() bool { caller = remote.host.SessionID("fake", "caller"); return caller != "" })
	repo := newRepo(t, "shop")
	a.register(t, repo)
	res := a.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "FAKE:ignore wait", Isolation: protocol.IsolationWorktree})
	w := a.d.workerBySession(res.SessionID)
	if w == nil {
		t.Fatal("worker missing")
	}
	a.d.wmu.Lock()
	w.owner, w.taskMsg = caller, ""
	a.d.wmu.Unlock()
	inside := writeTestFile(t, filepath.Join(res.Worktree, "result.txt"), []byte("worker result\n"))
	outside := writeTestFile(t, filepath.Join(t.TempDir(), "outside.txt"), []byte("outside\n"))
	link := filepath.Join(res.Worktree, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), scaled(30*time.Second))
	defer cancel()
	for _, p := range []string{outside, link} {
		if _, err := a.d.handback(ctx, res.SessionID, protocol.WorkerHandbackRequest{Report: "done", Files: []string{p}}); err == nil {
			t.Errorf("handback accepted outside path %s", p)
		}
	}
	out, err := a.d.handback(ctx, res.SessionID, protocol.WorkerHandbackRequest{Report: "the report", Status: protocol.StatusBlocked, Files: []string{inside}})
	if err != nil {
		t.Fatalf("handback: %v", err)
	}
	if out.Sent == nil || len(out.Sent.Files) != 1 {
		t.Fatalf("handback result: %+v", out)
	}
	msgID := out.Sent.ID
	dest := filepath.Join(b.cfg.StateDir, "inbox", caller, msgID, "result.txt")
	eventually(t, "caller receives notice with file", func() bool {
		for _, m := range remote.delivered() {
			if m.ID == msgID && strings.Contains(m.Body, "the report") && strings.Contains(m.Body, filepath.Dir(dest)) {
				return true
			}
		}
		return false
	})
	if got := string(mustReadFile(t, dest)); got != "worker result\n" {
		t.Fatalf("file contents = %q", got)
	}
}
