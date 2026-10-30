package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"testing"
)

func TestTabsMoveFeedToMessages(t *testing.T) {
	m := newModel(80, 24)
	if !strings.Contains(m.View(), "1 Sessions") {
		t.Fatal("sessions tab missing")
	}
	m = keys(m, "2")
	if !strings.Contains(m.View(), "2 Messages") || !strings.Contains(m.View(), "Messages") {
		t.Fatal("messages tab missing")
	}
	m = keys(m, "3")
	if !strings.Contains(m.View(), "3 Quotas") {
		t.Fatal("quotas tab missing")
	}
}

func TestCTOBoxFocusAndSend(t *testing.T) {
	src := &fakeCTOSource{fakeSource: &fakeSource{}}
	m := New(src)
	m = keys(m, "tab")
	if m.focus != focusCTO {
		t.Fatal("tab did not focus CTO")
	}
	m = keys(m, "h", "i")
	next, cmd := m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("send missing")
	}
	cmd()
	if len(src.sent) != 1 || src.sent[0] != "hi" {
		t.Fatalf("sent %v", src.sent)
	}
	m = keys(m, "esc")
	if m.focus != focusSessions {
		t.Fatal("escape did not return focus")
	}
}
