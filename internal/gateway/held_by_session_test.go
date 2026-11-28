package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A message Claude Code held for review is not delivered: it is held by the
// session, no reminder chases it, and a later release makes it delivered. A
// late or repeated delivered report does not undo the hold; only the release
// does.
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

	// A repeated first delivered report is not a release.
	if err := s.ApplyDelivery(req.ID, protocol.StateDelivered, ""); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.Message(req.ID)
	if got.State != protocol.StateHeldBySession {
		t.Fatalf("a late delivered report released the message: %+v", got)
	}
	// The screens keep the held state: the repeat wrote no delivered event.
	var delivered int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='msg.delivered' AND message_id=?`, req.ID).Scan(&delivered); err != nil {
		t.Fatal(err)
	}
	if delivered != 1 {
		t.Fatalf("delivered events = %d, want only the first delivery", delivered)
	}

	// The owner releases it in the Claude session.
	if err := s.ReleaseDelivery(req.ID, "released in Claude Code"); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.Message(req.ID)
	if got.State != protocol.StateDelivered || got.DeliveryDetail != "released in Claude Code" {
		t.Fatalf("released message = %+v", got)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='msg.delivered' AND message_id=?`, req.ID).Scan(&delivered); err != nil {
		t.Fatal(err)
	}
	if delivered != 2 {
		t.Fatalf("delivered events = %d, want the delivery and the release", delivered)
	}
}

// A batch frame is one delivery for several original messages. Holding the
// carrier holds every original, a late delivered report changes none of them,
// and the release delivers the carrier and every original.
func TestHeldBatchCoversItsOriginalsAndCarrier(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	busyStatus(t, s, b, "busy")
	var originals []string
	for i := 0; i < 2; i++ {
		c.advance(time.Minute)
		m, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeRequest,
			Body: "body-" + string(rune('a'+i)), IdempotencyKey: "held-batch-" + string(rune('a'+i))})
		if err != nil || !s.HoldForBusy(m) {
			t.Fatalf("hold original %d: %v", i, err)
		}
		originals = append(originals, m.ID)
	}
	busyStatus(t, s, b, "idle")
	carrier, n, err := releaseOne(t, s, b, false)
	if err != nil || n != 2 || len(carrier.Batch) != 2 {
		t.Fatalf("release for busy: n=%d carrier=%+v err=%v", n, carrier, err)
	}
	if err := s.ApplyDelivery(carrier.ID, protocol.StateDelivered, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.HoldDelivery(carrier.ID, "held by Claude Code"); err != nil {
		t.Fatal(err)
	}
	for _, id := range append([]string{carrier.ID}, originals...) {
		got, _, err := s.Message(id)
		if err != nil || got.State != protocol.StateHeldBySession {
			t.Fatalf("%s after the batch hold = %+v, %v", id, got, err)
		}
	}

	// A repeated delivered report for the carrier is not a release.
	if err := s.ApplyDelivery(carrier.ID, protocol.StateDelivered, ""); err != nil {
		t.Fatal(err)
	}
	for _, id := range append([]string{carrier.ID}, originals...) {
		got, _, _ := s.Message(id)
		if got.State != protocol.StateHeldBySession {
			t.Fatalf("%s after a late report = %+v, want held", id, got)
		}
	}

	if err := s.ReleaseDelivery(carrier.ID, "released in Claude Code"); err != nil {
		t.Fatal(err)
	}
	for _, id := range append([]string{carrier.ID}, originals...) {
		got, _, _ := s.Message(id)
		if got.State != protocol.StateDelivered {
			t.Fatalf("%s after the release = %+v, want delivered", id, got)
		}
	}
}

// A held message reaches its normal end. If the owner never releases it, it
// expires like any other undelivered message, and the sender reads that it was
// held and may never have been seen.
func TestHeldMessageExpiresWithTheReason(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	req := heldRequest(t, s, a, b, "expires")
	c.advance(protocol.DefaultTTL + time.Minute)
	n, err := s.Expire()
	if err != nil || n != 1 {
		t.Fatalf("expired %d messages, %v, want the held one", n, err)
	}
	got, _, err := s.Message(req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != protocol.StateExpired {
		t.Fatalf("a held message that expired is %s, want expired", got.State)
	}
	for _, want := range []string{"held by Claude Code", "may never have been seen"} {
		if !strings.Contains(got.DeliveryDetail, want) {
			t.Fatalf("expired detail %q missing %q", got.DeliveryDetail, want)
		}
	}
}

// A held request that the session reads and answers closes like a delivered
// one: a reply, an ack and a handoff all move it out of the held state.
func TestHeldRequestClosesOnReplyAckAndHandoff(t *testing.T) {
	t.Run("reply", func(t *testing.T) {
		s, _ := newTestStore(t)
		a, b := twoSessions(t, s)
		req := heldRequest(t, s, a, b, "reply")
		if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Type: protocol.TypeReply,
			ParentID: req.ID, ConversationID: req.ConversationID, Body: "done"}); err != nil {
			t.Fatal(err)
		}
		if got, _, _ := s.Message(req.ID); got.State != protocol.StateReplied {
			t.Fatalf("held request after reply = %+v", got)
		}
	})
	t.Run("ack", func(t *testing.T) {
		s, _ := newTestStore(t)
		a, b := twoSessions(t, s)
		req := heldRequest(t, s, a, b, "ack")
		if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Ack: true,
			ParentID: req.ID, ConversationID: req.ConversationID}); err != nil {
			t.Fatal(err)
		}
		if got, _, _ := s.Message(req.ID); got.State != protocol.StateAcknowledged {
			t.Fatalf("held request after ack = %+v", got)
		}
	})
	t.Run("handoff", func(t *testing.T) {
		s, _ := newTestStore(t)
		a, b := twoSessions(t, s)
		req := heldRequest(t, s, a, b, "handoff")
		n3, _, err := s.CreateNode("h3", "linux")
		if err != nil {
			t.Fatal(err)
		}
		carol, err := s.UpsertSession(n3, protocol.SessionInfo{Harness: "pi", NativeID: "nc", Name: "carol"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.StoreHandoff(protocol.MsgHandoff{Sender: b, MessageID: req.ID, Recipient: carol}); err != nil {
			t.Fatal(err)
		}
		if got, _, _ := s.Message(req.ID); got.State != protocol.StateReplied {
			t.Fatalf("held request after handoff = %+v", got)
		}
	})
}

// heldRequest stores a request to b, delivers it and reports it held.
func heldRequest(t *testing.T, s *Store, a, b, key string) protocol.Message {
	t.Helper()
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeRequest,
		Body: "please handle " + key, IdempotencyKey: key})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyDelivery(req.ID, protocol.StateDelivered, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.HoldDelivery(req.ID, "held by Claude Code"); err != nil {
		t.Fatal(err)
	}
	return req
}
