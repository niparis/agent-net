package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func workerIDOf(res protocol.WorkerStartResult) string {
	return strings.TrimPrefix(res.Branch, "agent-net/")
}

// A worker that is stopped with uncommitted changes in its worktree: the
// daemon commits them on the worker's branch, and the notice says so.
func TestStoppedWorkerKeepsUnfinishedWork(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "write half.txt", Isolation: protocol.IsolationWorktree})
	if err := os.WriteFile(filepath.Join(res.Worktree, "half.txt"), []byte("half\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
	w.workerStopped(t, g, protocol.StopRequested)
	n := w.waitNotice(t, 1)[0]
	for _, want := range []string{"status: stopped", "left uncommitted changes; committed as WIP ", ": 1 files"} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("notice lacks %q:\n%s", want, n.Body)
		}
	}
	subject := gitOutT(t, repo, "log", "-1", "--format=%s", res.Branch)
	if want := "WIP: uncommitted changes at worker end (stopped)"; subject != want {
		t.Errorf("commit subject %q, want %q", subject, want)
	}
	if got := gitOutT(t, repo, "show", res.Branch+":half.txt"); got != "half" {
		t.Errorf("half.txt on the branch: %q", got)
	}
	if st := gitOutT(t, res.Worktree, "status", "--porcelain"); st != "" {
		t.Errorf("worktree still has changes: %s", st)
	}
	eventually(t, "successful end drops the worker state directory", func() bool {
		_, err := os.Stat(filepath.Join(w.cfg.StateDir, "workers", workerIDOf(res)))
		return os.IsNotExist(err)
	})
}

// A worker lost while the daemon was down: the new daemon commits its changes.
func TestLostWorkerKeepsUnfinishedWork(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "x", Isolation: protocol.IsolationWorktree})
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
	if !strings.Contains(n.Body, "left uncommitted changes; committed as WIP ") {
		t.Errorf("notice: %s", n.Body)
	}
	if subject := gitOutT(t, repo, "log", "-1", "--format=%s", res.Branch); subject != "WIP: uncommitted changes at worker end (lost)" {
		t.Errorf("commit subject %q", subject)
	}
}

// A worker with no changes leaves no commit and no mention in its notice.
func TestNoUnfinishedCommitWithoutChanges(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "x", Isolation: protocol.IsolationWorktree})
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
	n := w.waitNotice(t, 1)[0]
	if strings.Contains(n.Body, "unfinished changes") {
		t.Errorf("notice: %s", n.Body)
	}
}

// Continue an ended worker by its session ID or its worker ID: a new worker on
// a new branch from the old branch, with the full first task.
func TestContinueEndedWorker(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	long := "write half.txt " + strings.Repeat("detail line that is long. ", 40) + "\nsecond line of the task"
	old := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: long, Isolation: protocol.IsolationWorktree, Touches: []string{"ui", "tests"}})

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
		rows, err := w.d.st.workers()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range rows {
			if row.SessionID == res.SessionID {
				found = true
				if row.Touches != "ui,tests" {
					t.Errorf("continued touches = %q", row.Touches)
				}
			}
		}
		if !found {
			t.Fatal("continued worker record missing")
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
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	old := w.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "x", Isolation: protocol.IsolationWorktree})
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
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	task := "line one\n" + strings.Repeat("y", 3000)
	res := w.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: task, Isolation: protocol.IsolationNone})
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
	t.Parallel()
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

func TestContinueAfterCodexResumeKeepsOriginalTask(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startCodexNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	owner := w.ownerID()
	originalTask := "implement the original Codex task"
	old := w.mustStart(t, protocol.WorkerStartRequest{Model: "gpt-fake", Project: "shop", Harness: "codex", Isolation: protocol.IsolationNone, Task: originalTask})
	firstNotice := w.waitNotice(t, 1)[0]
	if !strings.Contains(firstNotice.Body, "status: done") {
		t.Fatalf("first hand-back: %s", firstNotice.Body)
	}
	eventually(t, "the first Codex worker ends and is reported", func() bool {
		row, ok, err := w.d.st.workerBySession(old.SessionID)
		return w.d.workerCount() == 0 && err == nil && ok && row.EndedReason == protocol.StopHandback && row.Reported && row.Resumable
	})

	var sent protocol.MessageSent
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: old.SessionID, Body: "continue the same task"}), &sent)
	if reply := w.replyTo(t, owner, sent.Message.ID); !strings.Contains(reply.Body, "status: done") {
		t.Fatalf("resumed Codex reply: %s", reply.Body)
	}
	secondNotice := w.waitNotice(t, 1)[0]
	if !strings.Contains(secondNotice.Body, "status: done") {
		t.Fatalf("second hand-back: %s", secondNotice.Body)
	}
	eventually(t, "the resumed Codex worker ends and is reported", func() bool {
		row, ok, err := w.d.st.workerBySession(old.SessionID)
		return w.d.workerCount() == 0 && err == nil && ok && row.EndedReason == protocol.StopHandback && row.Reported && row.Resumable
	})

	continued, err := w.startDefault(protocol.WorkerStartRequest{Continue: old.SessionID})
	if err != nil {
		t.Fatalf("continue after Codex resume: %v", err)
	}
	if continued.Task == nil || !strings.Contains(continued.Task.Body, originalTask) {
		t.Fatalf("continued task does not contain original task %q: %+v", originalTask, continued.Task)
	}
	w.waitNotice(t, 1)
}

