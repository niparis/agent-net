package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func ctoSession(t *testing.T, s *Store, node string) string {
	t.Helper()
	id, _, err := s.upsertSession(s.db, node, protocol.SessionInfo{Harness: "cto", NativeID: "cto", Name: "CTO"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES('cto_session',?,0)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestWithdrawBySender(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	m, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeNotification,
		Body: "stale order", IdempotencyKey: "stale"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Withdraw(m.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != protocol.StateWithdrawn {
		t.Fatalf("state = %q, want withdrawn", got.State)
	}
	if stored, _, _ := s.Message(m.ID); stored.State != protocol.StateWithdrawn {
		t.Fatalf("status reads %q, want withdrawn", stored.State)
	}
	if pending, _ := s.Pending(mustNode(t, s, b), false); len(pending) != 0 {
		t.Fatalf("a withdrawn message is still pending: %+v", pending)
	}
	if _, err := s.Withdraw(m.ID, a); err == nil || !strings.Contains(err.Error(), "already withdrawn") {
		t.Fatalf("second withdraw: %v", err)
	}
}

func TestWithdrawAnotherSenderIsRefused(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	third, err := s.UpsertSession(mustNode(t, s, b), protocol.SessionInfo{Harness: "pi", NativeID: "nc", Name: "carol"})
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeNotification,
		Body: "not yours", IdempotencyKey: "not-yours"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Withdraw(m.ID, third); err == nil || !strings.Contains(err.Error(), "own message") {
		t.Fatalf("another sender withdrew it: %v", err)
	}
	if stored, _, _ := s.Message(m.ID); stored.State != protocol.StateStored {
		t.Fatalf("the message changed to %q", stored.State)
	}
}

func TestWithdrawAfterDeliveryIsRefused(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	for _, state := range []string{protocol.StateDelivered, protocol.StateReplied, protocol.StateAcknowledged} {
		t.Run(state, func(t *testing.T) {
			m, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeNotification,
				Body: "gone", IdempotencyKey: "gone-" + state})
			if err != nil {
				t.Fatal(err)
			}
			if state == protocol.StateDelivered {
				if err := s.ApplyDelivery(m.ID, protocol.StateDelivered, ""); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := s.db.Exec(`UPDATE messages SET state=? WHERE id=?`, state, m.ID); err != nil {
					t.Fatal(err)
				}
			}
			_, err = s.Withdraw(m.ID, a)
			if err == nil || !strings.Contains(err.Error(), "already delivered") {
				t.Fatalf("withdrawing a %s message: %v", state, err)
			}
			if stored, _, _ := s.Message(m.ID); stored.State != state {
				t.Fatalf("state changed to %q", stored.State)
			}
		})
	}
}

func TestOwnerAndCTOWithdrawAnyUndeliveredMessage(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	node := mustNode(t, s, a)
	owner := ownerSession(t, s)
	cto := ctoSession(t, s, node)
	for _, tc := range []struct {
		name  string
		actor string
	}{{"owner", owner}, {"cto", cto}} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeNotification,
				Body: "another session's message", IdempotencyKey: "other-" + tc.name})
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.Withdraw(m.ID, tc.actor)
			if err != nil {
				t.Fatalf("%s withdraw: %v", tc.name, err)
			}
			if got.State != protocol.StateWithdrawn {
				t.Fatalf("%s withdraw left %q", tc.name, got.State)
			}
		})
	}
}

func TestOwnerWithdrawsAllOlderThan(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	owner := ownerSession(t, s)
	var ids []string
	for i := 0; i < 3; i++ {
		c.t = c.t.Add(time.Minute)
		m, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeNotification,
			Body: "backlog", IdempotencyKey: "backlog-" + string(rune('a'+i))})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	// A newer message stays.
	c.t = c.t.Add(time.Minute)
	newer, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeNotification,
		Body: "newer", IdempotencyKey: "newer"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.WithdrawOlderThan(ids[2], owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("withdrew %d messages, want the named one and the two older", len(got))
	}
	for _, id := range ids {
		if m, _, _ := s.Message(id); m.State != protocol.StateWithdrawn {
			t.Fatalf("%s is %q, want withdrawn", id, m.State)
		}
	}
	if m, _, _ := s.Message(newer.ID); m.State != protocol.StateStored {
		t.Fatalf("the newer message is %q, want stored", m.State)
	}
	// A normal sender cannot clear a backlog.
	if _, err := s.WithdrawOlderThan(newer.ID, a); err == nil || !strings.Contains(err.Error(), "owner session or the CTO") {
		t.Fatalf("a normal sender cleared a backlog: %v", err)
	}
}

func TestWithdrawRemovesAnOriginalFromItsBatch(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	busyStatus(t, s, b, "busy")
	var ids []string
	for i := 0; i < 3; i++ {
		m, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeRequest,
			Body: "body-" + string(rune('a'+i)), IdempotencyKey: "b-" + string(rune('a'+i))})
		if err != nil {
			t.Fatal(err)
		}
		if !s.HoldForBusy(m) {
			t.Fatal("not held")
		}
		ids = append(ids, m.ID)
	}
	busyStatus(t, s, b, "idle")
	carrier, n, err := releaseOne(t, s, b, false)
	if err != nil || n != 3 {
		t.Fatalf("release: %d %v", n, err)
	}
	if _, err := s.Withdraw(ids[1], a); err != nil {
		t.Fatal(err)
	}
	after, _, _ := s.Message(carrier.ID)
	if len(after.Batch) != 2 {
		t.Fatalf("the batch still carries %d originals, want 2", len(after.Batch))
	}
	if strings.Contains(after.Body, ids[1]) {
		t.Fatalf("the withdrawn message is still in the batch text:\n%s", after.Body)
	}
	if m, _, _ := s.Message(ids[1]); m.State != protocol.StateWithdrawn {
		t.Fatalf("the withdrawn original is %q", m.State)
	}
	for _, id := range []string{ids[0], ids[2]} {
		if _, err := s.Withdraw(id, a); err != nil {
			t.Fatal(err)
		}
	}
	if m, _, _ := s.Message(carrier.ID); m.State != protocol.StateWithdrawn {
		t.Fatalf("an emptied batch is %q, want withdrawn", m.State)
	}
}

func TestWithdrawThroughTheGateway(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	m, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeNotification,
		Body: "stale", IdempotencyKey: "gateway-stale"})
	if err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s, conns: map[string]*nodeConn{}}
	raw, _ := json.Marshal(protocol.MsgWithdraw{MessageID: m.ID, Sender: a})
	data, err := g.handle(context.Background(), &nodeConn{id: mustNode(t, s, a)}, "msg.withdraw", raw)
	if err != nil {
		t.Fatal(err)
	}
	got := data.(map[string]any)["message"].(protocol.Message)
	if got.State != protocol.StateWithdrawn {
		t.Fatalf("gateway withdraw left %q", got.State)
	}
	// A sender from another node cannot withdraw it.
	raw, _ = json.Marshal(protocol.MsgWithdraw{MessageID: m.ID, Sender: b})
	if _, err := g.handle(context.Background(), &nodeConn{id: mustNode(t, s, b)}, "msg.withdraw", raw); err == nil {
		t.Fatal("another sender withdrew the message")
	}
}
