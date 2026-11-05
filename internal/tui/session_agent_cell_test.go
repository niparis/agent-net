package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

type agentCellSource struct {
	*fakeSource
	set []protocol.SessionAgentSetRequest
}

func (s *agentCellSource) Call(_ context.Context, method string, p any) (json.RawMessage, error) {
	switch method {
	case protocol.OpSessionAgentList:
		return json.Marshal(protocol.SessionAgentOptions{Agents: []protocol.SessionAgentOption{{ID: "agent-opaque-home", Name: "Home"}, {ID: "agent-opaque-advisor", Name: "Advisor", Role: "architect"}}})
	case protocol.OpSessionAgentSet:
		s.set = append(s.set, p.(protocol.SessionAgentSetRequest))
		return json.RawMessage(`{"ok":true}`), nil
	}
	return json.RawMessage(`{}`), nil
}

func TestSessionAgentCellAt80AndOwnerChoice(t *testing.T) {
	m, base := workerModel(80, 24)
	src := &agentCellSource{fakeSource: base}
	m.src = src
	m = selectID(m, "s_0000000000000001")
	for i := range m.snap.Agents {
		if m.snap.Agents[i].SessionID == m.selected {
			m.snap.Agents[i].AgentID = "agent-opaque-home"
			m.snap.Agents[i].AgentName = "Home"
		}
	}
	for i := 0; i < 3; i++ {
		m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyRight})
	}
	if m.sessionCell != 3 {
		t.Fatalf("cell %d, want agent", m.sessionCell)
	}
	view := ansi.Strip(m.testView())
	if !strings.Contains(view, "AGENT") || !strings.Contains(view, "Home") || !strings.Contains(view, "space") || strings.Contains(view, "agent-opaque-home") {
		t.Fatalf("agent cell missing at 80x24:\n%s", view)
	}
	golden(t, "view_80x24_session_agent_cell", m.testView())
	_, cmd := press(m, tea.KeyPressMsg{Code: tea.KeySpace})
	if cmd == nil {
		t.Fatal("agent cell did not start an assignment")
	}
	msg := cmd()
	if a, ok := msg.(actionMsg); !ok || a.err != nil {
		t.Fatalf("result %#v", msg)
	}
	if len(src.set) != 1 || src.set[0].SessionID != m.selected || src.set[0].Agent != "agent-opaque-advisor" {
		t.Fatalf("assignments %+v", src.set)
	}
}

func TestSessionAgentCellSkipsWorker(t *testing.T) {
	m, base := workerModel(80, 24)
	src := &agentCellSource{fakeSource: base}
	m.src = src
	for _, a := range m.snap.Agents {
		if a.Worker != nil && a.Online {
			m = selectID(m, a.SessionID)
			break
		}
	}
	a, ok := m.selectedAgent()
	if !ok || a.Worker == nil {
		t.Fatal("no worker selected")
	}
	for i := 0; i < 4; i++ {
		m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyRight})
	}
	if m.sessionCell == 3 {
		t.Fatal("worker exposed agent assignment")
	}
	if _, cmd := m.cycleSessionAgent(); cmd != nil {
		t.Fatal("worker got assignment command")
	}
}
