package gateway

import (
	"github.com/niparis/agent-net/internal/protocol"
	"testing"
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
