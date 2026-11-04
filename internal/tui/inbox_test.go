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
	m.inboxItems = []protocol.InboxItem{{ID: "i_1", Kind: "approval", Text: "deploy abc", Recommendation: "yes", Status: "open", CreatedAt: time.Now()}}
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
	m.inboxItems = []protocol.InboxItem{{ID: "i_1", Kind: "approval", SessionID: "s_1", Text: "deploy abc", Status: "open", CreatedAt: time.Now()}}
	if !strings.Contains(m.testView(), "7 Inbox (1)") {
		t.Fatal(m.testView())
	}
	m = keys(m, "7")
	if !strings.Contains(m.testView(), "deploy abc") {
		t.Fatal(m.testView())
	}
}
