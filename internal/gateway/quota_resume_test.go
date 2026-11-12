package gateway

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// quotaResumeSetup makes an owner session and an ended Codex worker it owns.
func quotaResumeSetup(t *testing.T) (g *Gateway, node, owner, worker string, received chan []byte, c *clock) {
	t.Helper()
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	c = &clock{t: time.Date(2026, 11, 12, 20, 0, 0, 0, time.Local)}
	g.store.now = c.now
	node, _, err = g.store.CreateNode("host1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	_, received = connectTestNode(t, g, node)
	owner, err = g.store.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	worker, err = g.store.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "worker1", Model: "gpt-5.5",
		Worker: &protocol.WorkerInfo{ID: "w_1", Owner: owner, RunGeneration: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.store.SetWorkerSessionEnded(node, worker, 1); err != nil {
		t.Fatal(err)
	}
	return g, node, owner, worker, received, c
}

func quotaResumeSend(t *testing.T, g *Gateway, node, owner, worker string) error {
	t.Helper()
	raw, _ := json.Marshal(protocol.MsgSend{Sender: owner, Recipient: worker, Body: "please fix the review findings", IdempotencyKey: "k1"})
	g.mu.Lock()
	nc := g.conns[node]
	g.mu.Unlock()
	_, err := g.handle(t.Context(), nc, "msg.send", raw)
	return err
}

// A message to an ended worker whose provider reported its usage limit does
// not start the worker. The sender is told, at once, until when and how to
// continue on another model. The message is not held.
func TestMessageToQuotaFailedWorkerDoesNotRestartIt(t *testing.T) {
	g, node, owner, worker, received, _ := quotaResumeSetup(t)
	// The run that ended the worker reported the limit.
	raw, _ := json.Marshal(protocol.WorkerStopped{SessionID: worker, WorkerID: "w_1", Project: "alpha", Harness: "codex", Model: "gpt-5.5",
		Reason: protocol.StopRunFailed, Failure: "You've hit your usage limit. Upgrade to Pro, visit https://chatgpt.com/codex/settings/usage to purchase more credits or try again at 3:39 AM."})
	g.mu.Lock()
	nc := g.conns[node]
	g.mu.Unlock()
	if _, err := g.handleWorker(t.Context(), nc, "worker.stopped", raw); err != nil {
		t.Fatal(err)
	}
	for len(received) > 0 {
		<-received
	}

	err := quotaResumeSend(t, g, node, owner, worker)
	if err == nil {
		t.Fatal("the message to a quota-failed worker was accepted")
	}
	for _, want := range []string{"codex quota used up until 2026-11-13", "agent_worker_start", "base"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q lacks %q", err, want)
		}
	}
	select {
	case frame := <-received:
		var h protocol.Header
		if json.Unmarshal(frame, &h) == nil && h.Type == "worker.resume" {
			t.Fatal("the worker was started again")
		}
	case <-time.After(50 * time.Millisecond):
	}
	// The sender also gets a notice that carries the refusal and the message.
	msgs, err := g.store.Messages(node, protocol.QueryMessages{SessionID: owner})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range msgs {
		if m.From == worker && strings.Contains(m.Body, "quota used up until") && strings.Contains(m.Body, "please fix the review findings") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no notice to the sender: %+v", msgs)
	}
}

// With no quota mark, a message still starts the ended worker.
func TestMessageToWorkerWithoutQuotaMarkStillRestartsIt(t *testing.T) {
	g, node, owner, worker, received, _ := quotaResumeSetup(t)
	for len(received) > 0 {
		<-received
	}
	if err := quotaResumeSend(t, g, node, owner, worker); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case frame := <-received:
		var h protocol.Header
		if err := json.Unmarshal(frame, &h); err != nil || h.Type != "worker.resume" {
			t.Fatalf("unexpected frame: %s", frame)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected worker.resume frame, none received")
	}
}

// After the reset the mark is over and the worker starts again.
func TestMessageToQuotaFailedWorkerRestartsAfterReset(t *testing.T) {
	g, node, owner, worker, received, c := quotaResumeSetup(t)
	if err := g.store.RecordQuotaBlock(node, protocol.QuotaBlock{Provider: "codex", Until: c.t.Add(7 * time.Hour)}, "usage limit"); err != nil {
		t.Fatal(err)
	}
	if err := quotaResumeSend(t, g, node, owner, worker); err == nil {
		t.Fatal("accepted before the reset")
	}
	c.t = c.t.Add(7*time.Hour + time.Minute)
	for len(received) > 0 {
		<-received
	}
	raw, _ := json.Marshal(protocol.MsgSend{Sender: owner, Recipient: worker, Body: "again", IdempotencyKey: "k2"})
	g.mu.Lock()
	nc := g.conns[node]
	g.mu.Unlock()
	if _, err := g.handle(t.Context(), nc, "msg.send", raw); err != nil {
		t.Fatalf("send after the reset: %v", err)
	}
}
