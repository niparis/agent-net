package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// fillInbox leaves n peer notifications waiting for to.
func fillInbox(t *testing.T, s *Store, from, to string, n int, prefix string) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: from, Recipient: to, Type: protocol.TypeNotification,
			Body: "waiting", IdempotencyKey: prefix + "-" + string(rune('a'+i))}); err != nil {
			t.Fatalf("fill %d: %v", i, err)
		}
	}
}

func pendingHas(t *testing.T, s *Store, session, id string) bool {
	t.Helper()
	ms, err := s.Pending(mustNode(t, s, session), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.ID == id {
			return true
		}
	}
	return false
}

// A reply to the recipient's own request always goes through, even when the
// recipient's inbox is at the limit.
func TestInboxLimitAcceptsReplyWithFullInbox(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	third, err := s.UpsertSession(mustNode(t, s, b), protocol.SessionInfo{Harness: "pi", NativeID: "nt", Name: "third"})
	if err != nil {
		t.Fatal(err)
	}
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeRequest,
		Body: "please answer", IdempotencyKey: "request"})
	if err != nil {
		t.Fatal(err)
	}
	// The reply goes to a, so a's inbox is the one at the limit.
	fillInbox(t, s, third, a, DefaultInboxLimit, "fill")
	reply, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: req.ID,
		Type: protocol.TypeReply, Body: "the answer", IdempotencyKey: "reply"})
	if err != nil {
		t.Fatalf("a reply was refused at the limit: %v", err)
	}
	if reply.State != protocol.StateStored {
		t.Fatalf("reply state = %q, want stored", reply.State)
	}
	if !pendingHas(t, s, a, reply.ID) {
		t.Fatal("the reply is not queued for delivery")
	}
}

// Calls to a worker and its notices to the caller are not peer messages: a
// full caller inbox does not refuse a worker's done notice.
func TestInboxLimitAcceptsWorkerNoticesWithFullInbox(t *testing.T) {
	s, _ := newTestStore(t)
	caller, peer := twoSessions(t, s)
	node := mustNode(t, s, caller)
	worker, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "worker", Name: "builder",
		Worker: &protocol.WorkerInfo{ID: "w_1", Owner: caller}})
	if err != nil {
		t.Fatal(err)
	}
	fillInbox(t, s, peer, caller, DefaultInboxLimit, "caller")
	notice, _, err := s.StoreMessage(protocol.MsgSend{Sender: worker, Recipient: caller, Type: protocol.TypeNotification,
		Body: "done: all checks pass", WorkerNotice: true, IdempotencyKey: "worker-done"})
	if err != nil {
		t.Fatalf("a worker notice was refused at the limit: %v", err)
	}
	if notice.State != protocol.StateStored {
		t.Fatalf("notice state = %q", notice.State)
	}
	// The caller's follow-up to its own worker is not a peer message either.
	fillInbox(t, s, peer, worker, DefaultInboxLimit, "worker")
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: caller, Recipient: worker, Type: protocol.TypeRequest,
		Body: "one more thing", IdempotencyKey: "follow-up"}); err != nil {
		t.Fatalf("a caller follow-up was refused at the limit: %v", err)
	}
}

// agent-net's own notices never count toward the limit, so a burst of step
// tasks cannot fill a recipient's inbox for its peers.
func TestAgentNetNoticesDoNotCountTowardLimit(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	for i := 0; i < DefaultInboxLimit+3; i++ {
		if _, err := s.db.Exec(`INSERT INTO messages (id, conversation_id, parent_id, sender, recipient, type, body, hop, idem_key, created_at, expires_at, state)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, "m_notice"+string(rune('a'+i)), "c_notice"+string(rune('a'+i)), "",
			agentNetSender, b, protocol.TypeNotification, "step task", 0, "notice-"+string(rune('a'+i)),
			c.t.UnixNano(), c.t.Add(protocol.DefaultTTL).UnixNano(), protocol.StateStored); err != nil {
			t.Fatal(err)
		}
	}
	m, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeNotification,
		Body: "peer message", IdempotencyKey: "peer"})
	if err != nil {
		t.Fatalf("an agent-net notice counted toward the limit: %v", err)
	}
	if m.QueueWaiting != 0 {
		t.Fatalf("the peer message reports %d waiting, want 0", m.QueueWaiting)
	}
}

// If the session goes ready between the busy check and the hold, the message
// must not stay held until the next status change or the 30 minute fallback.
func TestBusyHoldRaceReleasesWhenStatusFlipsBeforeHold(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	busyStatus(t, s, b, "busy")
	flipped := false
	busyHoldRaceHook = func() {
		if flipped {
			return
		}
		flipped = true
		busyStatus(t, s, b, "idle")
	}
	defer func() { busyHoldRaceHook = func() {} }()
	g := &Gateway{store: s, conns: map[string]*nodeConn{}}
	raw, _ := json.Marshal(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeNotification,
		Body: "race", IdempotencyKey: "race"})
	data, err := g.handle(context.Background(), &nodeConn{id: mustNode(t, s, a)}, "msg.send", raw)
	if err != nil {
		t.Fatal(err)
	}
	m := data.(map[string]any)["message"].(protocol.Message)
	if m.HeldForBusy {
		t.Fatalf("the message stayed held after the session went ready: %+v", m)
	}
	if stored, _, _ := s.Message(m.ID); stored.HeldForBusy {
		t.Fatal("the stored message is still marked held for busy")
	}
	if !pendingHas(t, s, b, m.ID) {
		t.Fatal("the released message is not queued for delivery")
	}
}
