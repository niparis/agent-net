package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOPresenceReturnsOnHostSync(t *testing.T) {
	s, _ := newTestStore(t)
	agent, peer := twoSessions(t, s)
	host := mustNode(t, s, agent)
	other := mustNode(t, s, peer)
	id, err := s.RegisterCTOSession(host)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeOffline(host); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SyncSessions(other, nil); err != nil {
		t.Fatal(err)
	}
	a, _, err := s.Agent(id)
	if err != nil || a.Online || a.NodeID != host {
		t.Fatalf("other host changed CTO: %+v %v", a, err)
	}
	root, _, err := s.StoreMessage(protocol.MsgSend{Sender: id, Recipient: peer, Body: "question"})
	if err != nil {
		t.Fatal(err)
	}
	msg, _, err := s.StoreMessage(protocol.MsgSend{Sender: peer, Recipient: id, Body: "wake", ParentID: root.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SyncSessions(host, nil); err != nil {
		t.Fatal(err)
	}
	a, _, err = s.Agent(id)
	if err != nil || !a.Online || a.Status != "dormant" {
		t.Fatalf("CTO should be reachable and dormant: %+v %v", a, err)
	}
	pending, err := s.Pending(host, true)
	if err != nil || len(pending) != 1 || pending[0].ID != msg.ID {
		t.Fatalf("pending CTO message: %+v %v", pending, err)
	}
}

func TestCTOPresenceCannotMoveIdentity(t *testing.T) {
	g, host := scopeGateway(t)
	other, _, err := g.store.CreateNode("other", "linux")
	if err != nil {
		t.Fatal(err)
	}
	id, err := g.store.RegisterCTOSession(host)
	if err != nil {
		t.Fatal(err)
	}
	report := func(node, status string) error {
		raw, _ := json.Marshal(map[string]string{"session_id": id, "status": status})
		_, err := g.handle(context.Background(), &nodeConn{id: node}, "cto.presence", raw)
		return err
	}
	if err := report(other, "dormant"); err != nil {
		t.Fatal(err)
	}
	a, _, _ := g.store.Agent(id)
	if a.NodeID != host || a.Status != "idle" {
		t.Fatalf("other node changed CTO: %+v", a)
	}
	if err := report(host, "dormant"); err != nil {
		t.Fatal(err)
	}
	row, err := g.store.GetSession(protocol.SessionGetRequest{Session: id})
	if err != nil || !row.Session.Online || row.Session.Status != "dormant" || row.Session.Availability != "wakes on message" {
		t.Fatalf("directory: %+v %v", row, err)
	}
	if err := report(host, "busy"); err == nil {
		t.Fatal("accepted invalid status")
	}
}
