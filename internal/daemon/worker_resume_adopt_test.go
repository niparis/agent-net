package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
)

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
	// The send waits for the resume here, to see the daemon's own answer.
	g := startGatewayWith(t, t.TempDir(), "", 0, func(o *gateway.Options) { o.ResumeAnswerWait = scaled(30 * time.Second) })
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
	// Either the resume that waited for the end gave up, or one that queued
	// behind it gave up on the lock: both answer within the wait.
	if err == nil || (!strings.Contains(err.Error(), "still ending after its hand-back") && !strings.Contains(err.Error(), "another resume")) {
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

// A resume that waits for the resume lock answers the gateway itself within
// its own wait. On 2026-11-12 three resumes queued behind one another, each
// holding the lock for the full wait, so every one of them ran past the
// gateway's 75 s launch wait and the gateway only saw "context deadline
// exceeded".
func TestResumeWaitingForTheResumeLockAnswersWithinItsWait(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, func(c *Config) { c.testResumeWait = scaled(300 * time.Millisecond) })
	w.d.resumeMu.Lock()
	defer w.d.resumeMu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, err := w.d.resumeWorkerSession(context.Background(), "s_any", "s_owner")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "another resume") {
			t.Fatalf("resume: %v", err)
		}
	case <-time.After(scaled(2 * time.Second)):
		t.Fatal("the resume kept waiting for the resume lock past its own wait")
	}
}

// A worker that cleanup is working on is not started again: the worktree and
// the branch are going away. The resume says so and the owner sends again.
func TestResumeRefusedWhileCleanupWorksOnTheWorker(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:ignore", Isolation: protocol.IsolationNone})
	wk := w.d.workerBySession(res.SessionID)
	w.d.wmu.Lock()
	id := wk.id
	wk.doneStatus, wk.handbackReport = protocol.StatusDone, "done"
	w.d.saveWorkerLocked(wk)
	w.d.wmu.Unlock()
	w.node.restart()
	w.cli = cliClient(t, w.node)
	w.d.wmu.Lock()
	w.d.cleaningWorkers = map[string]bool{id: true}
	w.d.wmu.Unlock()
	_, err := w.cli.call("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "one more thing"})
	if err == nil || !strings.Contains(err.Error(), "being cleaned up") {
		t.Fatalf("owner_send: %v", err)
	}
}
