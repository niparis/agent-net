package gateway

import (
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The owner asked (through the CTO, 2026-11-09) to store every outcome in
// plain words, including a button with no text: an owner dismiss is one answer
// row, but an automatic close is not a decision and makes none.
func TestOwnerAnswerAndDismissCreateAnswerRowAndAutomaticCloseDoesNot(t *testing.T) {
	s, _ := newTestStore(t)
	// Create an ask for a project.
	item, err := s.CreateInboxItem(protocol.InboxItem{
		Kind:      "question",
		SessionID: "s_test_session",
		Node:      "my-project",
		Text:      "Should we migrate SQLite to postgres?",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Owner answers the ask.
	if _, err := s.AnswerInbox(item.ID, "No, stick to SQLite."); err != nil {
		t.Fatal(err)
	}

	// Verify an answer row was written to standing_orders.
	judgments, err := s.FindJudgments(protocol.JudgmentsFindRequest{Ref: item.ID})
	if err != nil {
		t.Fatalf("FindJudgments failed: %v", err)
	}
	if len(judgments) != 1 {
		t.Fatalf("expected 1 answer row, got %d", len(judgments))
	}
	j := judgments[0]
	if j.Kind != protocol.StandingKindAnswer {
		t.Errorf("expected kind %q, got %q", protocol.StandingKindAnswer, j.Kind)
	}
	if j.Area != "my-project" {
		t.Errorf("expected area %q, got %q", "my-project", j.Area)
	}
	if j.Ref != item.ID {
		t.Errorf("expected ref %q, got %q", item.ID, j.Ref)
	}
	wantText := "Q: Should we migrate SQLite to postgres? / A: No, stick to SQLite."
	if j.Text != wantText {
		t.Errorf("expected text %q, got %q", wantText, j.Text)
	}

	// Now create another ask that gets automatically closed.
	closedItem, err := s.CreateInboxItem(protocol.InboxItem{
		Kind:      "question",
		SessionID: "s_test_session_2",
		Node:      "my-project",
		Text:      "Will this ask be auto-closed?",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Auto-close it via closeAsks.
	if _, err := s.closeAsks(s.db, "id=?", []any{closedItem.ID}, protocol.AskReasonSessionEnded); err != nil {
		t.Fatal(err)
	}

	// Verify no answer row was created for closedItem.
	closedJudgments, err := s.FindJudgments(protocol.JudgmentsFindRequest{Ref: closedItem.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(closedJudgments) != 0 {
		t.Errorf("expected 0 answer rows for automatic close, got %d", len(closedJudgments))
	}

	// An owner dismiss is one answer row that says no decision was made.
	dismissItem, err := s.CreateInboxItem(protocol.InboxItem{
		Kind:      "question",
		SessionID: "s_test_session_3",
		Node:      "my-project",
		Text:      "Will this ask be dismissed?",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AnswerInbox(dismissItem.ID, "dismiss"); err != nil {
		t.Fatal(err)
	}
	dismissJudgments, err := s.FindJudgments(protocol.JudgmentsFindRequest{Ref: dismissItem.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(dismissJudgments) != 1 || !strings.HasSuffix(dismissJudgments[0].Text, "A: dismissed: no decision") {
		t.Errorf("expected one answer row ending %q for the dismissed ask, got %+v", "A: dismissed: no decision", dismissJudgments)
	}
}

func TestJudgmentAddStoresCorrection(t *testing.T) {
	s, _ := newTestStore(t)
	added, err := s.AddJudgment(protocol.JudgmentAddRequest{
		Kind: protocol.StandingKindCorrection,
		Text: "Do not merge without running benchmarks first.",
		Area: "a2a-ocs",
		Ref:  "ask_123",
	})
	if err != nil {
		t.Fatalf("AddJudgment failed: %v", err)
	}
	if added.Kind != protocol.StandingKindCorrection {
		t.Errorf("expected kind %q, got %q", protocol.StandingKindCorrection, added.Kind)
	}
	if added.Area != "a2a-ocs" {
		t.Errorf("expected area %q, got %q", "a2a-ocs", added.Area)
	}
	if added.Ref != "ask_123" {
		t.Errorf("expected ref %q, got %q", "ask_123", added.Ref)
	}
	if added.Text != "Do not merge without running benchmarks first." {
		t.Errorf("unexpected text %q", added.Text)
	}
	if added.Source != protocol.StandingSourceOwner {
		t.Errorf("expected source owner, got %q", added.Source)
	}

	// Invalid kind must be rejected.
	if _, err := s.AddJudgment(protocol.JudgmentAddRequest{Kind: "invalid", Text: "text"}); err == nil {
		t.Errorf("expected error for invalid judgment kind")
	}

	// Empty text must be rejected.
	if _, err := s.AddJudgment(protocol.JudgmentAddRequest{Kind: protocol.StandingKindRule, Text: "   "}); err == nil {
		t.Errorf("expected error for empty text")
	}
}

func TestOwnerJudgmentsFind(t *testing.T) {
	s, _ := newTestStore(t)

	// Save an order, a rule, a correction, and an answer.
	_, err := s.SaveStandingOrder(protocol.StandingOrdersRequest{
		Text:   "Keep codebase simple and clean.",
		Source: protocol.StandingSourceOwner,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.AddJudgment(protocol.JudgmentAddRequest{
		Kind: protocol.StandingKindRule,
		Text: "Never use cgo dependencies in agent-net.",
		Area: "agent-net",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.AddJudgment(protocol.JudgmentAddRequest{
		Kind: protocol.StandingKindCorrection,
		Text: "Run benchmarks on staging before release in ancient-standards.",
		Area: "ancient-standards",
	})
	if err != nil {
		t.Fatal(err)
	}

	item, err := s.CreateInboxItem(protocol.InboxItem{
		Kind:      "question",
		SessionID: "s_1",
		Node:      "agent-net",
		Text:      "Can we use cgo for sqlite?",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AnswerInbox(item.ID, "No, modernc sqlite only."); err != nil {
		t.Fatal(err)
	}

	// 1. Search by word "benchmarks":
	hits, err := s.FindJudgments(protocol.JudgmentsFindRequest{Query: "benchmarks"})
	if err != nil {
		t.Fatalf("FindJudgments query failed: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit for 'benchmarks', got %d", len(hits))
	}
	if !strings.Contains(hits[0].Text, "Run benchmarks") {
		t.Errorf("unexpected hit text: %q", hits[0].Text)
	}
	if hits[0].Snippet == "" {
		t.Errorf("expected snippet for FTS hit, got empty")
	}

	// 2. Filter by area "agent-net":
	areaHits, err := s.FindJudgments(protocol.JudgmentsFindRequest{Area: "agent-net"})
	if err != nil {
		t.Fatal(err)
	}
	if len(areaHits) != 2 {
		t.Fatalf("expected 2 judgments for area agent-net, got %d", len(areaHits))
	}
	for _, h := range areaHits {
		if h.Area != "agent-net" {
			t.Errorf("expected area agent-net, got %q", h.Area)
		}
	}

	// 3. Search by word "sqlite" filtered by area "agent-net":
	sqliteHits, err := s.FindJudgments(protocol.JudgmentsFindRequest{Query: "sqlite", Area: "agent-net"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sqliteHits) != 1 {
		t.Fatalf("expected 1 hit for sqlite in agent-net, got %d", len(sqliteHits))
	}
	if sqliteHits[0].Kind != protocol.StandingKindAnswer {
		t.Errorf("expected kind answer, got %q", sqliteHits[0].Kind)
	}

	// 4. No query: newest first.
	all, err := s.FindJudgments(protocol.JudgmentsFindRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("expected 4 judgments, got %d", len(all))
	}
	for i := 0; i < len(all)-1; i++ {
		if all[i].SavedAt < all[i+1].SavedAt {
			t.Errorf("expected newest first, but item %d (%d) < item %d (%d)", i, all[i].SavedAt, i+1, all[i+1].SavedAt)
		}
	}
}

func TestJudgmentsBackfillRunsOnOldDB(t *testing.T) {
	s, _ := newTestStore(t)
	// Populate inbox_items with answered owner asks as if on an older release.
	for i := 1; i <= 10; i++ {
		_, err := s.db.Exec(`INSERT INTO inbox_items(id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status,answer,answered_at)
			VALUES(?,?,?,?,?,'rec','m_1','c_1',?,'answered',?,?)`,
			fmt.Sprintf("i_test_%d", i), "question", "s_1", "proj-alpha",
			fmt.Sprintf("Question %d", i),
			int64(i*1000), fmt.Sprintf("Answer %d", i), int64((i+1)*1000*1e9))
		if err != nil {
			t.Fatal(err)
		}
	}
	// Insert an auto-closed ask.
	_, err := s.db.Exec(`INSERT INTO inbox_items(id,kind,session_id,node,text,recommendation,message_id,conversation_id,created_at,status,answer,answered_at)
		VALUES('i_closed','question','s_1','proj-alpha','Question closed','rec','m_1','c_1',1000,'closed','session_ended',2000*1e9)`)
	if err != nil {
		t.Fatal(err)
	}

	// Run backfill query.
	if err := s.BackfillAnswerJudgments(); err != nil {
		t.Fatalf("BackfillAnswerJudgments failed: %v", err)
	}

	// Verify backfilled rows.
	rows, err := s.FindJudgments(protocol.JudgmentsFindRequest{Kind: protocol.StandingKindAnswer})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 10 {
		t.Fatalf("expected 10 backfilled answers, got %d", len(rows))
	}
	for _, r := range rows {
		if !strings.HasPrefix(r.Text, "Q: Question ") {
			t.Errorf("unexpected backfilled text: %q", r.Text)
		}
		if r.Area != "proj-alpha" {
			t.Errorf("unexpected area: %q", r.Area)
		}
		if !strings.HasPrefix(r.Ref, "i_test_") {
			t.Errorf("unexpected ref: %q", r.Ref)
		}
	}
}
