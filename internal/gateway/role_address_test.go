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

// The CTO role address follows the registered CTO network session. The CTO only
// takes messages from the owner, so the sender is an owner session.
func TestRoleAddressFollowsCTOSession(t *testing.T) {
	s, _, _, asker, node := chiefRig(t)
	cto, err := s.RegisterCTOSession(node)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET harness=? WHERE id=?`, protocol.OwnerHarness, asker); err != nil {
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

// The sender is told which session got a message sent to a role address, and
// that a replaced session's message went to the one that replaced it.
func TestRoleAddressTellsTheSender(t *testing.T) {
	s, _, oldChief, asker, node := chiefRig(t)
	newChief, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "chief-two", Name: "chief two"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET online=1, archived_at=0 WHERE id=?`, newChief); err != nil {
		t.Fatal(err)
	}
	if err := s.SetChief(protocol.ChiefSetRequest{SessionID: newChief}, "owner"); err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{"chief", oldChief} {
		m, _, note, err := s.StoreLeadMessage(protocol.MsgSend{Sender: asker, Recipient: to, Body: "a question for the chief"})
		if err != nil {
			t.Fatalf("send to %q: %v", to, err)
		}
		if m.To != newChief || !strings.Contains(note, newChief) {
			t.Fatalf("send to %q: went to %s with note %q, want a note that names %s", to, m.To, note, newChief)
		}
	}
}

// The name lookup resolves a role address before any session or agent name, so
// a session or agent called "chief" or "cto" does not shadow the pinned
// session. agent_message_send resolves its address through this lookup.
func TestRoleAddressNameLookupPrefersTheRole(t *testing.T) {
	s, _, chief, _, node := chiefRig(t)
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "decoy", Name: "chief"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET online=1, archived_at=0 WHERE name='chief'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "decoy-agent", Name: "not the chief"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET agent_id='chief' WHERE name='not the chief'`); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ResolveLeadAddress("chief"); err != nil || got != chief {
		t.Fatalf("lookup of chief = %s, %v; want the pinned chief %s", got, err, chief)
	}

	cto, err := s.RegisterCTOSession(node)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "cto-decoy", Name: "cto"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET online=1, archived_at=0 WHERE name='cto'`); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ResolveLeadAddress("cto"); err != nil || got != cto {
		t.Fatalf("lookup of cto = %s, %v; want the CTO session %s", got, err, cto)
	}
}

// With no chief pinned, the lookup gives the role address reason, not the
// ordinary unknown-name reason that sent senders to agent_sessions_find.
func TestRoleAddressNameLookupWithNoChief(t *testing.T) {
	s, _, _, _, _ := chiefRig(t)
	if err := s.SetChief(protocol.ChiefSetRequest{Clear: true}, "owner"); err != nil {
		t.Fatal(err)
	}
	_, err := s.ResolveLeadAddress("chief")
	if err == nil || !strings.Contains(err.Error(), "no chief of staff is set") {
		t.Fatalf("lookup of chief with no chief pinned: %v, want the reason from role_address.go", err)
	}
	if strings.Contains(err.Error(), "no session is named") {
		t.Fatalf("lookup of chief with no chief pinned says the name is unknown: %v", err)
	}
}
