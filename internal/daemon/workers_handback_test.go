package daemon

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// notices returns the completion notices the owner received, oldest first.
func (w *workerNode) notices(t *testing.T) []protocol.Message {
	t.Helper()
	var out []protocol.Message
	ms := w.messagesOf(t, w.ownerID())
	for i := len(ms) - 1; i >= 0; i-- { // messages come newest first
		if ms[i].To == w.ownerID() && strings.HasPrefix(ms[i].Body, "[agent-net worker notice]") {
			out = append(out, ms[i])
		}
	}
	return out
}

func (w *workerNode) waitNotice(t *testing.T, n int) []protocol.Message {
	t.Helper()
	var got []protocol.Message
	eventually(t, "worker notices", func() bool {
		got = w.notices(t)
		return len(got) >= n
	})
	return got
}

func gitOutT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// A worker with its own worktree edits, hands back, and the caller gets the
// report with the facts and the diff stat. The worker ends; a follow-up starts
// the same session again, and its worktree is kept because it has changes.
func TestWorkerWorktreeHandbackAndNotice(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	owner := w.ownerID()

	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:edit FAKE:handback add work.txt", Isolation: protocol.IsolationWorktree})
	wantDir := filepath.Join(w.cfg.StateDir, "worktrees")
	if !strings.HasPrefix(res.Worktree, wantDir) || res.Branch == "" || !strings.HasPrefix(res.Branch, "agent-net/w_") {
		t.Fatalf("worktree %q branch %q (want under %s)", res.Worktree, res.Branch, wantDir)
	}
	if strings.HasPrefix(res.Worktree, repo) {
		t.Fatalf("the worktree is inside the user's checkout: %s", res.Worktree)
	}
	if res.Path != repo {
		t.Errorf("the project path must stay the main tree: %s", res.Path)
	}
	if !strings.Contains(res.Task.Body, "You work in a git worktree of the project at "+res.Worktree+" on the branch "+res.Branch) {
		t.Errorf("the brief does not name the worktree: %q", res.Task.Body)
	}
	if !strings.HasPrefix(res.Task.Body, protocol.WorkerPreamble) || !strings.Contains(res.Task.Body, "agent_handback") {
		t.Errorf("task body: %q", res.Task.Body)
	}
	rec := readFake(t, res.Worktree)
	if rec.Cwd != res.Worktree {
		t.Errorf("the worker runs in %s, not in its worktree", rec.Cwd)
	}
	if gitOutT(t, repo, "branch", "--list", res.Branch) == "" {
		t.Error("the branch does not exist in the project")
	}

	// The notice: facts the daemon measured, then the report as model output.
	n := w.waitNotice(t, 1)[0]
	if n.From != res.SessionID || n.To != owner || n.ParentID != res.Task.ID || n.Type != protocol.TypeReply {
		t.Fatalf("notice message: %+v", n)
	}
	if !n.FromWorker {
		t.Error("the gateway does not mark the sender as a worker")
	}
	for _, want := range []string{"status: done", "worker: " + res.SessionID + " (opencode, fake/m1), project shop on " + w.cfg.Hostname,
		"run time: ", "tokens: 1200 in, 340 out", "cost: 0.0123", "worktree: " + res.Worktree + " (branch " + res.Branch + ")",
		"changes (git diff --stat):", "work.txt | 2 ++", protocol.ReportMarker, "finished the task in " + res.Worktree} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("notice lacks %q:\n%s", want, n.Body)
		}
	}
	if strings.Index(n.Body, "status: done") > strings.Index(n.Body, protocol.ReportMarker) {
		t.Error("the facts must come before the report")
	}
	// The user's own checkout is untouched.
	if _, err := os.Stat(filepath.Join(repo, "work.txt")); err == nil {
		t.Error("the worker edited the user's checkout")
	}
	if st := gitOutT(t, repo, "status", "--porcelain"); st != "" {
		t.Errorf("the user's checkout is dirty: %s", st)
	}

	// A done worker ends, like a subagent. A follow-up from its owner starts
	// the same session again, in the same worktree.
	eventually(t, "a worker that handed back ends", func() bool {
		return w.d.workerCount() == 0 && !alive(rec.Pid) && len(w.workers(t)) == 0
	})
	if rows, _ := w.d.st.workers(); len(rows) != 1 || !rows[0].Resumable || rows[0].SessionID != res.SessionID {
		t.Fatalf("the ended worker record: %+v", rows)
	}
	var sent protocol.MessageSent
	raw := w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "one more thing"})
	mustJSON(t, raw, &sent)
	if sent.Message == nil {
		t.Fatalf("owner_send gave %s", raw)
	}
	if r := w.replyTo(t, owner, sent.Message.ID); !strings.HasPrefix(r.Body, "ok from worker") {
		t.Errorf("follow-up: %+v", r)
	}
	rec2 := readFake(t, res.Worktree)
	if rec2.SessionID != rec.SessionID || rec2.Pid == rec.Pid || rec2.Cwd != res.Worktree || rec2.Env["XDG_DATA_HOME"] != rec.Env["XDG_DATA_HOME"] {
		t.Errorf("the resumed worker: %+v (first run %+v)", rec2, rec)
	}
	if len(w.logs.lines("worker.resumed", res.SessionID)) != 1 {
		t.Errorf("no worker.resumed line: %v", w.logs.lines("worker.resumed"))
	}

	// Stopping the resumed worker (it replied but did not hand back) sends one
	// notice with status stopped. It has changes, so the worktree and its
	// branch stay.
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
	w.workerStopped(t, g, protocol.StopRequested)
	eventually(t, "process gone", func() bool { return !alive(rec.Pid) })
	notices := w.waitNotice(t, 2)
	if len(notices) != 2 || !strings.Contains(notices[1].Body, "status: stopped") {
		t.Errorf("the notices of the two runs: %+v", notices)
	}
	if _, err := os.Stat(filepath.Join(res.Worktree, "work.txt")); err != nil {
		t.Errorf("the worktree with changes was removed: %v", err)
	}
	if gitOutT(t, repo, "branch", "--list", res.Branch) == "" {
		t.Error("the branch with changes was removed")
	}
	if len(w.logs.lines("worker.worktree kept", res.Branch)) != 2 {
		t.Errorf("no 'kept' line for each end: %v", w.logs.lines("worker.worktree"))
	}
	if len(w.logs.lines("worker.handback", "status=done")) != 1 {
		t.Errorf("no handback line")
	}
}

