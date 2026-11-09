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
		if ms[i].To == w.ownerID() && strings.HasPrefix(ms[i].Body, "[agent-net worker notice]") && !strings.Contains(ms[i].Body, "\nstatus: started\n") && !strings.Contains(ms[i].Body, "\nstatus: difficulty\n") {
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
	t.Parallel()
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

// A worktree with no changes remains until it is old enough for cleanup.
func TestWorkerWorktreeWithoutChangesIsRetained(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "say ok", Isolation: protocol.IsolationWorktree})
	if _, err := os.Stat(res.Worktree); err != nil {
		t.Fatal(err)
	}
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
	w.workerStopped(t, g, protocol.StopRequested)
	if _, err := os.Stat(res.Worktree); err != nil {
		t.Fatalf("worktree removed too early: %v", err)
	}
	if got := gitOutT(t, repo, "branch", "--list", res.Branch); got == "" {
		t.Fatal("branch removed too early")
	}
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
	t.Parallel()
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
	set.MaxAge = time.Hour
	w2 := startWorkerNode(t, g2, set, nil)
	repo2 := newRepo(t, "shop")
	w2.register(t, repo2)
	w2.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "x"})
	ageWorkers(w2, 2*time.Hour)
	if n := w2.waitNotice(t, 1)[0]; !strings.Contains(n.Body, "status: timed out") || !strings.Contains(n.Body, "longest time allowed") {
		t.Errorf("max age notice:\n%s", n.Body)
	}
}

// A worker whose model call fails (a provider or billing error) ends at once
// with a "failed" notice that carries the cleaned error text. Without this it
// sat idle until the idle limit and its owner waited for nothing.
func TestWorkerModelFailureSendsANotice(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.Idle = time.Hour // the failure must not wait for the idle limit
	// The models are subscription-backed (openai maps to the Codex quota), so
	// the loose retry below may resolve to them.
	w := startWorkerNodeWithDetect(t, g, set, inventoryWithModels("opencode", []string{"openai/m1", "openai/m2"}), nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "openai/m1", Task: "FAKE:modelfail do it"})
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
	if err != nil || failed["openai/m1"].IsZero() {
		t.Errorf("the gateway did not record the failed model: %v, %v", failed, err)
	}
	// A retry of the same loose request ("m" matches openai/m1 and openai/m2)
	// now picks the other model, and the note says why.
	res2 := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "m", Task: "FAKE:ignore retry"})
	if res2.Model != "openai/m2" || !strings.Contains(res2.ModelNote, "m -> openai/m2") || !strings.Contains(res2.ModelNote, "openai/m1 failed a worker at") {
		t.Errorf("retry: model %q, note %q", res2.Model, res2.ModelNote)
	}
}

func TestWorkerLocalFailureDoesNotDemote(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	w.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "x"})
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
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	w.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "FAKE:handback FAKE:blocked need a password"})
	if n := w.waitNotice(t, 1)[0]; !strings.Contains(n.Body, "status: blocked") {
		t.Errorf("notice:\n%s", n.Body)
	}
}

// Only a worker's own session can hand back.
func TestHandbackIsOnlyForWorkers(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	_, err := w.start(protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "x", Isolation: "container"})
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
	_, err = w.start(protocol.WorkerStartRequest{Model: "fake/m1", Project: "plain", Harness: "opencode", Task: "x", Isolation: protocol.IsolationWorktree})
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

