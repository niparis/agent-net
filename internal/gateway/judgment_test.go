package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

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

	if _, err = s.AddJudgment(protocol.JudgmentAddRequest{
		Kind: protocol.StandingKindAnswer,
		Text: "Q: Can we use cgo for sqlite? / A: No, modernc sqlite only.",
		Area: "agent-net",
	}); err != nil {
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

	// 5. Find by JudgmentID, and IDs.
	byJudgmentID, err := s.FindJudgments(protocol.JudgmentsFindRequest{JudgmentID: all[1].ID})
	if err != nil || len(byJudgmentID) != 1 || byJudgmentID[0].ID != all[1].ID {
		t.Fatalf("expected 1 hit by JudgmentID, got %+v (err %v)", byJudgmentID, err)
	}
	byIDs, err := s.FindJudgments(protocol.JudgmentsFindRequest{IDs: []string{all[0].ID, all[1].ID}})
	if err != nil || len(byIDs) != 2 {
		t.Fatalf("expected 2 hits by IDs, got %+v (err %v)", byIDs, err)
	}
}