// A continued start goes through admission like any start: with no room it is
// queued, keeps its base and its task, and starts when there is room.
func TestContinueIsQueuedWhenTheMachineIsBusy(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	fl := &fakeLoad{l: roomyLoad()}
	w := startWorkerNode(t, g, queueSet, withLoad(fl))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	pi, piID := helloPi(t, w.node, "caller", "/work/caller", true)
	old := startFrom(t, pi, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "write half.txt", Isolation: protocol.IsolationWorktree})
	if old.SessionID == "" {
		t.Fatalf("%+v", old)
	}
	if old.Status == "starting" {
		for {
			m := pi.nextPush()
			if m.From == old.SessionID && strings.Contains(m.Body, "status: started") {
				break
			}
		}
		eventually(t, "the first worker receives its task", func() bool {
			for _, m := range w.messagesOf(t, piID) {
				if m.To == old.SessionID && m.Type == protocol.TypeRequest {
					return true
				}
			}
			return false
		})
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
			if row.TaskID != old.SessionID || row.FixRun != 1 || row.ContinueRun != 0 {
				t.Errorf("queued continuation lost its root or rework: %+v", row)
			}
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
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	far := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "far")
	far.register(t, repo)
	old := w.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "far", Harness: "opencode", Task: "x", Isolation: protocol.IsolationWorktree})
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: old.SessionID})
	w.workerStopped(t, g, protocol.StopRequested)
	far.d.Stop()
	eventually(t, "a clear error for the offline machine", func() bool {
		_, err := w.startDefault(protocol.WorkerStartRequest{Continue: old.SessionID})
		return err != nil && strings.Contains(err.Error(), "is offline, so worker "+old.SessionID+" cannot be continued")
	})
}

func TestUnrestorableAdoptedWorkerSavesChanges(t *testing.T) {
	t.Parallel()
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
			res := w.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: harness, Task: "FAKE:slow", Isolation: protocol.IsolationWorktree})
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
			for _, want := range []string{"status: lost after daemon restart", "left uncommitted changes; committed as WIP ", ": 1 files"} {
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

func TestContinueChainKeepsEarlierCommits(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	a := w.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "write work", Isolation: protocol.IsolationWorktree})
	if err := os.WriteFile(filepath.Join(a.Worktree, "chain.txt"), []byte("work\n"), 0644); err != nil {
		t.Fatal(err)
	}
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: a.SessionID})
	w.workerStopped(t, g, protocol.StopRequested)
	b, err := w.startDefault(protocol.WorkerStartRequest{Continue: a.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: b.SessionID})
	w.workerStopped(t, g, protocol.StopRequested)
	// Main can advance after B ends. C must still start from B's original tip.
	gitOutT(t, repo, "merge", "--ff-only", a.Branch)
	if err := os.WriteFile(filepath.Join(repo, "later.txt"), []byte("later\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitOutT(t, repo, "add", "later.txt")
	gitOutT(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "later main change")
	c, err := w.startDefault(protocol.WorkerStartRequest{Continue: b.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := gitOutT(t, c.Worktree, "rev-parse", "HEAD"), gitOutT(t, repo, "rev-parse", a.Branch); got != want {
		t.Errorf("continued from %s, want earlier worker commit %s", got, want)
	}
	if strings.Contains(c.Task.Body, "left no changes") {
		t.Errorf("incorrect brief: %s", c.Task.Body)
	}
	if _, err := os.Stat(filepath.Join(c.Worktree, "chain.txt")); err != nil {
		t.Errorf("missing earlier work: %v", err)
	}
}

func TestContinueArchivedWorkerAfterCleanup(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	old := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "x", Isolation: protocol.IsolationWorktree})
	if err := os.WriteFile(filepath.Join(old.Worktree, "work.txt"), []byte("work"), 0644); err != nil {
		t.Fatal(err)
	}
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: old.SessionID})
	w.workerStopped(t, g, protocol.StopRequested)
	w.waitNotice(t, 1)
	st := g.gw.Store()
	if _, err := w.d.st.db.Exec(`UPDATE workers SET started_at=? WHERE session_id=?`, time.Now().Add(-24*time.Hour).UnixNano(), old.SessionID); err != nil {
		t.Fatal(err)
	}
	retained, err := w.d.retainedWorkerSessionIDs()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CleanWorkers(w.d.NodeID(), time.Nanosecond, retained); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ArchiveSessions(protocol.SessionsArchiveRequest{IDs: []string{old.SessionID}}); err != nil {
		t.Fatal(err)
	}
	w.d.cleanupWorkerSessions(t.Context())
	w.d.cleanupWorkers(t.Context())
	if _, err := w.startDefault(protocol.WorkerStartRequest{Continue: old.SessionID}); err != nil {
		t.Fatalf("continue archived worker: %v", err)
	}
}

func TestContinueWorkerMissingWorktree(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	old := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "x", Isolation: protocol.IsolationWorktree})
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: old.SessionID})
	w.workerStopped(t, g, protocol.StopRequested)
	w.waitNotice(t, 1)
	if err := os.RemoveAll(old.Worktree); err != nil {
		t.Fatal(err)
	}
	_, err := w.startDefault(protocol.WorkerStartRequest{Continue: old.SessionID})
	if err == nil || !strings.Contains(err.Error(), "worktree") || !strings.Contains(err.Error(), "gone") {
		t.Fatalf("missing worktree error: %v", err)
	}
}
