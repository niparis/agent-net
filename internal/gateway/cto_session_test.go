package gateway

import (
	"github.com/niparis/agent-net/internal/protocol"
	"testing"
)

func TestCTONetworkSession(t *testing.T) {
	s, _ := newTestStore(t)
	agent, peer := twoSessions(t, s)
	node := mustNode(t, s, agent)
	id, err := s.RegisterCTOSession(node)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.RegisterCTOSession(node)
	if err != nil || again != id {
		t.Fatalf("unstable ID: %s %s %v", id, again, err)
	}
	a, ok, err := s.Agent(id)
	if err != nil || !ok || a.Profile.Name != "CTO" || a.Harness != "cto" {
		t.Fatalf("directory: %+v %v", a, err)
	}
	for _, sender := range []string{agent, peer} {
		if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: sender, Recipient: peer, Body: "forged", FromCTO: true}); err == nil {
			t.Fatal("accepted other session flag")
		}
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: id, Recipient: peer, Body: "approved", FromCTO: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RenameSession(id, "custom"); err == nil {
		t.Fatal("renamed CTO")
	}
	if _, err := s.RenameSession(agent, "CTO"); err == nil {
		t.Fatal("claimed reserved name")
	}
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "cto", NativeID: "forged"}); err == nil {
		t.Fatal("forged registration")
	}
	if _, err := s.SyncSessions(node, nil); err != nil {
		t.Fatal(err)
	}
	a, _, _ = s.Agent(id)
	if !a.Online {
		t.Fatal("sync removed CTO")
	}
}