// A worktree with no changes is removed when the worker ends.
func TestWorkerWorktreeWithoutChangesIsRemoved(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "say ok", Isolation: protocol.IsolationWorktree})
	if _, err := os.Stat(res.Worktree); err != nil {
		t.Fatal(err)
	}
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
	w.workerStopped(t, g, protocol.StopRequested)
	eventually(t, "worktree removed", func() bool {
		_, err := os.Stat(res.Worktree)
		return err != nil && gitOutT(t, repo, "branch", "--list", res.Branch) == ""
	})
	// Without a hand-back, the stop still sends a notice with its status.
	n := w.waitNotice(t, 1)[0]
	for _, want := range []string{"status: stopped", "changes: none", "worktree: " + res.Worktree} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("notice lacks %q:\n%s", want, n.Body)
		}
	}
	if strings.Contains(n.Body, protocol.ReportMarker) {
		t.Error("a notice with no report has a report marker")
	}
}

// A worker that times out without handing back sends a "timed out" notice.
func TestWorkerTimedOutSendsANotice(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.Idle = 400 * time.Millisecond
	w := startWorkerNode(t, g, set, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "FAKE:ignore keep quiet"})
	w.workerStopped(t, g, protocol.StopIdle)
	n := w.waitNotice(t, 1)[0]
	for _, want := range []string{"status: timed out", "note: no message to or from the worker for 400ms", "worker: " + res.SessionID,
		"tokens: 1200 in, 340 out", "run time: "} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("notice lacks %q:\n%s", want, n.Body)
		}
	}
	if n.ParentID != res.Task.ID || n.Type != protocol.TypeReply {
		t.Errorf("notice is not a reply to the task: %+v", n)
	}
	// The same for the longest run time.
	g2 := startGateway(t, t.TempDir(), "")
	set = shortSet
	set.MaxAge = 400 * time.Millisecond
	w2 := startWorkerNode(t, g2, set, nil)
	repo2 := newRepo(t, "shop")
	w2.register(t, repo2)
	w2.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	if n := w2.waitNotice(t, 1)[0]; !strings.Contains(n.Body, "status: timed out") || !strings.Contains(n.Body, "longest time allowed") {
		t.Errorf("max age notice:\n%s", n.Body)
	}
}

