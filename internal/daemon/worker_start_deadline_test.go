package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkerStartWorktreeOutlivesRequest(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	_, owner := helloPi(t, w.node, "owner", repo, true)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$3\" = worktree ] && [ \"$4\" = add ]; then sleep 1; fi\nexec %q \"$@\"\n", git)
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	oldTimeout := workerGitCommandTimeout.Load()
	workerGitCommandTimeout.Store(int64(600 * time.Millisecond))
	t.Cleanup(func() { workerGitCommandTimeout.Store(oldTimeout) })
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	out, err := w.d.startWorker(ctx, protocol.WorkerLaunch{Project: "shop", Path: repo, Harness: "opencode", Model: "fake/m1", Owner: owner, Task: "do work", Isolation: protocol.IsolationWorktree})
	if err != nil || !out.Starting || out.SessionID == "" || out.Worktree == "" || out.Branch == "" {
		t.Fatalf("start should return immediately with worker metadata: %+v, %v", out, err)
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("start returned after its request deadline: %v", err)
	}
	eventually(t, "the started notice", func() bool {
		count := 0
		for _, m := range w.messagesOf(t, owner) {
			if strings.Contains(m.Body, "status: started") && m.From == out.SessionID {
				count++
			}
		}
		return count == 1
	})
	if ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("request did not expire while checkout was running: %v", ctx.Err())
	}
	if _, err := os.Stat(out.Worktree); err != nil {
		t.Fatalf("checkout did not finish: %v", err)
	}
}

func TestWorkerStartWorktreeFailureNotice(t *testing.T) {
	for _, tc := range []struct{ name, output, want string }{
		{"git", "checkout failed", "checkout failed"},
		{"lfs", "git-lfs: command not found", "git-lfs is missing on this machine"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			w := startWorkerNode(t, g, shortSet, nil)
			repo := newRepo(t, "shop")
			w.register(t, repo)
			_, owner := helloPi(t, w.node, "owner", repo, true)
			git, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			script := fmt.Sprintf("#!/bin/sh\nif [ \"$3\" = worktree ] && [ \"$4\" = add ]; then echo %q >&2; exit 1; fi\nexec %q \"$@\"\n", tc.output, git)
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			out, err := w.d.startWorker(context.Background(), protocol.WorkerLaunch{Project: "shop", Path: repo, Harness: "opencode", Model: "fake/m1", Owner: owner, Task: "do work", Isolation: protocol.IsolationWorktree})
			if err != nil || !out.Starting {
				t.Fatalf("start: %+v %v", out, err)
			}
			eventually(t, "one failure notice", func() bool {
				n := 0
				for _, m := range w.messagesOf(t, owner) {
					if strings.Contains(m.Body, "status: not started") && strings.Contains(m.Body, tc.want) {
						n++
					}
				}
				return n == 1
			})
			events, err := g.gw.Store().Events("worker.not_started")
			if err != nil || len(events) != 1 || !strings.Contains(events[0], tc.want) {
				t.Fatalf("failure event: %v, %v", events, err)
			}
			rows, err := w.d.st.workers()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, row := range rows {
				if row.SessionID == out.SessionID && row.EndedReason == protocol.StopStartFails {
					found = true
				}
			}
			if !found {
				t.Fatalf("no start_failed record for %s: %+v", out.SessionID, rows)
			}
		})
	}
}

func TestWorkerStartReturnsStartingThroughGateway(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, owner := helloPi(t, w.node, "caller", repo, true)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	release := filepath.Join(bin, "release")
	defer os.WriteFile(release, nil, 0600)
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$3\" = worktree ] && [ \"$4\" = add ]; then i=0; while [ ! -f %q ] && [ \"$i\" -lt 1000 ]; do sleep 0.01; i=$((i + 1)); done; [ -f %q ] || exit 1; fi\nexec %q \"$@\"\n", release, release, git)
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	raw, err := pi.call("worker_start", protocol.WorkerStartRequest{Touches: []string{"other"}, Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "do work", Isolation: protocol.IsolationWorktree})
	if err != nil {
		t.Fatal(err)
	}
	var out protocol.WorkerStartResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Status != "starting" || out.SessionID == "" || out.Worktree == "" || out.Branch == "" {
		t.Fatalf("start: %+v", out)
	}
	if _, err := os.Stat(out.Worktree); !os.IsNotExist(err) {
		t.Fatalf("checkout finished before reply: %v", err)
	}
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the worker's started notice and task", func() bool {
		notice, task := 0, 0
		for _, m := range w.messagesOf(t, owner) {
			if m.From == out.SessionID && strings.Contains(m.Body, "status: started") {
				notice++
			}
			if m.To == out.SessionID && m.Type == protocol.TypeRequest && strings.Contains(m.Body, "do work") {
				task++
			}
		}
		return notice == 1 && task == 1
	})
}

func TestWorkerContinuedStartRetainsCTOAttribution(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	first := w.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "first", Isolation: protocol.IsolationWorktree})
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: first.SessionID})
	w.workerStopped(t, g, protocol.StopRequested)
	out, err := w.d.continueWorker(context.Background(), protocol.WorkerContinue{Session: first.SessionID, Requester: w.ownerID(), FromCTO: true})
	if err != nil || !out.Starting {
		t.Fatalf("continue: %+v, %v", out, err)
	}
	eventually(t, "continued task retains CTO attribution", func() bool {
		for _, m := range w.messagesOf(t, w.ownerID()) {
			if m.To == out.SessionID && m.Type == protocol.TypeRequest && m.From == w.ownerID() && !m.FromCTO {
				return true
			}
		}
		return false
	})
}
