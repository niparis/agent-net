package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The role addresses "chief" and "cto" name the session pinned for the role
// (decision 0171, phase 2). After the owner moves the chief, a message to the old
// chief session goes to the current one, and the message names the session that
// got it.
func TestRoleAddressFollowsPinnedChief(t *testing.T) {
	s, _, oldChief, asker, node := chiefRig(t)
	newChief, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "chief-two", Name: "chief two"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET online=1, archived_at=0 WHERE id=?`, newChief); err != nil {
		t.Fatal(err)
	}
	if err := s.SetChief(protocol.ChiefSetRequest{SessionID: newChief}, "owner"); err != nil {
		t.Fatal(err)
	}
	send := func(to string) protocol.Message {
		t.Helper()
		m, _, err := s.StoreMessage(protocol.MsgSend{Sender: asker, Recipient: to, Body: "a question for the chief"})
		if err != nil {
			t.Fatalf("send to %q: %v", to, err)
		}
		return m
	}
	if m := send("chief"); m.To != newChief {
		t.Fatalf("message to the chief role went to %s, want the pinned chief %s", m.To, newChief)
	}
	if m := send(oldChief); m.To != newChief {
		t.Fatalf("message to the replaced chief %s went to %s, want %s", oldChief, m.To, newChief)
	}
}

// With no chief pinned the role address has no session, and the sender is told so.
func TestRoleAddressWithNoPinnedChief(t *testing.T) {
	s, _, _, asker, _ := chiefRig(t)
	if err := s.SetChief(protocol.ChiefSetRequest{Clear: true}, "owner"); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.StoreMessage(protocol.MsgSend{Sender: asker, Recipient: "chief", Body: "anyone there?"})
	if err == nil || !strings.Contains(err.Error(), "no chief") {
		t.Fatalf("message to the chief role with no chief: %v, want a refusal that says there is no chief", err)
	}
}

// The CTO role address follows the registered CTO network session.
func TestRoleAddressFollowsCTOSession(t *testing.T) {
	s, _, _, asker, node := chiefRig(t)
	cto, err := s.RegisterCTOSession(node)
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := s.StoreMessage(protocol.MsgSend{Sender: asker, Recipient: "cto", Body: "a message for the CTO"})
	if err != nil {
		t.Fatal(err)
	}
	if m.To != cto {
		t.Fatalf("message to the cto role went to %s, want the CTO session %s", m.To, cto)
	}
}
