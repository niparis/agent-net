package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// ---- a hand-back ends the worker, and the owner's next message resumes it ----

// A worker ends when it hands back, like a Claude Code subagent. Its process is
// gone, the notice says so, and the worker leaves the list. The owner's next
// message starts the same harness session again, and it answers.
func TestHandbackEndsTheWorkerAndOwnerFollowUpResumesIt(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	owner := w.ownerID()

	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:handback first", Isolation: protocol.IsolationNone})
	rec := readFake(t, repo)
	first := w.waitNotice(t, 1)[0]
	if !strings.Contains(first.Body, "status: done") || !strings.Contains(first.Body, "ended after its hand-back") {
		t.Errorf("the notice does not say the worker ended:\n%s", first.Body)
	}
	eventually(t, "the worker ends after its hand-back", func() bool {
		return w.d.workerCount() == 0 && !alive(rec.Pid) && len(w.workers(t)) == 0
	})
	if len(w.logs.lines("worker.stopped", "reason=handback", "ended_after_handback=1")) != 1 {
		t.Errorf("no end-after-hand-back log line: %v", w.logs.lines("worker.stopped"))
	}
	eventually(t, "the ended worker record is reported", func() bool {
		rows, _ := w.d.st.workers()
		return len(rows) == 1 && rows[0].Resumable && rows[0].Reported
	})
	if rows, _ := w.d.st.workers(); len(rows) != 1 || !rows[0].Resumable || !rows[0].Reported {
		t.Fatalf("the ended worker record: %+v", rows)
	}

	// The owner's follow-up starts the same session (same native ID) again.
	var sent protocol.MessageSent
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "one more thing"}), &sent)
	if r := w.replyTo(t, owner, sent.Message.ID); !strings.HasPrefix(r.Body, "ok from worker") {
		t.Errorf("follow-up reply: %+v", r)
	}
	var rec2 fakeOCRecord
	eventually(t, "a new worker process for the same session", func() bool {
		raw, err := os.ReadFile(filepath.Join(repo, "fake-opencode.json"))
		rec2 = fakeOCRecord{}
		return err == nil && json.Unmarshal(raw, &rec2) == nil && rec2.Pid != 0 && rec2.Pid != rec.Pid
	})
	if rec2.SessionID != rec.SessionID {
		t.Errorf("the follow-up started session %q, not %q", rec2.SessionID, rec.SessionID)
	}
	if len(w.logs.lines("worker.resumed", "session="+res.SessionID)) != 1 {
		t.Errorf("no worker.resumed line: %v", w.logs.lines("worker.resumed"))
	}
	if len(w.workers(t)) != 1 || w.workers(t)[0].SessionID != res.SessionID {
		t.Errorf("the resumed worker is not listed: %+v", w.workers(t))
	}
}

func TestOpenCodeWorkerQueuedTurnAfterHandback(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	owner := w.ownerID()
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "FAKE:wait-handback", Isolation: protocol.IsolationNone})
	var sent protocol.MessageSent
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "FAKE:queued-handback"}), &sent)
	eventually(t, "second message delivered", func() bool { return w.d.st.isDelivered(sent.Message.ID) })
	if err := os.WriteFile(filepath.Join(repo, "fake-opencode-finish"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	first := w.replyTo(t, owner, res.Task.ID)
	if !strings.Contains(first.Body, "first report") {
		t.Fatalf("first report: %s", first.Body)
	}
	second := w.replyTo(t, owner, sent.Message.ID)
	if !strings.Contains(second.Body, "second report") {
		t.Fatalf("second report: %s", second.Body)
	}
	eventually(t, "worker ends after the second report", func() bool { return w.d.workerCount() == 0 })
}

// A Pi worker cannot be started again: Pi's rpc mode has no resume, so the
// owner gets that reason, and no process is started.
func TestPiWorkerCannotBeResumed(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startPiNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "pi", Isolation: protocol.IsolationWorktree,
		Task: "FAKE:handback say ok"})
	rec := readPi(t, res.Worktree)
	w.waitNotice(t, 1)
	eventually(t, "the Pi worker ends after its hand-back", func() bool { return !alive(rec.Pid) && w.d.workerCount() == 0 })

	_, err := w.cli.call("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "and again"})
	if err == nil || !strings.Contains(err.Error(), "cannot be started again") || !strings.Contains(err.Error(), "Pi's rpc mode") {
		t.Fatalf("a Pi worker was resumed, or the reason is unclear: %v", err)
	}
	if w.d.workerCount() != 0 {
		t.Error("a Pi worker was started again")
	}
}

// Only the session that started a worker can start it again.
func TestNonOwnerCannotResumeAWorker(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "FAKE:handback done"})
	rec := readFake(t, repo)
	w.waitNotice(t, 1)
	eventually(t, "the worker ends", func() bool { return w.d.workerCount() == 0 && !alive(rec.Pid) })

	stranger, _ := helloPi(t, w.node, "pi-stranger", "/work/x", true)
	_, err := stranger.call("send", map[string]any{"to": res.SessionID, "body": "hello"})
	if err == nil || !strings.Contains(err.Error(), "only the session that started it") {
		t.Fatalf("a stranger started the worker again: %v", err)
	}
	if w.d.workerCount() != 0 {
		t.Error("a worker was started again")
	}
	rec2 := readFake(t, repo)
	if rec2.Pid != rec.Pid {
		t.Errorf("a new worker process was started: %+v", rec2)
	}
}

