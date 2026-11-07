package gateway

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestNoticeReminderClassification(t *testing.T) {
	for _, key := range []string{"check:s:day:1", "step-closed_abc", "step_abc"} {
		if needsReminderCheck(protocol.Message{From: "agent-net", IdempotencyKey: key}) {
			t.Errorf("%s classified as reminder", key)
		}
	}
	if !needsReminderCheck(protocol.Message{From: "agent-net", IdempotencyKey: "reminder:m"}) {
		t.Fatal("reminder not classified")
	}
}

func TestNoticeRetirementAndStall(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	m, err := s.storeCheck(protocol.AgentView{SessionID: b}, "day", 1, 0, c.t)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE session_checks SET check_waiting=0 WHERE session_id=?`, b); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state) VALUES('m_orphan','c_orphan','','agent-net',?,'notification','batch',0,'step_orphan',?,?,'stored')`, b, c.t.UnixNano(), c.t.Add(time.Hour).UnixNano()); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(21 * time.Minute)
	if err = s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	var state string
	for _, id := range []string{m.ID, "m_orphan"} {
		if err = s.db.QueryRow(`SELECT state FROM messages WHERE id=?`, id).Scan(&state); err != nil || state != "expired" {
			t.Fatalf("%s state=%s err=%v", id, state, err)
		}
	}
	recipient, _, _ := s.Agent(b)
	if recipient.StalledSince != 0 {
		t.Fatal("obsolete notices caused stall")
	}
	if _, _, err = send(s, a, b, "real request", ""); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(21 * time.Minute)
	if err = s.checkSessionStalls(); err != nil {
		t.Fatal(err)
	}
	recipient, _, _ = s.Agent(b)
	if recipient.StalledSince == 0 {
		t.Fatal("undelivered request did not cause stall")
	}
}

func TestNoticePendingKeepsCurrentCheckAndStepClosed(t *testing.T) {
	s, c := newTestStore(t)
	_, b := twoSessions(t, s)
	m, err := s.storeCheck(protocol.AgentView{SessionID: b}, "day", 1, 0, c.t)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state) VALUES('m_closed','c_closed','','agent-net',?,'notification','closed',0,'step-closed_test',?,?,'stored')`, b, c.t.UnixNano(), c.t.Add(time.Hour).UnixNano()); err != nil {
		t.Fatal(err)
	}
	node, err := s.SessionNode(b)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.Pending(node, false)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, p := range pending {
		found[p.ID] = true
	}
	if !found[m.ID] || !found["m_closed"] {
		t.Fatalf("current notices missing from queue: %+v", pending)
	}
}
