package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
)

func keyI() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("I")} }

func spaceKey() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")} }

func TestIntegrationsFormTogglesAndSavesForTheNetwork(t *testing.T) {
	m, src := workerModel(120, 40)
	src.intg = protocol.Integrations{HintHook: true}
	src.intgSet = true

	m, cmd := press(m, keyI())
	if m.mode != modeIntegrations || cmd == nil {
		t.Fatalf("I must open the form and read the answers, mode %v", m.mode)
	}
	m = send(m, cmd())
	screen := strings.Join(lines(m.View()), "\n")
	for _, want := range []string{"Integrations (all machines)", "[x] Claude: hint which session to ask after an error", "[ ] Claude: accept messages from your other agents"} {
		if !strings.Contains(screen, want) {
			t.Errorf("form lacks %q:\n%s", want, screen)
		}
	}

	// Turn all-on on, and the hint hook off.
	m, _ = press(m, spaceKey())
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = press(m, spaceKey())
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter must save")
	}
	msg := cmd()
	if _, ok := msg.(integrationsSavedMsg); !ok {
		t.Fatalf("save gave %T", msg)
	}
	m = send(m, msg)
	if m.mode != modeNormal || m.notice.err {
		t.Fatalf("mode %v notice %+v", m.mode, m.notice)
	}
	if len(src.intgSaved) != 1 {
		t.Fatalf("saved %d times", len(src.intgSaved))
	}
	got := src.intgSaved[0]
	if !got.PeerMessages || got.HintHook {
		t.Errorf("saved %+v", got)
	}
	if !strings.Contains(m.notice.text, "within a minute") {
		t.Errorf("notice: %q", m.notice.text)
	}
}

func TestIntegrationsFormEscCancels(t *testing.T) {
	m, src := workerModel(120, 40)
	m, cmd := press(m, keyI())
	m = send(m, cmd())
	m, _ = press(m, spaceKey())
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeNormal || len(src.intgSaved) != 0 {
		t.Fatalf("esc saved or stayed in the form: mode %v saved %d", m.mode, len(src.intgSaved))
	}
}
