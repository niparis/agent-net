package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestInboxTabShowsDetails(t *testing.T) {
	m := newModel(80, 24)
	m.asks = askItems([]protocol.InboxItem{{ID: "i_1", Kind: "approval", Text: "deploy abc", Recommendation: "yes", Status: "open", CreatedAt: time.Now()}})
	m = keys(m, "7")
	if !strings.Contains(m.testView(), "deploy abc") {
		t.Fatal(m.testView())
	}
	m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(m.testView(), "yes") {
		t.Fatal(m.testView())
	}
}

func TestApprovalAppearsInInbox(t *testing.T) {
	m := newModel(80, 24)
	m.asks = askItems([]protocol.InboxItem{{ID: "i_1", Kind: "approval", SessionID: "s_1", Text: "deploy abc", Status: "open", CreatedAt: time.Now()}})
	if !strings.Contains(m.testView(), "7 Desk (1)") {
		t.Fatal(m.testView())
	}
	m = keys(m, "7")
	if !strings.Contains(m.testView(), "deploy abc") {
		t.Fatal(m.testView())
	}
}

func TestInboxSummaryDoesNotRepeatKind(t *testing.T) {
	for _, kind := range []string{"approval", "question"} {
		if got := inboxItemSummary(protocol.InboxItem{Kind: kind, Text: "Deploy release?"}); got != "Deploy release?" {
			t.Errorf("%s summary = %q", kind, got)
		}
	}
}

func TestInboxDetailWrapsTextAndHidesIDs(t *testing.T) {
	m := newModel(80, 24)
	m.asks = askItems([]protocol.InboxItem{{ID: "i_private", Kind: "approval", SessionID: "s_private", Text: strings.Repeat("question ", 12) + "lastword", Recommendation: "recommend yes", Status: "open"}})
	m = keys(m, "7")
	m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	view := m.testView()
	if !strings.Contains(view, "lastword") || !strings.Contains(view, "recommend yes") || strings.Contains(view, "s_private") || strings.Contains(view, "i_private") {
		t.Fatalf("detail: %s", view)
	}
}

func TestInboxNoDetailWhenQuestionFitsRow(t *testing.T) {
	m, _ := inboxAllKindsModel(80, 24)
	m.asks[1].Question = "one two three four five"
	m = openInbox(m)
	m.inboxIndex = inboxRowIndex(m, "question")
	m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.inboxDetail {
		t.Fatal("opened detail with no extra content")
	}
}
