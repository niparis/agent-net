package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func workerIDOf(res protocol.WorkerStartResult) string {
	return strings.TrimPrefix(res.Branch, "agent-net/")
}

// A worker that is stopped with uncommitted changes in its worktree: the
// daemon commits them on the worker's branch, and the notice says so.
func TestStoppedWorkerKeepsUnfinishedWork(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "write half.txt", Isolation: protocol.IsolationWorktree})
	if err := os.WriteFile(filepath.Join(res.Worktree, "half.txt"), []byte("half\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
	w.workerStopped(t, g, protocol.StopRequested)
	n := w.waitNotice(t, 1)[0]
	for _, want := range []string{"status: stopped", "unfinished changes were saved as a commit on branch " + res.Branch} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("notice lacks %q:\n%s", want, n.Body)
		}
	}
	subject := gitOutT(t, repo, "log", "-1", "--format=%s", res.Branch)
	if want := "agent-net: unfinished work of worker " + workerIDOf(res) + " (stopped)"; subject != want {
		t.Errorf("commit subject %q, want %q", subject, want)
	}
	if got := gitOutT(t, repo, "show", res.Branch+":half.txt"); got != "half" {
		t.Errorf("half.txt on the branch: %q", got)
	}
	if st := gitOutT(t, res.Worktree, "status", "--porcelain"); st != "" {
		t.Errorf("worktree still has changes: %s", st)
	}
}

// A worker lost while the daemon was down: the new daemon commits its changes.
func TestLostWorkerKeepsUnfinishedWork(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x", Isolation: protocol.IsolationWorktree})
	rec := readFake(t, res.Worktree)
	if err := os.WriteFile(filepath.Join(res.Worktree, "half.txt"), []byte("half\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.d.Stop()
	syscall.Kill(-rec.Pgid, syscall.SIGKILL)
	eventually(t, "the worker died", func() bool { return !alive(rec.Pid) })
	w.node.restart()
	w.cli = cliClient(t, w.node)
	w.workerStopped(t, g, protocol.StopLost)
	n := w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "unfinished changes were saved as a commit on branch "+res.Branch) {
		t.Errorf("notice: %s", n.Body)
	}
	if subject := gitOutT(t, repo, "log", "-1", "--format=%s", res.Branch); subject != "agent-net: unfinished work of worker "+workerIDOf(res)+" (lost)" {
		t.Errorf("commit subject %q", subject)
	}
}

// A worker with no changes leaves no commit and no mention in its notice.
func TestNoUnfinishedCommitWithoutChanges(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x", Isolation: protocol.IsolationWorktree})
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
	n := w.waitNotice(t, 1)[0]
	if strings.Contains(n.Body, "unfinished changes") {
		t.Errorf("notice: %s", n.Body)
	}
}

// Continue an ended worker by its session ID or its worker ID: a new worker on
// a new branch from the old branch, with the full first task.
func TestContinueEndedWorker(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	long := "write half.txt " + strings.Repeat("detail line that is long. ", 40) + "\nsecond line of the task"
	old := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: long, Isolation: protocol.IsolationWorktree})

	if _, err := w.startDefault(protocol.WorkerStartRequest{Continue: old.SessionID}); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Errorf("continue of a running worker: %v", err)
	}
	if _, err := w.startDefault(protocol.WorkerStartRequest{Continue: "ses_nope"}); err == nil || !strings.Contains(err.Error(), "no worker") {
		t.Errorf("continue of an unknown worker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(old.Worktree, "half.txt"), []byte("half\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: old.SessionID})
	w.workerStopped(t, g, protocol.StopRequested)
	w.waitNotice(t, 1)

	for i, ref := range []string{old.SessionID, workerIDOf(old)} {
		res, err := w.startDefault(protocol.WorkerStartRequest{Continue: ref, Task: "then add tests"})
		if err != nil {
			t.Fatalf("continue %s: %v", ref, err)
		}
		if res.SessionID == old.SessionID || res.Branch == old.Branch || res.Branch == "" || res.Worktree == old.Worktree {
			t.Fatalf("continue %d did not make a new worker: %+v", i, res)
		}
		if res.Project != "shop" || res.Harness != "opencode" || res.Model != "fake/m1" {
			t.Errorf("project, harness or model not kept: %+v", res)
		}
		if got := gitOutT(t, res.Worktree, "show", "HEAD:half.txt"); got != "half" {
			t.Errorf("unfinished work is not in the new worktree: %q", got)
		}
		for _, want := range []string{"You continue the work of an earlier worker that ended (stopped). Its unfinished changes are already in your worktree as the last commit. Review them critically, then finish the task.",
			long, "Also: then add tests"} {
			if !strings.Contains(res.Task.Body, want) {
				t.Errorf("task lacks %q:\n%s", want, res.Task.Body)
			}
		}
		w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
		w.workerStopped(t, g, protocol.StopRequested)
	}
}

