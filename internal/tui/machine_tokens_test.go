package tui

import (
	"context"
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type machineSource struct {
	fakeSource
	id, action string
}

func (s *machineSource) MachineAction(_ context.Context, id, action string) error {
	s.id, s.action = id, action
	return nil
}

func TestMachineActionsConfirmNamedTarget(t *testing.T) {
	for _, key := range []string{"x", "r"} {
		src := &machineSource{fakeSource: fakeSource{snap: fakeSnapshot()}}
		m := newModel(140, 34)
		m.src = src
		m.tab = settingsTab
		m = selectSettingsKind(m, "machine")
		target := m.machineRows()[m.settingsItems()[m.settingsRow].index]
		m, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		if m.mode != modeConfirm || cmd != nil {
			t.Fatalf("%s did not ask: mode %v", key, m.mode)
		}
		if !strings.Contains(ansi.Strip(m.helpLine(140)), target.host) {
			t.Fatal("confirmation does not name machine")
		}
		cancelled, _ := press(m, tea.KeyMsg{Type: tea.KeyEsc})
		if cancelled.mode != modeNormal || src.id != "" {
			t.Fatal("cancel acted")
		}
		_, cmd = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
		if cmd == nil {
			t.Fatal("confirm did not act")
		}
		cmd()
		if src.id != target.nodeID {
			t.Fatalf("wrong machine %q", src.id)
		}
	}
}

func TestMachineOfflineRotationRefused(t *testing.T) {
	m := newModel(140, 34)
	m.tab = settingsTab
	for i := range m.snap.Nodes {
		m.snap.Nodes[i].Online = false
	}
	for i := range m.snap.Agents {
		m.snap.Agents[i].Online = false
	}
	m = selectSettingsKind(m, "machine")
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd != nil || m.mode != modeNormal || !strings.Contains(m.notice.text, "offline") {
		t.Fatalf("offline rotation mode=%v notice=%s", m.mode, m.notice.text)
	}
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if cmd != nil || m.mode != modeConfirm {
		t.Fatal("offline removal unavailable")
	}
}

func TestMachineProposalNamesTarget(t *testing.T) {
	m := newModel(140, 34)
	target := m.machineRows()[0]
	for _, action := range []string{"machine_remove", "machine_rotate"} {
		args, _ := json.Marshal(map[string]string{"node_id": target.nodeID})
		p := protocol.CTOProposal{Tool: "settings_change", Action: action, Args: args}
		if got := m.inboxProposalSummary(p); !strings.Contains(got, target.host) {
			t.Fatalf("proposal omits machine: %s", got)
		}
	}
}
