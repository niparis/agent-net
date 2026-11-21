package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// withdrawAsk takes back the held start's ask as the caller does, with
// agent_ask_sort, and returns the result or the error.
func withdrawAsk(pi *localClient, ask string) (protocol.AskSortResult, error) {
	var out protocol.AskSortResult
	raw, err := pi.call("ask_sort", protocol.AskSortArgs{Ask: ask, Result: protocol.AskSortWithdraw, Reason: "not needed any more"})
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}

func runningWorkers(w *workerNode) int {
	w.d.wmu.Lock()
	defer w.d.wmu.Unlock()
	return len(w.d.workers)
}

// A start that was confirmed but is still in the judge is cancelled by the
// caller's withdraw. No worker starts and no notice says "not started" after
// the withdraw told the caller.
func TestWithdrawAfterConfirmCancelsAStartThatHasNotRun(t *testing.T) {
	var judged atomic.Int32
	release := make(chan struct{})
	g, w, pi, piID := overrideNodeWith(t, shortSet, blockedJudge(&judged, release))
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	res := startFrom(t, pi, overrideRequest())
	ask := waitOverrideAsk(t, g)
	w.cli.mustCall("inbox_answer", map[string]string{"id": ask.ID, "answer": protocol.StartAnswerConfirm})
	eventually(t, "the confirmed start reaches the judge", func() bool { return judged.Load() > 0 })
	out, err := withdrawAsk(pi, ask.ID)
	if err != nil {
		t.Fatalf("withdraw after confirm: %v", err)
	}
	if !strings.Contains(out.Note, "withdrawn; the start was cancelled before it ran") {
		t.Fatalf("withdraw note %q", out.Note)
	}
	close(release)
	// The judge ends with the cancel; give the daemon time to settle, then
	// check that nothing started and the caller got no stray notice.
	eventually(t, "the start settles", func() bool {
		w.d.acceptedMu.Lock()
		defer w.d.acceptedMu.Unlock()
		e := w.d.accepted[res.StartID]
		return e != nil && e.Settled
	})
	if n := runningWorkers(w); n != 0 {
		t.Fatalf("%d workers run after the withdraw", n)
	}
	if n, _ := w.d.st.queueLen(); n != 0 {
		t.Fatalf("queue length %d after the withdraw", n)
	}
	for _, m := range w.messagesOf(t, piID) {
		if strings.Contains(m.Body, "start: "+res.StartID) {
			t.Fatalf("a notice for the withdrawn start:\n%s", m.Body)
		}
	}
}

// A confirmed start that waits for room in the queue is taken out of the queue.
func TestWithdrawAfterConfirmCancelsAQueuedStart(t *testing.T) {
	fl := &fakeLoad{l: cpuBusy()}
	g, w, pi, piID := overrideNodeWith(t, queueSet, withLoad(fl))
	res := startFrom(t, pi, overrideRequest())
	ask := waitOverrideAsk(t, g)
	w.cli.mustCall("inbox_answer", map[string]string{"id": ask.ID, "answer": protocol.StartAnswerConfirm})
	eventually(t, "the queued notice", func() bool { return startNoticeCount(w, t, piID, "queued", res.StartID) == 1 })
	if n, _ := w.d.st.queueLen(); n != 1 {
		t.Fatalf("queue length %d before the withdraw", n)
	}
	out, err := withdrawAsk(pi, ask.ID)
	if err != nil {
		t.Fatalf("withdraw of a queued start: %v", err)
	}
	if !strings.Contains(out.Note, "withdrawn; the start was cancelled before it ran") {
		t.Fatalf("withdraw note %q", out.Note)
	}
	if n, _ := w.d.st.queueLen(); n != 0 {
		t.Fatalf("queue length %d after the withdraw", n)
	}
	fl.set(roomyLoad())
	// The queue is empty, so no worker can begin even when room comes back.
	if n := runningWorkers(w); n != 0 {
		t.Fatalf("%d workers run after the withdraw", n)
	}
}

// When the worker has begun, the withdraw says so and names the worker. It
// does not stop it.
func TestWithdrawAfterTheWorkerStartedNamesIt(t *testing.T) {
	g, w, pi, piID := overrideNode(t)
	res := startFrom(t, pi, overrideRequest())
	ask := waitOverrideAsk(t, g)
	w.cli.mustCall("inbox_answer", map[string]string{"id": ask.ID, "answer": protocol.StartAnswerConfirm})
	noticeFor(t, w, piID, res.StartID)
	workers := w.workers(t)
	if len(workers) != 1 {
		t.Fatalf("want one running worker, got %d", len(workers))
	}
	out, err := withdrawAsk(pi, ask.ID)
	if err != nil {
		t.Fatalf("withdraw after the worker started: %v", err)
	}
	want := "already started as " + workers[0].SessionID + "; stop it with agent_worker_stop"
	if !strings.Contains(out.Note, want) {
		t.Fatalf("withdraw note %q lacks %q", out.Note, want)
	}
	if n := runningWorkers(w); n != 1 {
		t.Fatalf("the withdraw stopped the worker: %d run", n)
	}
}