// Only the owner may continue a worker. The gateway checks, and the machine
// checks its own record too.
func TestContinueNeedsTheOwner(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	old := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x", Isolation: protocol.IsolationWorktree})
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: old.SessionID})
	w.workerStopped(t, g, protocol.StopRequested)

	if _, err := w.d.continueWorker(t.Context(), protocol.WorkerContinue{Session: old.SessionID, Requester: "s_other"}); err == nil || !strings.Contains(err.Error(), "only the session that started") {
		t.Errorf("machine check: %v", err)
	}
	_, other := helloPi(t, w.node, "pi-other", "/work/other", true)
	_, err := w.d.workerStartCall(t.Context(), other, protocol.WorkerStartRequest{Continue: old.SessionID}, false, "")
	if err == nil || !strings.Contains(err.Error(), "only the session that started") {
		t.Errorf("gateway check: %v", err)
	}
}

// The full first task is kept in the worker record.
func TestWorkerRecordKeepsTheFullFirstTask(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	task := "line one\n" + strings.Repeat("y", 3000)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: task, Isolation: protocol.IsolationNone})
	r, ok, err := w.d.st.workerBySession(res.SessionID)
	if err != nil || !ok || r.TaskFull != task {
		t.Fatalf("task_full: %v %v len %d", err, ok, len(r.TaskFull))
	}
	if got := boundTask(strings.Repeat("z", 70000)); len(got) != 64*1024 {
		t.Errorf("bound: %d", len(got))
	}
}

