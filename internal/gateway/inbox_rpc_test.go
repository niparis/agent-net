package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestInboxAnswerGateway(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	_, _ = s.UpsertSession(node, owner("test"))
	agent, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p"})
	item, err := storeOldItem(t, s, protocol.InboxItem{Kind: "question", SessionID: agent, Text: "question"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"item_id": item.ID, "answer": "yes"})
	g := &Gateway{store: s}
	if _, err := g.handle(context.Background(), &nodeConn{id: node}, "inbox.answer", raw); err != nil {
		t.Fatal(err)
	}
	pending, err := s.Pending(node, false)
	if err != nil || len(pending) != 1 || pending[0].Body != "yes" {
		t.Fatalf("pending: %+v, %v", pending, err)
	}
}

func TestApprovalAnswerHasGatewayNote(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	_, _ = s.UpsertSession(node, owner("test"))
	agent, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p"})
	item, err := storeOldItem(t, s, protocol.InboxItem{Kind: "approval", SessionID: agent, Text: "deploy abc"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AnswerInbox(item.ID, "approve"); err != nil {
		t.Fatal(err)
	}
	pending, err := s.Pending(node, false)
	if err != nil || len(pending) != 1 || !strings.Contains(pending[0].ApprovalNote, "the owner approved: deploy abc,") {
		t.Fatalf("pending: %+v %v", pending, err)
	}
}

func TestMessagePreservesBothApprovalFields(t *testing.T) {
	s, _ := newTestStore(t)
	sender, recipient := twoSessions(t, s)
	m, _, err := s.StoreMessage(protocol.MsgSend{Sender: sender, Recipient: recipient, Body: "request"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE messages SET owner_approved=1, approval_note=? WHERE id=?`, "the owner approved: action, time", m.ID); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Message(m.ID)
	if err != nil || !ok || !got.OwnerApproved || got.ApprovalNote != "the owner approved: action, time" {
		t.Fatalf("message: %+v %v", got, err)
	}
}

// A charter draft is an ask with no live asking session. The CTO accepts it
// the same way the owner does: the charter value is written and the Inbox row
// closes. It must not try to deliver a message to a session.
func TestInboxAnswerCharterDraftAsCTO(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	_, _ = s.UpsertSession(node, owner("test"))
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "project", Repo: protocol.RepoInfo{Project: "demo"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCharter("demo", "maturity", "early", "owner"); err != nil {
		t.Fatal(err)
	}
	res, err := s.db.Exec(`INSERT INTO charter_drafts(level,scope,attr,value,source,source_hash,drafted_by,created_at) VALUES('repo','demo','purpose','A small demo','AGENTS.md','hash','s_agent',?)`, s.now().UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	draftID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	item, err := storeOldItem(t, s, protocol.InboxItem{
		Kind: protocol.AskKindDraft, SessionID: gatewayAsker, FromKind: protocol.AskFromGateway,
		AskerName: "charter draft", SourceRef: fmt.Sprintf("draft:%d", draftID),
		Text: "Charter draft for demo purpose: A small demo?", Options: []string{"accept", "reject"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AnswerInboxAs(item.ID, "accept", "cto"); err != nil {
		t.Fatal(err)
	}
	var status, decidedBy string
	if err := s.db.QueryRow(`SELECT status,decided_by FROM charter_drafts WHERE id=?`, draftID).Scan(&status, &decidedBy); err != nil {
		t.Fatal(err)
	}
	if status != "accepted" || decidedBy != "cto" {
		t.Fatalf("draft status=%q decided_by=%q, want accepted by cto", status, decidedBy)
	}
	var itemStatus, answer string
	if err := s.db.QueryRow(`SELECT status,answer FROM inbox_items WHERE id=?`, item.ID).Scan(&itemStatus, &answer); err != nil {
		t.Fatal(err)
	}
	if itemStatus == "open" || answer != "accept" {
		t.Fatalf("inbox item status=%q answer=%q, want closed with accept", itemStatus, answer)
	}
	if c, _ := s.EffectiveCharter("demo", "demo", ""); c.Purpose != "A small demo" {
		t.Fatalf("charter purpose %q, want the accepted draft", c.Purpose)
	}
	if pending, err := s.Pending(node, false); err != nil || len(pending) != 0 {
		t.Fatalf("a draft answer created a message: %+v %v", pending, err)
	}
}

// An answer to an approval or a question must not need an owner session on the
// asking session's machine. The answer is stored and waits for the session.
func TestInboxAnswerDoesNotNeedOwnerSession(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	// This machine has no owner session: the owner is on another machine.
	agent, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p"})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"question", "approval"} {
		item, err := storeOldItem(t, s, protocol.InboxItem{Kind: kind, SessionID: agent, Text: kind})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.AnswerInbox(item.ID, "yes"); err != nil {
			t.Fatalf("%s answer: %v", kind, err)
		}
	}
	pending, err := s.Pending(node, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 {
		t.Fatalf("pending answers = %d, want 2: %+v", len(pending), pending)
	}
	for _, m := range pending {
		if m.To != agent {
			t.Fatalf("answer to %q, want %q", m.To, agent)
		}
	}
}
