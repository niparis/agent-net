package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
)

// endedWorkerForResume starts a worker that hands back, waits until it has
// ended and is resumable, and returns it. The gateway answers a send after
// resumeGrace at the latest.
func endedWorkerForResume(t *testing.T, resumeGrace, resumeWait time.Duration) (*testGateway, *workerNode, protocol.WorkerStartResult) {
	t.Helper()
	g := startGatewayWith(t, t.TempDir(), "", 0, func(o *gateway.Options) { o.ResumeAnswerWait = resumeGrace })
	w := startWorkerNode(t, g, shortSet, func(c *Config) { c.testResumeWait = resumeWait })
	repo := newRepo(t, "shop")
	w.register(t, repo)
	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:handback first", Isolation: protocol.IsolationNone})
	w.waitNotice(t, 1)
	eventually(t, "the worker has ended and can be resumed", func() bool {
		rows, _ := w.d.st.workers()
		return w.d.workerCount() == 0 && len(rows) == 1 && rows[0].Resumable && rows[0].Reported
	})
	return g, w, res
}

// A send to an ended worker whose resume is slow answers within the grace, not
// after the resume. On 2026-11-19 such a send took 33 s, so the caller's tool
// call timed out although the message was sent. The message is stored once and
// is delivered when the worker is up.
func TestSendToEndedWorkerAnswersBeforeASlowResumeFinishes(t *testing.T) {
	_, w, res := endedWorkerForResume(t, 200*time.Millisecond, scaled(30*time.Second))
	owner := w.ownerID()
	// A resume that waits on the resume lock stands for a harness start that
	// blocks.
	w.d.resumeMu.Lock()
	released := false
	release := func() {
		if !released {
			released = true
			w.d.resumeMu.Unlock()
		}
	}
	defer release()

	began := time.Now()
	var sent protocol.MessageSent
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "one more thing"}), &sent)
	if took := time.Since(began); took > scaled(5*time.Second) {
		t.Fatalf("the send took %s while the resume was blocked", took)
	}
	if sent.Message.ID == "" {
		t.Fatalf("no message in the answer: %+v", sent)
	}
	if w.d.st.isDelivered(sent.Message.ID) {
		t.Fatal("the message was delivered before the worker was up")
	}

	release()
	if r := w.replyTo(t, owner, sent.Message.ID); !strings.HasPrefix(r.Body, "ok from worker") {
		t.Fatalf("reply: %+v", r)
	}
	count := 0
	for _, m := range w.messagesOf(t, owner) {
		if m.From == owner && m.Body == "one more thing" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("the message was stored %d times", count)
	}
	if n := len(w.logs.lines("worker.resumed", "session="+res.SessionID)); n != 1 {
		t.Fatalf("%d worker.resumed lines: %v", n, w.logs.lines("worker.resumed"))
	}
}

// When a slow resume finally fails, the message that was already accepted is
// taken back and the sender gets a notice, so nothing is left waiting for a
// worker that will not come.
func TestSlowResumeThatFailsWithdrawsTheMessageAndNotifiesTheSender(t *testing.T) {
	g, w, res := endedWorkerForResume(t, 100*time.Millisecond, scaled(time.Second))
	owner := w.ownerID()
	w.d.resumeMu.Lock()
	defer w.d.resumeMu.Unlock()

	var sent protocol.MessageSent
	mustJSON(t, w.cli.mustCall("owner_send", protocol.OwnerSendRequest{To: res.SessionID, Body: "one more thing"}), &sent)
	eventually(t, "the notice that the restart failed", func() bool {
		for _, m := range w.messagesOf(t, owner) {
			if m.To == owner && strings.Contains(m.Body, "another resume") && strings.Contains(m.Body, "one more thing") {
				return true
			}
		}
		return false
	})
	eventually(t, "the message is withdrawn", func() bool {
		m, ok, err := g.gw.Store().Message(sent.Message.ID)
		return err == nil && ok && m.State == protocol.StateWithdrawn
	})
}