// A worker that handed back can be continued too: its uncommitted changes are
// committed first, so the new branch has them.
func TestContinueAfterHandback(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	old := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:edit FAKE:handback add work.txt", Isolation: protocol.IsolationWorktree})
	w.workerStopped(t, g, protocol.StopHandback)
	res, err := w.startDefault(protocol.WorkerStartRequest{Continue: old.SessionID, Task: "FAKE:ignore"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(res.Worktree, "work.txt")); err != nil {
		t.Errorf("the handed-back changes are not in the new worktree: %v", err)
	}
	if !strings.Contains(res.Task.Body, "ended (handback)") {
		t.Errorf("task: %s", res.Task.Body)
	}
}

// A continued start goes through admission like any start: with no room it is
// queued, keeps its base and its task, and starts when there is room.
func TestContinueIsQueuedWhenTheMachineIsBusy(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	fl := &fakeLoad{l: roomyLoad()}
	w := startWorkerNode(t, g, queueSet, withLoad(fl))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, piID := helloPi(t, w.node, "caller", "/work/caller", true)
	old := startFrom(t, pi, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "write half.txt", Isolation: protocol.IsolationWorktree})
	if old.SessionID == "" {
		t.Fatalf("%+v", old)
	}
	if err := os.WriteFile(filepath.Join(old.Worktree, "half.txt"), []byte("half\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pi.mustCall("worker_stop", protocol.WorkerStopRequest{Session: old.SessionID})
	w.workerStopped(t, g, protocol.StopRequested)

	fl.set(cpuBusy())
	var r protocol.WorkerStartResult
	json.Unmarshal(pi.mustCall("worker_start", protocol.WorkerStartRequest{Continue: old.SessionID, Task: "and tests"}), &r)
	if r.Status != "queued" || !protocol.IsQueueID(r.QueueID) {
		t.Fatalf("continue was not queued: %+v", r)
	}
	fl.set(roomyLoad())
	var started protocol.Message
	for {
		started = pi.nextPush()
		if strings.Contains(started.Body, "status: started") {
			break
		}
	}
	sid := started.From
	if sid == "" || sid == old.SessionID {
		t.Fatalf("started notice: %+v", started)
	}
	eventually(t, "the continued task reached the new worker", func() bool {
		for _, m := range w.messagesOf(t, sid) {
			if m.From == piID && strings.Contains(m.Body, "You continue the work of an earlier worker that ended (stopped)") &&
				strings.Contains(m.Body, "write half.txt") && strings.Contains(m.Body, "Also: and tests") {
				return true
			}
		}
		return false
	})
	rows, _ := w.d.st.workers()
	found := false
	for _, row := range rows {
		if row.SessionID == sid {
			found = true
			if got := gitOutT(t, row.Worktree, "show", "HEAD:half.txt"); got != "half" {
				t.Errorf("half.txt in the new worktree: %q", got)
			}
			if row.TaskFull != "write half.txt\n\nAlso: and tests" {
				t.Errorf("task_full %q", row.TaskFull)
			}
		}
	}
	if !found {
		t.Error("no record of the new worker")
	}
}

// The machine that ran the old worker must be online.
func TestContinueNeedsTheMachineOnline(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	far := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "far")
	far.register(t, repo)
	old := w.mustStart(t, protocol.WorkerStartRequest{Project: "far", Harness: "opencode", Task: "x", Isolation: protocol.IsolationWorktree})
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: old.SessionID})
	w.workerStopped(t, g, protocol.StopRequested)
	far.d.Stop()
	eventually(t, "a clear error for the offline machine", func() bool {
		_, err := w.startDefault(protocol.WorkerStartRequest{Continue: old.SessionID})
		return err != nil && strings.Contains(err.Error(), "is offline, so worker "+old.SessionID+" cannot be continued")
	})
}

func TestUnrestorableAdoptedWorkerSavesChanges(t *testing.T) {
	for _, harness := range []string{"opencode", "codex"} {
		t.Run(harness, func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			var w *workerNode
			if harness == "codex" {
				w = startCodexNode(t, g, shortSet)
			} else {
				w = startWorkerNode(t, g, shortSet, nil)
			}
			repo := newRepo(t, "shop")
			w.register(t, repo)
			res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: harness, Task: "FAKE:slow", Isolation: protocol.IsolationWorktree})
			if harness == "codex" {
				readCodex(t, res.Worktree, func(r fakeCodexRecord) bool { return len(r.Turns) == 1 })
			} else {
				w.replyTo(t, w.ownerID(), res.Task.ID)
			}
			if err := os.WriteFile(filepath.Join(res.Worktree, "half.txt"), []byte("half\n"), 0600); err != nil {
				t.Fatal(err)
			}
			w.d.Stop()
			name := "opencode-password"
			if harness == "codex" {
				name = "token"
			}
			if err := os.Remove(filepath.Join(w.cfg.StateDir, "workers", workerIDOf(res), name)); err != nil {
				t.Fatal(err)
			}
			w.node.restart()
			w.cli = cliClient(t, w.node)
			n := w.waitNotice(t, 1)[0]
			for _, want := range []string{"status: lost after daemon restart", "unfinished changes were saved as a commit on branch " + res.Branch} {
				if !strings.Contains(n.Body, want) {
					t.Errorf("notice lacks %q: %s", want, n.Body)
				}
			}
			if got := gitOutT(t, repo, "show", res.Branch+":half.txt"); got != "half" {
				t.Errorf("saved file: %q", got)
			}
			eventually(t, "unrestorable worker ends", func() bool { return w.d.workerCount() == 0 })
		})
	}
}
