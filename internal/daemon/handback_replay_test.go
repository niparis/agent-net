package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// savedHandback is a worker that handed back complete with a file while its
// check ran, and a daemon that stopped while the gateway was down, as on the
// gateway machine during an upgrade: the installer restarts the gateway first.
type savedHandback struct {
	*asyncCheck
	workerID, head, gatewayDir, gatewayAddr string
}

func startSavedHandback(t *testing.T, autoContinue *bool) *savedHandback {
	t.Helper()
	a := startAsyncCheckWith(t, autoContinue, queueSet)
	workerID := a.workerID(t)
	proof := filepath.Join(a.res.Worktree, "proof.txt")
	if err := os.WriteFile(proof, []byte("proof\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := a.w.d.handback(context.Background(), a.res.SessionID,
		protocol.WorkerHandbackRequest{Report: "the report of the saved hand-back", Files: []string{proof}})
	if err != nil || !out.Checking {
		t.Fatalf("handback: %+v %v", out, err)
	}
	a.waitStarts(t, 1)
	s := &savedHandback{asyncCheck: a, workerID: workerID,
		head: gitOutT(t, a.res.Worktree, "rev-parse", "HEAD"), gatewayDir: a.g.dir, gatewayAddr: a.g.addr}
	s.gatewayDown(t)
	return s
}

func (s *savedHandback) gatewayDown(t *testing.T) {
	t.Helper()
	s.g.stop()
	eventually(t, "the daemon sees the gateway go", func() bool { return !s.w.d.Connected() })
}

// startLiveSavedHandback is a worker that still runs while its complete
// hand-back is saved: a worker with queued messages runs them before it ends,
// and the daemon may stop before its notice. The row is saved as
// handbackFlow saves it.
func startLiveSavedHandback(t *testing.T, autoContinue *bool) *savedHandback {
	t.Helper()
	a := startAsyncCheckWith(t, autoContinue, queueSet)
	wk := a.w.d.workerBySession(a.res.SessionID)
	gitIn(t, a.res.Worktree, "add", "-A")
	gitIn(t, a.res.Worktree, "commit", "-q", "-m", "work")
	s := &savedHandback{asyncCheck: a, workerID: wk.id, head: gitOutT(t, a.res.Worktree, "rev-parse", "HEAD"),
		gatewayDir: a.g.dir, gatewayAddr: a.g.addr}
	tail := &handbackTail{req: protocol.WorkerHandbackRequest{Report: "the report of the saved hand-back", Status: protocol.StatusDone},
		end: true, sessionID: a.res.SessionID, complete: true}
	a.w.d.persistHandback(wk, tail, s.head)
	if !tail.persisted {
		t.Fatal("the hand-back was not saved")
	}
	s.gatewayDown(t)
	return s
}

// savedRow reads the saved hand-back from the stopped daemon's database.
func (s *savedHandback) savedRow(t *testing.T) (pendingHandback, bool) {
	t.Helper()
	st, err := openState(s.w.node.cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	return st.pendingHandback(s.workerID)
}

// Before, the stopping daemon recorded the change and sent the notice at
// once: with the gateway down, the record failed with "not connected to the
// gateway", the notice with files was not sent, and the change was lost. Now
// the hand-back is saved before Stop returns and replayed at the next
// connect, with the interrupted check, the report and the files.
func TestHandbackSavedWhileGatewayDownIsRecordedAfterRestart(t *testing.T) {
	s := startSavedHandback(t, autoContinueOff())
	var g2 *testGateway
	s.w.node.restartWith(func() {
		p, ok := s.savedRow(t)
		if !ok || p.Head != s.head || len(p.Req.Files) != 1 || p.Files == nil || len(p.Files.Files) != 1 || !p.End {
			t.Fatalf("the hand-back is not saved when Stop returns: ok=%t %+v", ok, p)
		}
		if p.Check == nil || p.Check.Status != protocol.HandbackCheckCouldNotRun {
			t.Fatalf("saved check: %+v", p.Check)
		}
		g2 = startGateway(t, s.gatewayDir, s.gatewayAddr)
	}, true)
	s.w.cli = cliClient(t, s.w.node)

	n := s.w.waitNotice(t, 1)[0]
	t.Logf("the caller's notice after the restart (files: %+v):\n%s", n.Files, n.Body)
	if !strings.Contains(n.Body, "the report of the saved hand-back") || !strings.Contains(n.Body, "hand-back check: could not run: interrupted") {
		t.Fatalf("notice:\n%s", n.Body)
	}
	if strings.Contains(n.Body, "delivery record:") {
		t.Fatalf("the notice reports a failed delivery record:\n%s", n.Body)
	}
	if len(n.Files) != 1 || n.Files[0].Name != "proof.txt" {
		t.Fatalf("notice files: %+v", n.Files)
	}
	change := changeView(t, g2, noticeChangeID(t, n.Body))
	if change.Change.HeadCommit != s.head {
		t.Errorf("change head %s, want the hand-back head %s", change.Change.HeadCommit, s.head)
	}
	if c := change.HandbackCheck; c == nil || c.Status != protocol.HandbackCheckCouldNotRun || !strings.Contains(c.Detail, "daemon stopped") {
		t.Errorf("change hand-back check: %+v", c)
	}
	eventually(t, "the saved hand-back is removed after its notice", func() bool {
		_, ok := s.w.d.st.pendingHandback(s.workerID)
		return !ok
	})
	eventually(t, "the worker ends after its hand-back", func() bool { return s.w.d.workerBySession(s.res.SessionID) == nil })
}

// The next daemon adopts the worker before it can reach the gateway. Its
// hand-back is complete and only waits for the link, so the tick must not
// send it an automatic continue or an idle notice. Before, the adopted worker
// looked like one that stopped early, and got continue nudges.
func TestSavedHandbackWorkerGetsNoNudge(t *testing.T) {
	s := startLiveSavedHandback(t, nil)
	s.w.node.restartWith(nil, false)
	w := s.w.d.workerBySession(s.res.SessionID)
	if w == nil {
		t.Fatal("the worker was not adopted")
	}
	snap := s.w.d.SettingsSnapshot()
	s.w.d.wmu.Lock()
	continueEligible := autoContinueEligibleWith(w, snap, "shop")
	held := w.checking
	s.w.d.wmu.Unlock()
	if continueEligible {
		t.Error("the adopted worker with a saved hand-back may get an automatic continue")
	}
	if !held {
		t.Error("the adopted worker with a saved hand-back may get an idle notice or an idle end")
	}

	startGateway(t, s.gatewayDir, s.gatewayAddr)
	eventually(t, "the daemon connects", s.w.d.Connected)
	s.w.cli = cliClient(t, s.w.node)
	if n := s.w.waitNotice(t, 1)[0]; !strings.Contains(n.Body, "the report of the saved hand-back") {
		t.Fatalf("notice:\n%s", n.Body)
	}
	eventually(t, "the worker ends after its hand-back", func() bool { return s.w.d.workerBySession(s.res.SessionID) == nil })
	if got := fakeAutoContinueCount(s.res.Worktree); got != 0 {
		t.Errorf("the worker got %d automatic continues", got)
	}
}

// A worker whose notice went out while the daemon was stopping never got its
// end: the stopping daemon refuses new background work. The next daemon
// adopted it with its hand-back status, and nothing ended it. A message from
// its owner then asked to start it again, and the daemon waited for that end
// on the link's own context, holding the resume lock, until the gateway gave
// up after 75 s with "context deadline exceeded". The next daemon now ends
// such a worker when it adopts it.
func TestResumeAfterRestartOfWorkerWhoseEndNeverRan(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	owner := w.ownerID()
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:ignore", Isolation: protocol.IsolationNone})
	rec := readFake(t, repo)
	// What finishHandback leaves when the end it starts is refused.
	wk := w.d.workerBySession(res.SessionID)
	w.d.wmu.Lock()
	wk.doneStatus, wk.handbackReport = protocol.StatusDone, "done"
	w.d.saveWorkerLocked(wk)
	w.d.wmu.Unlock()

	w.node.restart()
	w.cli = cliClient(t, w.node)
	var sent protocol.MessageSent
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "one more thing"}), &sent)
	if r := w.replyTo(t, owner, sent.Message.ID); !strings.HasPrefix(r.Body, "ok from worker") {
		t.Errorf("follow-up reply: %+v", r)
	}
	if alive(rec.Pid) {
		t.Error("the old worker process still runs")
	}
	if len(w.logs.lines("worker.resumed", "session="+res.SessionID)) != 1 {
		t.Errorf("no worker.resumed line: %v", w.logs.lines("worker.resumed"))
	}
}

