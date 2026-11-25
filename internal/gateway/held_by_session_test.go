package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A message Claude Code held for review is not delivered: it is held by the
// session, no reminder chases it, and a later release makes it delivered.
func TestSessionHeldMessageStopsRemindersUntilRelease(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "please rotate the key", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE sessions SET status='idle', last_active=? WHERE id=?", c.t.UnixNano(), b); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyDelivery(req.ID, protocol.StateDelivered, ""); err != nil {
		t.Fatal(err)
	}
	detail := "held by Claude Code: the sender's permission mode class does not match this session's"
	if err := s.HoldDelivery(req.ID, detail); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Message(req.ID)
	if err != nil || got.State != protocol.StateHeldBySession || !strings.Contains(got.DeliveryDetail, "held by Claude Code") {
		t.Fatalf("held message = %+v, %v", got, err)
	}

	// The recipient is idle and has not replied, but a held message is not a
	// message it can answer, so no reminder is due.
	c.advance(30 * time.Minute)
	nudges, err := s.checkOwedReplies()
	if err != nil {
		t.Fatal(err)
	}
	if len(nudges) != 0 {
		t.Fatalf("reminders for a held message: %+v", nudges)
	}

	// The owner releases it in the Claude session.
	if err := s.ApplyDelivery(req.ID, protocol.StateDelivered, "released in Claude Code"); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.Message(req.ID)
	if got.State != protocol.StateDelivered || got.DeliveryDetail != "released in Claude Code" {
		t.Fatalf("released message = %+v", got)
	}
	c.advance(30 * time.Minute)
	nudges, err = s.checkOwedReplies()
	if err != nil {
		t.Fatal(err)
	}
	if len(nudges) != 1 {
		t.Fatalf("reminders after release = %d, want 1", len(nudges))
	}
}