// A held start that is withdrawn before any answer is dropped. A confirm that
// arrives after is refused with a reason, and a decision frame that was
// already in flight starts nothing.
func TestConfirmAfterWithdrawIsRefused(t *testing.T) {
	g, w, pi, piID := overrideNode(t)
	res := startFrom(t, pi, overrideRequest())
	ask := waitOverrideAsk(t, g)
	out, err := withdrawAsk(pi, ask.ID)
	if err != nil {
		t.Fatalf("withdraw of a held start: %v", err)
	}
	if !strings.Contains(out.Note, "withdrawn; the start was cancelled before it ran") {
		t.Fatalf("withdraw note %q", out.Note)
	}
	w.d.heldMu.Lock()
	_, held := w.d.heldStarts[res.StartID]
	w.d.heldMu.Unlock()
	if held {
		t.Fatal("the withdrawn start is still held")
	}
	_, err = w.cli.call("inbox_answer", map[string]string{"id": ask.ID, "answer": protocol.StartAnswerConfirm})
	if err == nil || !strings.Contains(err.Error(), "withdrawn by its caller") {
		t.Fatalf("confirm after withdraw: %v", err)
	}
	// A confirm that was already on its way when the caller withdrew.
	w.d.onStartDecision(protocol.StartDecision{Ref: protocol.StartOverrideRefPrefix + res.StartID, Session: piID, Answer: protocol.StartAnswerConfirm})
	// A launch is always recorded before it begins, so no record means none ran.
	w.d.acceptedMu.Lock()
	_, launched := w.d.accepted[res.StartID]
	w.d.acceptedMu.Unlock()
	if launched || runningWorkers(w) != 0 {
		t.Fatalf("a late confirm launched the withdrawn start (recorded %v, %d workers)", launched, runningWorkers(w))
	}
	for _, m := range w.messagesOf(t, piID) {
		if strings.Contains(m.Body, "start: "+res.StartID) {
			t.Fatalf("a notice for the withdrawn start:\n%s", m.Body)
		}
	}
}

// bareStartDaemon is a daemon with only the state a withdraw reads, so that the
// withdraw rules run without a gateway.
func bareStartDaemon(t *testing.T) *Daemon {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &Daemon{ctoCtx: ctx}
}

// The withdraw of a start that was confirmed and is in the judge cancels the
// judge, waits for the start to end, and says the start did not run.
func TestWithdrawNoteCancelsAConfirmedStartInTheJudge(t *testing.T) {
	d := bareStartDaemon(t)
	ctx := d.noteConfirmedStart("a_1")
	ended := make(chan bool, 1)
	go func() {
		<-ctx.Done() // the judge ends with the cancel, and the start with it
		ended <- d.settleConfirmedStart("a_1", nil, errStartWithdrawn)
	}()
	if note := d.withdrawnStartNote(context.Background(), "s_caller", "a_1"); note != "withdrawn; the start was cancelled before it ran" {
		t.Fatalf("note %q", note)
	}
	if !<-ended {
		t.Fatal("the start did not learn that its caller withdrew it")
	}
}

// A withdraw of a worker that already runs says so and names the worker.
func TestWithdrawNoteNamesAWorkerThatStarted(t *testing.T) {
	d := bareStartDaemon(t)
	d.noteConfirmedStart("a_1")
	d.settleConfirmedStart("a_1", &protocol.WorkerStartResult{Status: "started", SessionID: "s_worker"}, nil)
	if note := d.withdrawnStartNote(context.Background(), "s_caller", "a_1"); note != "already started as s_worker; stop it with agent_worker_stop" {
		t.Fatalf("note %q", note)
	}
}

// A confirmed start that failed on its own is not called cancelled.
func TestWithdrawNoteOfAStartThatFailed(t *testing.T) {
	d := bareStartDaemon(t)
	d.noteConfirmedStart("a_1")
	d.settleConfirmedStart("a_1", nil, errors.New("no allowed provider"))
	note := d.withdrawnStartNote(context.Background(), "s_caller", "a_1")
	if !strings.Contains(note, "had already failed") {
		t.Fatalf("note %q", note)
	}
}

// A held start is dropped by the withdraw, and the answer that was already on
// its way starts nothing and sends no notice.
func TestWithdrawNoteDropsAHeldStartAndAnswerIsIgnored(t *testing.T) {
	d := bareStartDaemon(t)
	d.heldStarts = map[string]*heldStart{"a_1": {id: "a_1", caller: "s_caller"}}
	if note := d.withdrawnStartNote(context.Background(), "s_caller", "a_1"); note != "withdrawn; the start was cancelled before it ran" {
		t.Fatalf("note %q", note)
	}
	if len(d.heldStarts) != 0 {
		t.Fatal("the withdrawn start is still held")
	}
	d.onStartDecision(protocol.StartDecision{Ref: protocol.StartOverrideRefPrefix + "a_1", Session: "s_caller", Answer: protocol.StartAnswerConfirm})
	if _, launched := d.accepted["a_1"]; launched {
		t.Fatal("a late confirm launched the withdrawn start")
	}
	if _, left := d.withdrawnStarts["a_1"]; left {
		t.Fatal("the withdrawal is still remembered after the late answer")
	}
}

// A start this daemon does not know is said so, not reported as cancelled.
func TestWithdrawNoteOfAnUnknownStart(t *testing.T) {
	d := bareStartDaemon(t)
	if note := d.withdrawnStartNote(context.Background(), "s_caller", "a_x"); !strings.Contains(note, "does not track the start") {
		t.Fatalf("note %q", note)
	}
}
