package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestTrustNeedsASelectedSession(t *testing.T) {
	empty := &fakeSource{snap: Snapshot{Connected: true}}
	m := New(empty)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m, _ = openSessionSetting(m, 0)
	if !m.notice.err {
		t.Fatal("missing selection should fail")
	}
}

func TestTrustKeyIsInHelp(t *testing.T) {
	m, _ := workerModel(160, 40)
	foot := lines(m.testView())
	if hints := strings.Split(ansi.Strip(foot[len(foot)-1]), "  "); len(hints) > 7 {
		t.Errorf("footer has too many hints: %q", foot[len(foot)-1])
	}
	m, _ = press(m, tea.KeyPressMsg{Text: "?", Code: []rune("?")[0]})
	if !strings.Contains(strings.Join(m.helpContent(160), "\n"), "cycle the request rule") {
		t.Error("help lacks the settings menu rule")
	}
	_ = protocol.OpSessionTrust
}
