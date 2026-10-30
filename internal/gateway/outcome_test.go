package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestRecordOutcome(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	if _, err := s.db.Exec(`UPDATE sessions SET repo_project=CASE id WHEN ? THEN 'consumer' ELSE 'provider' END WHERE id IN (?,?)`, a, a, b); err != nil {
		t.Fatal(err)
	}
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "request"})
	if err != nil {
		t.Fatal(err)
	}
	rep, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "no", ParentID: req.ID, ReplyOutcome: protocol.ReplyOutcome{Outcome: "reject", Reason: "outside charter"}})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Outcomes("consumer", "provider", 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("outcomes: %+v %v", list, err)
	}
	if list[0].MessageID != req.ID || list[0].ReplyID != rep.ID || list[0].Reason != "outside charter" {
		t.Fatalf("outcome: %+v", list[0])
	}
}

func TestOutcomeInbox(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "first line\nmore"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "unclear", ParentID: req.ID, ReplyOutcome: protocol.ReplyOutcome{Outcome: "escalate", Reason: "unclear", Recommendation: "ask owner"}})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.OutcomeInbox()
	if err != nil || len(rows) != 1 {
		t.Fatalf("inbox: %+v %v", rows, err)
	}
	if rows[0].Request != "first line" || rows[0].Reason != "unclear" || rows[0].Recommendation != "ask owner" || rows[0].Sender != a || rows[0].Recipient != b {
		t.Fatalf("entry: %+v", rows[0])
	}
}

func TestOverruleOutcome(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	node, _, err := s.CreateNode("owner-host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	req, _, _ := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "do work"})
	rep, _, _ := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, Body: "no", ParentID: req.ID, ReplyOutcome: protocol.ReplyOutcome{Outcome: "reject", Reason: "no"}})
	if _, err = s.ResolveOutcome(rep.ID, a, "overrule"); err == nil {
		t.Fatal("agent overruled")
	}
	msg, err := s.ResolveOutcome(rep.ID, owner, "overrule")
	if err != nil {
		t.Fatal(err)
	}
	if msg.Body != "do work" || !msg.Overruled || msg.To != b {
		t.Fatalf("message: %+v", msg)
	}
	if _, err = s.ResolveOutcome(rep.ID, owner, "overrule"); err == nil {
		t.Fatal("resolved twice")
	}
	rows, err := s.OutcomeInbox()
	if err != nil || len(rows) != 0 {
		t.Fatalf("inbox: %+v %v", rows, err)
	}
}

func TestLetStandNotifiesAndOwnerRequestsStayOutOfInbox(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	node, _, err := s.CreateNode("host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	req, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "work"})
	if err != nil {
		t.Fatal(err)
	}
	rep, _, err := s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: req.ID, Body: "no", ReplyOutcome: protocol.ReplyOutcome{Outcome: "reject", Reason: "reason"}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.ResolveOutcome(rep.ID, owner, "let_stand")
	if err != nil || m.ID == "" || m.To != b || m.Overruled || m.Body != "Your decision stands for: work" {
		t.Fatalf("resolution: %+v %v", m, err)
	}
	req, _, err = s.StoreMessage(protocol.MsgSend{Sender: owner, Recipient: b, Body: "work"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: owner, ParentID: req.ID, Body: "no", ReplyOutcome: protocol.ReplyOutcome{Outcome: "reject", Reason: "reason"}})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := s.OutcomeInbox()
	if err != nil || len(entries) != 0 {
		t.Fatalf("inbox: %+v %v", entries, err)
	}
}
