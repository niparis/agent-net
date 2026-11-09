package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The owner's n and m marks are corrections the chief must be able to search.
// Each row gives the ask, the whole question, what the chief did with it, the
// mark and when the owner made it. A page continues with offset.
func TestOwnerJudgmentMarksFind(t *testing.T) {
	s, c, chief, asker, node := chiefRig(t)
	first := chiefAsk(t, s, asker, "Deploy the snapshot fix?\nScope: nodes. Destination: ops.")
	sortAsk(t, s, chief, node, protocol.AskSortArgs{Ask: first, Result: protocol.AskSortClose, Reason: "routine deploy"})
	if err := s.MarkAsk(protocol.AskMarkRequest{Ask: first, Mark: protocol.MarkDidntNeedMe, Why: "the deploy is routine"}, "owner"); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Minute)
	second := chiefAskKind(t, s, asker, "approval", "Rotate the admin token?")
	sortAsk(t, s, chief, node, protocol.AskSortArgs{Ask: second, Result: protocol.AskSortOwner, Priority: 2, WhyYou: "only the owner holds the token", Reason: "needs a decision"})
	if err := s.MarkAsk(protocol.AskMarkRequest{Ask: second, Mark: protocol.MarkShouldHaveAsked}, "owner"); err != nil {
		t.Fatal(err)
	}

	page, err := s.FindJudgments(protocol.JudgmentsFindRequest{Marks: true, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 {
		t.Fatalf("page one has %d rows: %+v", len(page), page)
	}
	row := page[0]
	if row.Ask != second || row.Ref != second || row.Kind != protocol.StandingKindCorrection || row.Area != "chief" {
		t.Fatalf("row key: %+v", row)
	}
	if row.Question != "Rotate the admin token?" || row.Mark != protocol.MarkShouldHaveAsked || row.MarkText != "should have asked me" {
		t.Fatalf("row question and mark: %+v", row)
	}
	if !strings.Contains(row.Disposition, "owner") || !strings.Contains(row.Disposition, "only the owner holds the token") {
		t.Fatalf("row disposition: %q", row.Disposition)
	}
	if row.SavedAt == 0 || row.SavedAt != c.now().Unix() {
		t.Fatalf("row time: %d, want %d", row.SavedAt, c.now().Unix())
	}

	next, err := s.FindJudgments(protocol.JudgmentsFindRequest{Marks: true, Limit: 1, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(next) != 1 || next[0].Ask != first || next[0].Mark != protocol.MarkDidntNeedMe {
		t.Fatalf("page two: %+v", next)
	}
	if next[0].Question != "Deploy the snapshot fix?\nScope: nodes. Destination: ops." {
		t.Fatalf("the whole question was not kept: %q", next[0].Question)
	}
	if !strings.Contains(next[0].Disposition, "closed") || !strings.Contains(next[0].Disposition, "routine deploy") {
		t.Fatalf("page two disposition: %q", next[0].Disposition)
	}

	found, err := s.FindJudgments(protocol.JudgmentsFindRequest{Marks: true, Query: "snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Ask != first {
		t.Fatalf("mark search: %+v", found)
	}
}