func TestWorkerIdleWithoutHandbackNotice(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.IdleNotice = 200 * time.Millisecond
	// The fake server is a race-built test binary. On a loaded host its start
	// can take longer than the short default, so the start bound is wider. It
	// only applies when a start fails.
	set.StartTimeout = scaled(30 * time.Second)
	w := startWorkerNode(t, g, set, nil)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("worker daemon logs:\n%s", strings.Join(w.logs.lines(), "\n"))
		}
	})
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "FAKE:edit", Isolation: protocol.IsolationWorktree, AutoContinue: autoContinueOff()})
	n := w.waitNotice(t, 1)[0]
	key := n.IdempotencyKey
	if key == "" {
		t.Fatal("idle notice has no idempotency key")
	}
	for _, want := range []string{"status: idle without hand-back", "run time:", "work.txt", "last assistant message", "finished without hand-back", protocol.ChecklistMarker} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("notice missing %q: %s", want, n.Body)
		}
	}
	eventually(t, "first idle notice acknowledged", func() bool {
		worker := w.d.workerBySession(res.SessionID)
		if worker == nil {
			return false
		}
		w.d.wmu.Lock()
		defer w.d.wmu.Unlock()
		return worker.idleNoticeSent && !worker.idleNoticePending && worker.idleNoticeKey == key
	})
	if got := len(w.notices(t)); got != 1 {
		t.Fatalf("got %d notices", got)
	}
	// Simulate a crash after the gateway accepted the notice but before the
	// daemon saved the acknowledgement. Seed the original key directly so the
	// simulation cannot capture a key cleared by a transient harness status.
	idleWorker := w.d.workerBySession(res.SessionID)
	w.d.wmu.Lock()
	id := idleWorker.id
	w.d.wmu.Unlock()
	if err := w.d.st.saveWorkerIdleNotice(id, time.Now().Add(-time.Second), key, "", false); err != nil {
		t.Fatal(err)
	}
	w.node.restart()
	w.cli = cliClient(t, w.node)
	eventually(t, "worker adopted after unrecorded acknowledgement and gateway connected", func() bool {
		return w.d.workerBySession(res.SessionID) != nil && w.d.sessionByID(res.SessionID) != nil && inventoryOf(t, w.cli, w.node)
	})
	eventually(t, "retried idle notice acknowledged", func() bool {
		worker := w.d.workerBySession(res.SessionID)
		if worker == nil {
			return false
		}
		w.d.wmu.Lock()
		defer w.d.wmu.Unlock()
		return worker.idleNoticeSent && !worker.idleNoticePending && worker.idleNoticeKey == key
	})
	if got := w.notices(t); len(got) != 1 {
		t.Fatalf("got %d notices after retry with key %q: %+v", len(got), key, got)
	}
	var followup protocol.MessageSent
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "another turn"}), &followup)
	// When the gateway link is down, the daemon queues the send in its outbox
	// and sends it on reconnect. The test needs the message to reach the
	// worker, so it accepts a queued send and waits for the gateway to have it.
	followupID := ""
	if followup.Message != nil {
		followupID = followup.Message.ID
	} else {
		eventually(t, "queued follow-up reached the gateway", func() bool {
			for _, m := range w.messagesOf(t, w.ownerID()) {
				if m.To == res.SessionID && m.Body == "another turn" {
					followupID = m.ID
					return true
				}
			}
			return false
		})
	}
	eventually(t, "follow-up assigned to worker", func() bool {
		worker := w.d.workerBySession(res.SessionID)
		if worker == nil {
			return false
		}
		w.d.wmu.Lock()
		defer w.d.wmu.Unlock()
		return worker.taskMsg == followupID
	})
	second := w.waitNotice(t, 2)[1]
	if second.ParentID != followupID || second.IdempotencyKey == key || !strings.Contains(second.Body, "idle without hand-back") {
		t.Fatalf("second idle notice parent %q, want %q, key %q after %q: %+v", second.ParentID, followupID, second.IdempotencyKey, key, second)
	}
	eventually(t, "second idle notice acknowledged", func() bool {
		worker := w.d.workerBySession(res.SessionID)
		if worker == nil {
			return false
		}
		w.d.wmu.Lock()
		defer w.d.wmu.Unlock()
		return worker.idleNoticeSent && !worker.idleNoticePending
	})
	if got := len(w.notices(t)); got != 2 {
		t.Fatalf("got %d notices after second turn", got)
	}
	// A turn can start in the harness without a plugin busy event.
	worker := w.d.workerBySession(res.SessionID)
	w.d.wmu.Lock()
	activeFile, native := filepath.Join(worker.dir, "data", "fake-active"), worker.nativeID
	w.d.wmu.Unlock()
	if err := os.WriteFile(activeFile, []byte(native), 0600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "server reports a new turn", func() bool { ls := w.d.sessionByID(res.SessionID); return ls != nil && w.d.infoOf(ls).Status == "busy" })
	if err := os.Remove(activeFile); err != nil {
		t.Fatal(err)
	}
	w.waitNotice(t, 3)
}

func TestCodexWorkerIdleNoticeAfterAutomaticHandbackFails(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.IdleNotice = 1 * time.Second
	w := startCodexNode(t, g, set)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Model: "gpt-fake", Project: "shop", Harness: "codex", Task: "FAKE:edit FAKE:hold-codex", Isolation: protocol.IsolationWorktree, AutoContinue: autoContinueOff()})
	eventually(t, "codex turn held", func() bool { _, err := os.Stat(filepath.Join(res.Worktree, "fake-codex-held")); return err == nil })
	worker := w.d.workerBySession(res.SessionID)
	w.d.wmu.Lock()
	cc := worker.codex
	w.d.wmu.Unlock()
	cc.mu.Lock()
	cc.cur.from = "s_unknown"
	cc.mu.Unlock()
	if err := os.WriteFile(filepath.Join(res.Worktree, "fake-codex-release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "automatic handback fails", func() bool {
		w.d.wmu.Lock()
		defer w.d.wmu.Unlock()
		return !worker.idleNoticeSince.IsZero()
	})
	w.node.restart()
	w.cli = cliClient(t, w.node)
	eventually(t, "codex worker adopted", func() bool { return w.d.workerBySession(res.SessionID) != nil && w.d.sessionByID(res.SessionID) != nil })
	n := w.waitNotice(t, 1)[0]
	for _, want := range []string{"status: idle without hand-back", "last assistant message", "codex done in", "work.txt", protocol.ChecklistMarker} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("notice missing %q: %s", want, n.Body)
		}
	}
	var next protocol.MessageSent
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "continue"}), &next)
	handback := w.waitNotice(t, 2)[1]
	if !strings.Contains(handback.Body, "status: done") || handback.ParentID != next.Message.ID {
		t.Fatalf("follow-up handback: %+v", handback)
	}
	eventually(t, "codex worker ends after follow-up", func() bool { return w.d.workerCount() == 0 })
}

