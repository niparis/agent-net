package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// sendHeld sends through the gateway entry point, so the busy hold runs as it
// does for a real sender.
func sendHeld(t *testing.T, g *Gateway, from, to, body, key string, urgent bool) protocol.Message {
	t.Helper()
	raw, _ := json.Marshal(protocol.MsgSend{Sender: from, Recipient: to, Type: protocol.TypeRequest,
		Body: body, IdempotencyKey: key, Urgent: urgent})
	data, err := g.handle(context.Background(), &nodeConn{id: mustNode(t, g.store, from)}, "msg.send", raw)
	if err != nil {
		t.Fatal(err)
	}
	m := data.(map[string]any)["message"].(protocol.Message)
	if !m.HeldForBusy {
		t.Fatalf("%s was not held for the busy session", key)
	}
	return m
}

func takeUrgent(t *testing.T, g *Gateway, node, session string) ([]protocol.Message, error) {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"session_id": session})
	data, err := g.handle(context.Background(), &nodeConn{id: node}, "msg.take_urgent", raw)
	if err != nil {
		return nil, err
	}
	return data.(map[string]any)["messages"].([]protocol.Message), nil
}

// An urgent message to a busy session is handed to the session's hook inside
// the turn. It is then delivered once: the turn-end release must not send it
// again, and the non-urgent message keeps its hold (decision 0180).
func TestUrgentMessageIsTakenInsideTheTurnAndNotSentAgain(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	g := &Gateway{store: s, conns: map[string]*nodeConn{}}
	busyStatus(t, s, b, "busy")
	calm := sendHeld(t, g, a, b, "calm note", "calm", false)
	stop := sendHeld(t, g, a, b, "STOP copying", "stop", true)

	got, err := takeUrgent(t, g, mustNode(t, s, b), b)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != stop.ID || got[0].Body != "STOP copying" {
		t.Fatalf("took %+v, want only the urgent message %s", got, stop.ID)
	}
	if got[0].AuthorityNote == "" && got[0].RecipientRelation == "" && got[0].FromName == "" {
		t.Fatalf("the taken message has none of the delivery fields the adapter needs: %+v", got[0])
	}
	if st, _, _ := s.Message(stop.ID); st.HeldForBusy || st.State != protocol.StateDispatched {
		t.Fatalf("taken message is held=%v state=%q, want dispatched and not held", st.HeldForBusy, st.State)
	}
	if st, _, _ := s.Message(calm.ID); !st.HeldForBusy {
		t.Fatal("the non-urgent message lost its hold")
	}
	again, err := takeUrgent(t, g, mustNode(t, s, b), b)
	if err != nil || len(again) != 0 {
		t.Fatalf("second take = %+v, %v; want nothing", again, err)
	}
	if err := s.ApplyDelivery(stop.ID, protocol.StateDelivered, ""); err != nil {
		t.Fatal(err)
	}
	busyStatus(t, s, b, "idle")
	carrier, n, err := releaseOne(t, s, b, false)
	if err != nil || n != 1 || carrier.ID != calm.ID || len(carrier.Batch) != 0 {
		t.Fatalf("turn-end release = %+v n=%d err=%v, want only the non-urgent message", carrier, n, err)
	}
}

// Only the node that owns the session may take its messages.
func TestTakeUrgentRefusesAnotherNode(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	g := &Gateway{store: s, conns: map[string]*nodeConn{}}
	busyStatus(t, s, b, "busy")
	sendHeld(t, g, a, b, "STOP", "stop", true)
	if _, err := takeUrgent(t, g, mustNode(t, s, a), b); err == nil {
		t.Fatal("another node took the messages of a session it does not own")
	}
}

// A message with files goes with a normal delivery, so the hook leaves it held.
func TestTakeUrgentKeepsFilesHeld(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	g := &Gateway{store: s, conns: map[string]*nodeConn{}}
	busyStatus(t, s, b, "busy")
	m := sendHeld(t, g, a, b, "STOP", "stop", true)
	if _, err := s.db.Exec(`UPDATE messages SET files=? WHERE id=?`, `[{"name":"x.txt","size":1,"sha256":"00"}]`, m.ID); err != nil {
		t.Fatal(err)
	}
	got, err := takeUrgent(t, g, mustNode(t, s, b), b)
	if err != nil || len(got) != 0 {
		t.Fatalf("took %+v, %v; a message with files must wait for the turn end", got, err)
	}
}
