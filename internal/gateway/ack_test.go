package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/niparis/agent-net/internal/protocol"
)

// An ack closes a request with no body and no delivery, and the request then
// shows as acknowledged.
func TestAckClosesRequestWithoutDelivery(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeRequest, Body: "status report"})
	if err != nil {
		t.Fatal(err)
	}
	ack, created, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: req.ID, Ack: true})
	if err != nil || !created {
		t.Fatalf("ack: created=%v err=%v", created, err)
	}
	if ack.Type != protocol.TypeAck || ack.State != protocol.StateAcknowledged || ack.ParentID != req.ID || ack.Body != "" {
		t.Fatalf("ack message = %+v", ack)
	}
	got, _, err := s.Message(req.ID)
	if err != nil || got.State != protocol.StateAcknowledged {
		t.Fatalf("request state = %q, %v; want %q", got.State, err, protocol.StateAcknowledged)
	}
	// Nothing is queued for delivery to the sender.
	for _, node := range []string{mustNode(t, s, a), mustNode(t, s, b)} {
		pending, err := s.Pending(node, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range pending {
			if m.ID == ack.ID {
				t.Fatalf("ack is still queued on %s", node)
			}
		}
	}
	// The TUI's wait/owes view no longer lists the request for either side.
	for _, id := range []string{a, b} {
		view, ok, err := s.Agent(id)
		if err != nil || !ok {
			t.Fatalf("agent %s: %v", id, err)
		}
		agents := []protocol.AgentView{view}
		if err := s.applyPending(agents); err != nil {
			t.Fatal(err)
		}
		if len(agents[0].Pending) != 0 {
			t.Fatalf("%s still owes or waits: %+v", id, agents[0].Pending)
		}
	}
}

// An ack answers a request, so the reminder loop sends nothing for it.
func TestAckStopsTheReminder(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeRequest, Body: "status report"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE sessions SET status='idle', last_active=? WHERE id=?", c.t.UnixNano(), b); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(3 * time.Minute)
	if _, _, err = s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: req.ID, Ack: true}); err != nil {
		t.Fatal(err)
	}
	reminders, err := s.remindPending()
	if err != nil || len(reminders) != 0 {
		t.Fatalf("reminders after ack: %+v %v", reminders, err)
	}
}

// An ack after a reminder counts as answered after the reminder.
func TestAckAfterReminderCountsAnswered(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeRequest, Body: "status report"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE sessions SET status='idle', last_active=? WHERE id=?", c.t.UnixNano(), b); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(3 * time.Minute)
	reminders, err := s.remindPending()
	if err != nil || len(reminders) != 1 {
		t.Fatalf("reminders: %+v %v", reminders, err)
	}
	c.t = c.t.Add(5 * time.Minute)
	if _, _, err = s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: req.ID, Ack: true}); err != nil {
		t.Fatal(err)
	}
	counts, err := s.SessionCounts(24 * time.Hour)
	if err != nil || counts.PendingAnsweredAfterReminder != 1 {
		t.Fatalf("counts = %+v, %v", counts, err)
	}
}

// ack cannot carry an outcome.
func TestAckRefusedWithOutcome(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeRequest, Body: "status report"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: req.ID, Ack: true,
		ReplyOutcome: protocol.ReplyOutcome{Outcome: "accept"}})
	if err == nil || !strings.Contains(err.Error(), "ack") {
		t.Fatalf("ack with outcome: %v", err)
	}
}

// ack needs a request to close.
func TestAckNeedsARequest(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Ack: true}); err == nil {
		t.Fatal("ack without a parent was accepted")
	}
	note, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeNotification, Body: "fyi"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: note.ID, Ack: true}); err == nil {
		t.Fatal("ack of a notification was accepted")
	}
}

