package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The conversation view is gone, and b does not read session text.
func TestBDoesNotReadTheSession(t *testing.T) {
	m, src := workerModel(100, 30)
	m = selectID(m, workerID)
	next, cmd := press(m, tea.KeyPressMsg{Text: "b", Code: []rune("b")[0]})
	if cmd != nil {
		t.Fatal("b started a command")
	}
	if next.testView() != m.testView() {
		t.Fatal("b changed the screen")
	}
	if len(src.queries) != 0 {
		t.Fatalf("b read the session text: %+v", src.queries)
	}
}
