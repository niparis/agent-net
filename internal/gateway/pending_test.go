package gateway

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestPendingRequests(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "request", IdempotencyKey: "request", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	check := func(want int) {
		t.Helper()
		agents, err := s.Agents(protocol.QueryAgents{})
		if err != nil {
			t.Fatal(err)
		}
		for _, agent := range agents {
			if (agent.SessionID == a || agent.SessionID == b) && len(agent.Pending) != want {
				t.Fatalf("%s pending = %+v, want %d", agent.SessionID, agent.Pending, want)
			}
		}
	}
	check(1)
	agents, err := s.Agents(protocol.QueryAgents{})
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range agents {
		if agent.SessionID != a && agent.SessionID != b {
			continue
		}
		p := agent.Pending[0]
		if p.MessageID != req.ID || p.Since != req.CreatedAt.Unix() || p.Outgoing != (agent.SessionID == a) || p.PeerName == "" {
			t.Fatalf("bad pending: %+v", p)
		}
	}
	_, _, err = s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "reply", IdempotencyKey: "reply", ParentID: req.ID, Type: protocol.TypeReply})
	if err != nil {
		t.Fatal(err)
	}
	check(0)
	_, _, err = s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "notice", IdempotencyKey: "notice", Type: protocol.TypeNotification})
	if err != nil {
		t.Fatal(err)
	}
	check(0)
	_, _, err = s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "old", IdempotencyKey: "old", Type: protocol.TypeRequest})
	if err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(24*time.Hour + time.Second)
	check(0)
}

func TestPendingStallCountOncePerIdlePeriod(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, err := s.db.Exec(`UPDATE sessions SET status='idle', last_active=? WHERE id IN (?, ?)`, c.t.UnixNano(), a, b); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "request", IdempotencyKey: "stall", Type: protocol.TypeRequest}); err != nil {
		t.Fatal(err)
	}
	if err := s.samplePendingStalls(); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(21 * time.Minute)
	if err := s.samplePendingStalls(); err != nil {
		t.Fatal(err)
	}
	if err := s.samplePendingStalls(); err != nil {
		t.Fatal(err)
	}
	counts, err := s.SessionCounts(24 * time.Hour)
	if err != nil || counts.PendingStalls != 2 {
		t.Fatalf("counts = %+v, %v", counts, err)
	}
}