// The ack row is never delivered and never wakes the sender.
func TestAckDoesNotWakeTheSender(t *testing.T) {
	p := newWakeProbe(t)
	sender := p.offline(t, "claude", protocol.ModeForeground, protocol.AutonomyOff)
	req, _, err := p.s.StoreMessage(protocol.MsgSend{Sender: sender, Recipient: p.from, Type: protocol.TypeRequest, Body: "status report"})
	if err != nil {
		t.Fatal(err)
	}
	ack, _, err := p.s.StoreMessage(protocol.MsgSend{Sender: p.from, Recipient: sender, ParentID: req.ID, Ack: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.g.dispatch(context.Background(), ack) {
		t.Fatal("dispatch reported a wake for an ack")
	}
	p.expectNoWake(t)
}

func connectTestNode(t *testing.T, g *Gateway, nodeID string) (*websocket.Conn, chan []byte) {
	t.Helper()
	ctx := t.Context()
	accepted := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		accepted <- ws
	}))
	t.Cleanup(server.Close)

	client, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(websocket.StatusNormalClosure, "") })

	ws := <-accepted
	cancelCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	nc := &nodeConn{id: nodeID, ws: ws, dead: make(chan struct{}), cancel: cancel}
	g.mu.Lock()
	g.conns[nodeID] = nc
	g.mu.Unlock()

	received := make(chan []byte, 100)
	go func() {
		for {
			_, raw, e := client.Read(cancelCtx)
			if e != nil {
				return
			}
			received <- raw
			var h protocol.Header
			if json.Unmarshal(raw, &h) == nil && h.Type == "worker.resume" {
				res, _ := protocol.EncodeResult(h.ID, protocol.WorkerResumeResult{}, nil)
				_ = client.Write(cancelCtx, websocket.MessageText, res)
			}
		}
	}()
	go func() {
		for {
			_, raw, e := ws.Read(cancelCtx)
			if e != nil {
				return
			}
			var h protocol.Header
			if json.Unmarshal(raw, &h) == nil && h.Type == "result" {
				nc.answer(h)
			}
		}
	}()
	return client, received
}