// A worker whose model call fails (a provider or billing error) ends at once
// with a "failed" notice that carries the cleaned error text. Without this it
// sat idle until the idle limit and its owner waited for nothing.
func TestWorkerModelFailureSendsANotice(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.Idle = time.Hour // the failure must not wait for the idle limit
	w := startWorkerNode(t, g, set, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "FAKE:modelfail do it"})
	w.workerStopped(t, g, protocol.StopModelFails)
	n := w.waitNotice(t, 1)[0]
	for _, want := range []string{"status: failed", "note: the model call failed: AI.Error: Upstream request failed: Insufficient account funds.",
		"worker: " + res.SessionID} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("notice lacks %q:\n%s", want, n.Body)
		}
	}
	if strings.Contains(n.Body, "sk-abcdefghijklmnopqrstuvwxyz123456") {
		t.Errorf("the notice leaks a secret:\n%s", n.Body)
	}
	if !strings.Contains(n.Body, "[REDACTED] add funds\n") || strings.Count(n.Body, "Insufficient") != 1 {
		t.Errorf("the error is not one clean line:\n%s", n.Body)
	}
	if n.ParentID != res.Task.ID || n.Type != protocol.TypeReply {
		t.Errorf("notice is not a reply to the task: %+v", n)
	}
	if w.d.workerCount() != 0 {
		t.Error("the worker is still running")
	}
	// The gateway now ranks the model last for the same node and harness.
	failed, err := g.gw.Store().FailedModels(w.d.NodeID(), "opencode")
	if err != nil || failed["fake/m1"].IsZero() {
		t.Errorf("the gateway did not record the failed model: %v, %v", failed, err)
	}
	// A retry of the same loose request ("m" matches fake/m1 and fake/m2) now
	// picks the other model, and the note says why.
	res2 := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "m", Task: "FAKE:ignore retry"})
	if res2.Model != "fake/m2" || !strings.Contains(res2.ModelNote, "m -> fake/m2") || !strings.Contains(res2.ModelNote, "fake/m1 failed a worker at") {
		t.Errorf("retry: model %q, note %q", res2.Model, res2.ModelNote)
	}
}

func TestWorkerLocalFailureDoesNotDemote(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1", Task: "FAKE:localfail"})
	w.workerStopped(t, g, protocol.StopRunFailed)
	n := w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "status: failed") || !strings.Contains(n.Body, "Failed to execute statement") {
		t.Errorf("notice: %s", n.Body)
	}
	failed, err := g.gw.Store().FailedModels(w.d.NodeID(), "opencode")
	if err != nil || !failed["fake/m1"].IsZero() {
		t.Errorf("local failure demoted model: %v %v", failed, err)
	}
}

// A worker whose process dies sends a "failed" notice.
func TestWorkerCrashSendsANotice(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x"})
	rec := readFake(t, repo)
	syscall.Kill(-rec.Pgid, syscall.SIGKILL)
	w.workerStopped(t, g, protocol.StopExited)
	n := w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "status: failed") || !strings.Contains(n.Body, "ended by itself") {
		t.Errorf("crash notice:\n%s", n.Body)
	}
}

// A worker may hand back blocked.
func TestWorkerHandbackBlocked(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "FAKE:handback FAKE:blocked need a password"})
	if n := w.waitNotice(t, 1)[0]; !strings.Contains(n.Body, "status: blocked") {
		t.Errorf("notice:\n%s", n.Body)
	}
}

// Only a worker's own session can hand back.
func TestHandbackIsOnlyForWorkers(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	pi, _ := helloPi(t, w.node, "pi-x", "/work/x", true)
	if _, err := pi.call("worker_handback", protocol.WorkerHandbackRequest{Report: "done"}); err == nil ||
		!strings.Contains(err.Error(), "not one") {
		t.Errorf("an ordinary agent handed back: %v", err)
	}
	if _, err := w.cli.call("worker_handback", protocol.WorkerHandbackRequest{Report: "done"}); err == nil {
		t.Error("the command line handed back")
	}
}

func TestWorkerIsolationIsChecked(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	_, err := w.start(protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "x", Isolation: "container"})
	if err == nil || !strings.Contains(err.Error(), "isolation can be") {
		t.Fatalf("isolation container: %v", err)
	}
	if w.d.workerCount() != 0 {
		t.Fatal("a worker started")
	}
	// A project that is not a repository with a commit cannot get a worktree.
	empty := filepath.Join(t.TempDir(), "plain")
	os.MkdirAll(empty, 0o755)
	gitIn(t, empty, "init", "-q", "-b", "main")
	w.register(t, empty)
	_, err = w.start(protocol.WorkerStartRequest{Project: "plain", Harness: "opencode", Task: "x", Isolation: protocol.IsolationWorktree})
	if err == nil || !strings.Contains(err.Error(), "cannot make a worktree") {
		t.Fatalf("no commit: %v", err)
	}
	if w.d.workerCount() != 0 {
		t.Fatal("a failed worktree left a worker")
	}
}

func mustJSON(t *testing.T, raw json.RawMessage, v any) {
	t.Helper()
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("bad JSON %s: %v", raw, err)
	}
}
