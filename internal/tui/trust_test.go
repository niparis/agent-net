package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
)

func keyT() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")} }

func TestTrustNeedsASelectedSession(t *testing.T) {
	empty := &fakeSource{snap: Snapshot{Connected: true}}
	m := New(empty)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m, _ = press(m, keyT())
	if !m.notice.err {
		t.Fatal("missing selection should fail")
	}
}

func TestTrustKeyIsInHelpAndFooter(t *testing.T) {
	m, _ := workerModel(160, 40)
	foot := lines(m.View())
	if !strings.Contains(foot[len(foot)-1], "t trust") {
		t.Errorf("footer: %q", foot[len(foot)-1])
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if !strings.Contains(strings.Join(lines(m.View()), "\n"), "cycle request rule") {
		t.Error("help lacks the t key")
	}
	_ = protocol.OpSessionTrust
}