// An ack to a request whose sender is an ended worker leaves the worker ended:
// no resume, and no delivery frame to it.
func TestAckToEndedWorkerLeavesWorkerEndedNoResume(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })

	node, _, err := g.store.CreateNode("host1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	_, received := connectTestNode(t, g, node)

	ownerID, err := g.store.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	workerID, err := g.store.UpsertSession(node, protocol.SessionInfo{
		Harness:  "opencode",
		NativeID: "worker1",
		Worker:   &protocol.WorkerInfo{ID: "w_1", Owner: ownerID, RunGeneration: 1},
	})
	if err != nil {
		t.Fatal(err)
	}

	req, _, err := g.store.StoreMessage(protocol.MsgSend{
		Sender:    workerID,
		Recipient: ownerID,
		Type:      protocol.TypeRequest,
		Body:      "need guidance",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := g.store.SetWorkerSessionEnded(node, workerID, 1); err != nil {
		t.Fatal(err)
	}

	a, ok, err := g.store.Agent(workerID)
	if err != nil || !ok || a.Online || a.Status != "ended" {
		t.Fatalf("worker before ack: online=%v status=%q ok=%v err=%v", a.Online, a.Status, ok, err)
	}

	// Drain any messages received during setup.
	for len(received) > 0 {
		<-received
	}

	raw, _ := json.Marshal(protocol.MsgSend{
		Sender:    ownerID,
		Recipient: workerID,
		ParentID:  req.ID,
		Ack:       true,
	})
	g.mu.Lock()
	nc := g.conns[node]
	g.mu.Unlock()

	res, err := g.handle(t.Context(), nc, "msg.send", raw)
	if err != nil {
		t.Fatalf("msg.send ack: %v", err)
	}
	m := res.(map[string]any)["message"].(protocol.Message)
	if m.State != protocol.StateAcknowledged {
		t.Fatalf("ack state = %q; want %q", m.State, protocol.StateAcknowledged)
	}

	// Worker must remain ended and inactive.
	a, ok, err = g.store.Agent(workerID)
	if err != nil || !ok || a.Online || a.Status != "ended" {
		t.Fatalf("worker after ack: online=%v status=%q", a.Online, a.Status)
	}
	var active int
	if err := g.store.db.QueryRow("SELECT worker_run_active FROM sessions WHERE id=?", workerID).Scan(&active); err != nil || active != 0 {
		t.Fatalf("worker_run_active = %d, want 0 (err=%v)", active, err)
	}

	// No worker.resume or delivery frame was sent.
	select {
	case frame := <-received:
		t.Fatalf("unexpected frame sent to node: %s", string(frame))
	case <-time.After(50 * time.Millisecond):
	}
}

// An ack closes the request: no reply reminder later.
func TestAckToEndedWorkerClosesRequestNoReminder(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("host1", "linux")
	ownerID, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "owner", Status: "idle"})
	workerID, _ := s.UpsertSession(node, protocol.SessionInfo{
		Harness:  "opencode",
		NativeID: "worker1",
		Worker:   &protocol.WorkerInfo{ID: "w_1", Owner: ownerID, RunGeneration: 1},
	})

	req, _, err := s.StoreMessage(protocol.MsgSend{
		Sender:    workerID,
		Recipient: ownerID,
		Type:      protocol.TypeRequest,
		Body:      "need guidance",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.SetWorkerSessionEnded(node, workerID, 1); err != nil {
		t.Fatal(err)
	}

	if _, err := s.db.Exec("UPDATE sessions SET status='idle', last_active=? WHERE id=?", c.t.UnixNano(), ownerID); err != nil {
		t.Fatal(err)
	}

	c.t = c.t.Add(3 * time.Minute)

	if _, _, err := s.StoreMessage(protocol.MsgSend{
		Sender:    ownerID,
		Recipient: workerID,
		ParentID:  req.ID,
		Ack:       true,
	}); err != nil {
		t.Fatalf("store ack: %v", err)
	}

	got, _, err := s.Message(req.ID)
	if err != nil || got.State != protocol.StateAcknowledged {
		t.Fatalf("request state = %q; want %q", got.State, protocol.StateAcknowledged)
	}

	reminders, err := s.remindPending()
	if err != nil || len(reminders) != 0 {
		t.Fatalf("reminders after ack: %+v, %v", reminders, err)
	}

	// Batch message carrier attributed to ended worker also closed on ack.
	carrier, _, err := s.StoreMessage(protocol.MsgSend{
		Sender:    workerID,
		Recipient: ownerID,
		Type:      protocol.TypeRequest,
		Body:      "batch carrier",
	})
	if err != nil {
		t.Fatal(err)
	}
	child, _, err := s.StoreMessage(protocol.MsgSend{
		Sender:    workerID,
		Recipient: ownerID,
		Type:      protocol.TypeRequest,
		Body:      "child request",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE messages SET batch_id=? WHERE id=?", carrier.ID, child.ID); err != nil {
		t.Fatal(err)
	}

	c.t = c.t.Add(3 * time.Minute)
	if _, _, err := s.StoreMessage(protocol.MsgSend{
		Sender:    ownerID,
		Recipient: workerID,
		ParentID:  carrier.ID,
		Ack:       true,
	}); err != nil {
		t.Fatalf("store batch carrier ack: %v", err)
	}

	childGot, _, err := s.Message(child.ID)
	if err != nil || childGot.State != protocol.StateAcknowledged {
		t.Fatalf("child request state = %q; want %q", childGot.State, protocol.StateAcknowledged)
	}

	reminders, err = s.remindPending()
	if err != nil || len(reminders) != 0 {
		t.Fatalf("reminders after batch ack: %+v, %v", reminders, err)
	}
}

// A real reply with a body to an ended worker still resumes it, as today.
func TestRealReplyToEndedWorkerResumesWorker(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })

	node, _, err := g.store.CreateNode("host1", "linux")
	if err != nil {
		t.Fatal(err)
	}
	_, received := connectTestNode(t, g, node)

	ownerID, err := g.store.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	workerID, err := g.store.UpsertSession(node, protocol.SessionInfo{
		Harness:  "opencode",
		NativeID: "worker1",
		Worker:   &protocol.WorkerInfo{ID: "w_1", Owner: ownerID, RunGeneration: 1},
	})
	if err != nil {
		t.Fatal(err)
	}

	req, _, err := g.store.StoreMessage(protocol.MsgSend{
		Sender:    workerID,
		Recipient: ownerID,
		Type:      protocol.TypeRequest,
		Body:      "need guidance",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := g.store.SetWorkerSessionEnded(node, workerID, 1); err != nil {
		t.Fatal(err)
	}

	// Drain any messages received during setup.
	for len(received) > 0 {
		<-received
	}

	raw, _ := json.Marshal(protocol.MsgSend{
		Sender:    ownerID,
		Recipient: workerID,
		ParentID:  req.ID,
		Body:      "here is the guidance to continue",
	})
	g.mu.Lock()
	nc := g.conns[node]
	g.mu.Unlock()

	res, err := g.handle(t.Context(), nc, "msg.send", raw)
	if err != nil {
		t.Fatalf("msg.send reply: %v", err)
	}
	m := res.(map[string]any)["message"].(protocol.Message)
	if m.State != protocol.StateStored {
		t.Fatalf("reply state = %q; want %q", m.State, protocol.StateStored)
	}

	// Worker must have been resumed.
	select {
	case frame := <-received:
		var h protocol.Header
		if err := json.Unmarshal(frame, &h); err != nil || h.Type != "worker.resume" {
			t.Fatalf("unexpected frame: %s", string(frame))
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected worker.resume frame, none received")
	}

	var active int
	if err := g.store.db.QueryRow("SELECT worker_run_active FROM sessions WHERE id=?", workerID).Scan(&active); err != nil || active != 1 {
		t.Fatalf("worker_run_active = %d, want 1 (err=%v)", active, err)
	}
}
