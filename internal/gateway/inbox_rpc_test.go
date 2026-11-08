package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestInboxAnswerGateway(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	_, _ = s.UpsertSession(node, owner("test"))
	agent, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p"})
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: agent, Text: "question"})
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

func TestInboxCreateUsesAuthenticatedSession(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	agent, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p", Repo: protocol.RepoInfo{Project: "project"}})
	other, _, _ := s.CreateNode("other", "linux")
	g := &Gateway{store: s}
	raw, _ := json.Marshal(map[string]string{"session_id": agent, "kind": "approval", "text": "deploy abc", "recommendation": "yes", "node": "forged"})
	if _, err := g.handle(context.Background(), &nodeConn{id: other}, "inbox.create", raw); err == nil {
		t.Fatal("accepted a session from another node")
	}
	if _, err := g.handle(context.Background(), &nodeConn{id: node}, "inbox.create", raw); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListInbox()
	if err != nil || len(items) != 1 || items[0].Node != "project" || items[0].SessionID != agent {
		t.Fatalf("items: %+v %v", items, err)
	}
}

func TestInboxCreateUsesResolvedRepoAndQuestionDetails(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.AddProjects(r.a, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "other", Path: "/code/other"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`UPDATE sessions SET repo_id='other', name='Reviewer' WHERE id=?`, r.caller); err != nil {
		t.Fatal(err)
	}
	request := map[string]any{"session_id": r.caller, "kind": "question", "text": "Which branch?", "recommendation": "main", "options": []string{"main", "next"}}
	type result struct {
		Item protocol.InboxItem `json:"item"`
	}
	first, err := deliveryCall[result](r, r.a, "inbox.create", request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Item.Node != "other" {
		t.Fatalf("inbox node %q, want resolved repo other", first.Item.Node)
	}
	if first.Item.AskerName != "Reviewer" || first.Item.Recommendation != "main" || !reflect.DeepEqual(first.Item.Options, []string{"main", "next"}) {
		t.Fatalf("question details lost: %+v", first.Item)
	}
	request["text"] = "Which branch now?"
	second, err := deliveryCall[result](r, r.a, "inbox.create", request)
	if err != nil {
		t.Fatal(err)
	}
	items, err := r.s.ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, item := range items {
		status[item.ID] = item.Status
	}
	if status[first.Item.ID] != "replaced" || status[second.Item.ID] != "open" {
		t.Fatalf("question statuses: %v", status)
	}
}

func TestApprovalAnswerHasGatewayNote(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	_, _ = s.UpsertSession(node, owner("test"))
	agent, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p"})
	item, err := s.CreateInboxItem(protocol.InboxItem{Kind: "approval", SessionID: agent, Text: "deploy abc"})
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

func TestMaturityApprovalUsesInboxAndSharedNote(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	_, _ = s.UpsertSession(node, owner("test"))
	agent, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p"})
	for _, action := range []string{"merge project abc123", "deploy project abc123 to prod"} {
		item, err := s.RequestWrittenApproval(agent, "project", protocol.ApprovalRequest{Action: action, Recommendation: "approve"})
		if err != nil {
			t.Fatal(err)
		}
		answered, err := s.AnswerInbox(item.ID, "approve")
		if err != nil {
			t.Fatal(err)
		}
		msgs, err := s.Pending(node, false)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, m := range msgs {
			if m.ApprovalNote == protocol.WrittenApprovalNote("the owner", action, answered.AnsweredAt) {
				found = true
			}
		}
		if !found {
			t.Fatalf("no shared note for %s: %+v", action, msgs)
		}
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
	item, err := s.CreateInboxItem(protocol.InboxItem{
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
		item, err := s.CreateInboxItem(protocol.InboxItem{Kind: kind, SessionID: agent, Text: kind})
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

// Dismissing an approval goes through the same frame as an answer, but it must
// not need the session that asked: old approvals outlive their sessions.
func TestInboxDismissGatewayNeedsNoSession(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	_, _ = s.UpsertSession(node, owner("test"))
	agent, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "p"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{
		"session_id": agent, "kind": "approval", "text": "merge abc", "recommendation": "yes",
	})
	g := &Gateway{store: s}
	if _, err := g.handle(context.Background(), &nodeConn{id: node}, "inbox.create", raw); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListInbox()
	if err != nil || len(items) != 1 || items[0].Kind != "approval" {
		t.Fatalf("items: %+v, %v", items, err)
	}
	// The session that asked is gone, as an old procurement session is now.
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE id=?`, agent); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(map[string]string{"item_id": items[0].ID, "answer": "dismiss"})
	if _, err := g.handle(context.Background(), &nodeConn{id: node}, "inbox.answer", raw); err != nil {
		t.Fatal(err)
	}
	items, err = s.ListInbox()
	if err != nil || len(items) != 1 || items[0].Status != "dismissed" {
		t.Fatalf("items: %+v, %v", items, err)
	}
}