// A resume that cannot finish answers the gateway itself, before the
// gateway's launch wait, and lets go of the resume lock. It never waits on the
// link's context, and the link stays up.
func TestSlowResumeAnswersBeforeTheGatewayGivesUp(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, func(c *Config) { c.testResumeWait = scaled(time.Second) })
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:ignore", Isolation: protocol.IsolationNone})
	// An end that does not finish: the worker stays registered as stopping.
	wk := w.d.workerBySession(res.SessionID)
	w.d.wmu.Lock()
	wk.stopping = true
	w.d.wmu.Unlock()
	w.d.mu.Lock()
	epoch := w.d.linkEpoch
	w.d.mu.Unlock()

	_, err := w.cli.call("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "one more thing"})
	if err == nil || !strings.Contains(err.Error(), "still ending after its hand-back") {
		t.Fatalf("owner_send: %v", err)
	}
	if !w.d.resumeMu.TryLock() {
		t.Fatal("the resume lock is still held")
	}
	w.d.resumeMu.Unlock()
	w.d.mu.Lock()
	reconnected := w.d.linkEpoch != epoch
	w.d.mu.Unlock()
	if reconnected || !w.d.Connected() {
		t.Fatal("the slow resume closed the gateway link")
	}
}

// The worker process also ends while the daemon is down. Its saved hand-back
// still records the change and sends the notice, it is not reported as lost,
// and its session can be started again like any worker that handed back.
func TestSavedHandbackOfWorkerThatEndedWhileDaemonWasDown(t *testing.T) {
	s := startLiveSavedHandback(t, autoContinueOff())
	s.w.d.wmu.Lock()
	pgid := s.w.d.workers[s.workerID].pgid
	s.w.d.wmu.Unlock()
	var g2 *testGateway
	s.w.node.restartWith(func() {
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		g2 = startGateway(t, s.gatewayDir, s.gatewayAddr)
	}, true)
	s.w.cli = cliClient(t, s.w.node)

	n := s.w.waitNotice(t, 1)[0]
	if !strings.Contains(n.Body, "the report of the saved hand-back") || strings.Contains(n.Body, "lost") {
		t.Fatalf("notice:\n%s", n.Body)
	}
	if change := changeView(t, g2, noticeChangeID(t, n.Body)); change.Change.HeadCommit != s.head {
		t.Errorf("change head %s, want %s", change.Change.HeadCommit, s.head)
	}
	row, ok, err := s.w.d.st.workerBySession(s.res.SessionID)
	if err != nil || !ok || row.EndedReason != protocol.StopHandback || !row.Resumable || row.DoneStatus != protocol.StatusDone {
		t.Fatalf("worker row: ok=%t err=%v %+v", ok, err, row)
	}
	eventually(t, "the saved hand-back is removed after its notice", func() bool {
		_, ok := s.w.d.st.pendingHandback(s.workerID)
		return !ok
	})
	if got := len(s.w.notices(t)); got != 1 {
		t.Errorf("%d notices, want only the hand-back", got)
	}
}
