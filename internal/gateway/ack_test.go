package gateway

import (
	"context"
	"strings"
	"testing"
	"time"

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
