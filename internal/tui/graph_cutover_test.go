package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

type cutoverSource struct {
	fakeSource
	err   error
	calls int
}

func (s *cutoverSource) Call(_ context.Context, method string, _ any) (json.RawMessage, error) {
	if method == "graph_refs_enable" {
		s.calls++
		return nil, s.err
	}
	return json.RawMessage(`{"on":false,"blockers":[]}`), nil
}
func TestGraphCutoverStates(t *testing.T) {
	s := &cutoverSource{}
	m := newModel(120, 34)
	m.src = s
	m.tab = settingsTab
	m.cutoverLoaded = true
	m.cutoverSuggested = "/gateway-state/gateway-before-typed-graph-20261106-1200.db"
	m = selectSettingsKind(m, "graph-cutover")
	if !strings.Contains(m.testView(), "Typed Repo/Agent Graph: off") {
		t.Fatal(m.testView())
	}
	m.cutoverBlockers = []string{"upgrade required: machine fixture has not reported typed Graph support"}
	if !strings.Contains(m.testView(), "machine fixture") {
		t.Fatal(m.testView())
	}
	m.cutoverBlockers = nil
	m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(m.testView(), "restoring the backup") {
		t.Fatal(m.testView())
	}
	golden(t, "graph_cutover_confirm_120x34", m.testView())
	s.err = errors.New("upgrade required: machine fixture has not reported typed Graph support")
	m, cmd := press(m, tea.KeyPressMsg{Text: "y", Code: 'y'})
	if cmd == nil {
		t.Fatal("no cutover call")
	}
	next, _ := m.Update(cmd())
	m = next.(Model)
	if !strings.Contains(m.testView(), "upgrade required: machine fixture") {
		t.Fatal(m.testView())
	}
	s.err = nil
	m.mode = modeGraphCutoverConfirm
	m, cmd = press(m, tea.KeyPressMsg{Text: "y", Code: 'y'})
	next, _ = m.Update(cmd())
	m = next.(Model)
	if !m.cutoverOn || !strings.Contains(m.testView(), "Backup: /gateway-state/gateway-before-typed-graph-20261106-1200.db") {
		t.Fatal(m.testView())
	}
	m.mode = modeNormal
	m = selectSettingsKind(m, "graph-cutover")
	if !strings.Contains(m.testView(), "Typed Repo/Agent Graph: on") {
		t.Fatal(m.testView())
	}
	m, cmd = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil || s.calls != 2 {
		t.Fatal("enabled cutover ran again")
	}
}

func TestGraphCutoverStatusDoesNotUndoSuccess(t *testing.T) {
	m := newModel(80, 24)
	m.cutoverOn = true
	m.cutoverEpoch = 1
	next, _ := m.Update(graphCutoverStatusMsg{on: false})
	if !next.(Model).cutoverOn {
		t.Fatal("late status made an enabled cutover look off")
	}
}

func TestGraphCutoverKeepsEnabledOnStatusError(t *testing.T) {
	m := newModel(80, 24)
	m.cutoverLoaded, m.cutoverOn = true, true
	next, _ := m.Update(graphCutoverStatusMsg{err: errors.New("offline")})
	m = next.(Model)
	if !m.cutoverLoaded || !m.cutoverOn {
		t.Fatal("lost known enabled state")
	}
}

func TestGraphCutoverManyBlockers(t *testing.T) {
	m := newModel(80, 24)
	m.tab = settingsTab
	m.mode = modeGraphCutoverPath
	m.rename = newTextInput()
	m.rename.Prompt = "Backup path on the gateway> "
	for i := 0; i < 30; i++ {
		m.cutoverBlockers = append(m.cutoverBlockers, fmt.Sprintf("machine %02d needs upgrade", i))
	}
	if !strings.Contains(m.testView(), "Backup path") {
		t.Fatal("blockers hid the backup prompt")
	}
	for i := 0; i < 30; i++ {
		m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	if !strings.Contains(m.testView(), "machine 29") {
		t.Fatal("cannot view last blocker")
	}
}

func TestGraphCutoverPrefillsGatewayBackup(t *testing.T) {
	m := newModel(120, 34)
	m.tab = settingsTab
	next, _ := m.Update(graphCutoverStatusMsg{backup: "/gateway-state/gateway-before-typed-graph-20261106-1200.db"})
	m = next.(Model)
	m = selectSettingsKind(m, "graph-cutover")
	m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.rename.Value() != "/gateway-state/gateway-before-typed-graph-20261106-1200.db" {
		t.Fatalf("prefill %q", m.rename.Value())
	}
}