// A Claude worker that ends its turn without a hand-back tells its caller. The
// daemon adopts it after a restart (nothing of the session belongs to the old
// daemon process), so the notice must come from the adopted worker too.
func TestClaudeWorkerIdleNoticeAfterDaemonRestart(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.IdleNotice = time.Second
	set.Idle = time.Hour
	c := startClaudeNode(t, g, set)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("worker daemon logs:\n%s", strings.Join(c.logs.lines(), "\n"))
		}
	})
	repo := newRepo(t, "shop")
	c.register(t, repo)
	res := c.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "claude", Model: "haiku",
		Task: "FAKE:ignore", AutoContinue: autoContinueOff()})
	rec := readClaude(t, repo)
	fakeClaudeTranscriptLine(c.sessions, repo, rec.Session,
		`{"type":"assistant","message":{"id":"m1","model":"claude-haiku-5-5","content":[{"type":"text","text":"I stopped before finishing."}]}}`)

	// A turn is running, so the worker is not idle yet.
	setFakeClaudeStatus(t, c.sessions, rec.Pid, "busy")
	eventually(t, "the Claude session is busy", func() bool {
		ls := c.d.sessionByID(res.SessionID)
		return ls != nil && c.d.infoOf(ls).Status == "busy"
	})
	if got := len(c.notices(t)); got != 0 {
		t.Fatalf("got %d notices while the worker was busy", got)
	}

	restarted := time.Now()
	c.node.restart()
	c.cli = cliClient(t, c.node)
	eventually(t, "the Claude worker is adopted", func() bool {
		return c.d.workerBySession(res.SessionID) != nil && c.d.sessionByID(res.SessionID) != nil
	})

	// The adopted worker's turn ends without a hand-back.
	setFakeClaudeStatus(t, c.sessions, rec.Pid, "idle")
	eventually(t, "the adopted Claude session is idle", func() bool {
		ls := c.d.sessionByID(res.SessionID)
		return ls != nil && c.d.infoOf(ls).Status == "idle"
	})
	n := c.waitNotice(t, 1)[0]
	if n.From != res.SessionID || n.ParentID != res.Task.ID || !strings.Contains(n.Body, "status: idle without hand-back") {
		t.Fatalf("idle notice: %+v", n)
	}
	if !strings.Contains(n.Body, "I stopped before finishing.") {
		t.Errorf("notice lacks the worker's last assistant message:\n%s", n.Body)
	}
	if n.CreatedAt.Before(restarted) {
		t.Fatalf("the idle notice was sent before the restart: %s < %s", n.CreatedAt, restarted)
	}
	if c.d.workerBySession(res.SessionID) == nil {
		t.Fatal("the adopted worker ended on its idle clock instead of telling the caller")
	}
}

// A Claude worker whose session process died while the daemon was down is
// reported as lost, not left running.
func TestClaudeWorkerThatDiedDuringRestartIsReportedLost(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.IdleNotice = time.Hour
	set.Idle = time.Hour
	c := startClaudeNode(t, g, set)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("worker daemon logs:\n%s", strings.Join(c.logs.lines(), "\n"))
		}
	})
	repo := newRepo(t, "shop")
	c.register(t, repo)
	c.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "claude", Model: "haiku",
		Task: "FAKE:ignore", AutoContinue: autoContinueOff()})
	rec := readClaude(t, repo)

	c.d.Stop()
	syscall.Kill(-rec.Pgid, syscall.SIGKILL)
	eventually(t, "the Claude session died", func() bool { return !alive(rec.Pid) })
	c.node.restart()
	c.cli = cliClient(t, c.node)

	if c.d.workerCount() != 0 {
		t.Fatalf("the daemon runs %d workers after losing its worker", c.d.workerCount())
	}
	c.workerStopped(t, g, protocol.StopLost)
	n := c.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "status: lost after daemon restart") {
		t.Fatalf("lost notice: %+v", n)
	}
	rows, _ := c.d.st.workers()
	if len(rows) != 1 || rows[0].EndedReason != protocol.StopLost {
		t.Fatalf("the worker row is not ended: %+v", rows)
	}
}