// A session that was a worker must not be registered by the plugin of another
// OpenCode server (for example the user's shared service, which sees the
// worker's session in the shared data folder). The daemon keeps the worker's
// record after its hand-back, and refuses a hello for that native session from
// any process that is not the worker's own private server.
func TestPluginMayNotRegisterAWorkersSession(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	owner := w.ownerID()
	at := time.Now().Add(-time.Minute)
	rows := []workerRow{
		{ID: "w_ended", Harness: "opencode", Project: "shop", Path: "/work/shop", Owner: owner,
			NativeID: "ses_worker_ended", SessionID: "s_worker_ended", StartedAt: at, LastActive: at,
			EndedReason: protocol.StopHandback, Resumable: true, Reported: true},
		{ID: "w_stopped", Harness: "opencode", Project: "shop", Path: "/work/shop", Owner: owner,
			NativeID: "ses_worker_stopped", SessionID: "s_worker_stopped", StartedAt: at, LastActive: at,
			EndedReason: protocol.StopRequested},
	}
	for _, r := range rows {
		if err := w.d.st.saveWorker(r); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range rows {
		lc := dialLocal(t, w.node.sock)
		_, err := lc.call("hello", map[string]any{"client": "opencode", "harness": "opencode",
			"native_id": r.NativeID, "cwd": "/work/shop", "status": "idle"})
		if err == nil || !strings.Contains(err.Error(), "belongs to a worker started by this daemon") {
			t.Errorf("the plugin registered worker session %s (%s): %v", r.NativeID, r.EndedReason, err)
		}
	}
}

// ---- ended workers leave the list ----

// A worker that the machine says it does not run leaves the gateway's list at
// once, instead of staying as an online worker.
func TestWorkerUnknownToItsMachineLeavesTheList(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	owner := w.ownerID()
	// A record the gateway thinks is a running worker on this machine, while
	// the daemon has no such worker.
	info := protocol.SessionInfo{Harness: "claude", NativeID: "ses_stale", Cwd: "/work/stale", Name: "stale worker",
		Status: "idle", Delivery: "native-claude", Mode: protocol.ModeBackground,
		Worker: &protocol.WorkerInfo{ID: "w_stale", Owner: owner, Project: "shop", StartedAt: time.Now().Unix()}}
	id, err := g.gw.Store().UpsertSession(w.d.NodeID(), info)
	if err != nil {
		t.Fatal(err)
	}
	if ws := w.workers(t); len(ws) != 1 || ws[0].SessionID != id {
		t.Fatalf("the stale worker is not listed first: %+v", ws)
	}
	_, err = w.cli.call("worker_stop", map[string]any{"session": id})
	if err == nil || !strings.Contains(err.Error(), "no worker with session") {
		t.Fatalf("stopping a worker the machine does not run: %v", err)
	}
	eventually(t, "the stale worker leaves the list", func() bool { return len(w.workers(t)) == 0 })
}

// A worker record that is offline and older than the longest run time is
// cleaned when the daemon starts, so it does not stay listed for weeks.
func TestLeftoverWorkerRecordIsCleanedAtStart(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	// The record is added while the daemon is stopped: a running daemon cleans
	// at its first connect, which can come before the check on a slow machine.
	owner := w.ownerID()
	w.d.Stop()
	id := addLeftoverWorkerOf(t, g, w, owner, "claude", "ses_leftover", 6*time.Hour)
	if _, ok, _ := g.gw.Store().Agent(id); !ok {
		t.Fatal("the leftover record is not there")
	}

	w.node.restart()
	w.cli = cliClient(t, w.node)
	eventually(t, "the leftover worker record is cleaned at start", func() bool {
		_, ok, _ := g.gw.Store().Agent(id)
		return !ok
	})
	if len(w.workers(t)) != 0 {
		t.Errorf("the leftover is still listed: %+v", w.workers(t))
	}
}

// The same cleanup runs periodically, so a record that goes stale while the
// daemon runs does not stay either.
func TestLeftoverWorkerRecordIsCleanedPeriodically(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	set := shortSet
	set.Cleanup = 100 * time.Millisecond
	w := startWorkerNode(t, g, set, nil)
	id := addLeftoverWorker(t, g, w, "opencode", "ses_stale", 5*time.Hour)
	eventually(t, "the leftover worker record is cleaned periodically", func() bool {
		_, ok, _ := g.gw.Store().Agent(id)
		return !ok
	})
}

// addLeftoverWorker puts a worker session of this machine in the gateway, as
// an older agent-net left it: offline, and started age ago.
func addLeftoverWorker(t *testing.T, g *testGateway, w *workerNode, harness, native string, age time.Duration) string {
	t.Helper()
	return addLeftoverWorkerOf(t, g, w, w.ownerID(), harness, native, age)
}

func addLeftoverWorkerOf(t *testing.T, g *testGateway, w *workerNode, owner, harness, native string, age time.Duration) string {
	t.Helper()
	info := protocol.SessionInfo{Harness: harness, NativeID: native, Cwd: "/work/old", Name: "old worker",
		Status: "idle", Delivery: "native-" + harness, Mode: protocol.ModeBackground,
		Worker: &protocol.WorkerInfo{ID: "w_old", Owner: owner, Project: "shop",
			StartedAt: time.Now().Add(-age).Unix()}}
	id, err := g.gw.Store().UpsertSession(w.d.NodeID(), info)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().SetSessionOffline(w.d.NodeID(), id); err != nil {
		t.Fatal(err)
	}
	return id
}
