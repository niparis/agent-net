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
	err         error
	calls       int
	statusOn    bool
	statusErr   error
	statusCalls int
}

func (s *cutoverSource) Call(_ context.Context, method string, _ any) (json.RawMessage, error) {
	if method == "graph_refs_enable" {
		s.calls++
		return nil, s.err
	}
	s.statusCalls++
	if s.statusErr != nil {
		return nil, s.statusErr
	}
	return json.RawMessage(fmt.Sprintf(`{"on":%t,"blockers":[],"backup":"/saved/backup"}`, s.statusOn)), nil
}
func TestGraphCutoverStates(t *testing.T) {
	s := &cutoverSource{}
	m := newModel(120, 34)
	m.src = s
	m.tab = settingsTab
	m.cutoverLoaded = true
	m.cutoverSuggested = "/gateway-state/gateway-before-typed-graph-20261106-1200"
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
	if !strings.Contains(m.testView(), "Backup folder: /gateway-state/gateway-before-typed-graph-20261106-1200") || !strings.Contains(m.testView(), "Holds the gateway database, skills and keys.") || !strings.Contains(m.testView(), "restoring the backup") {
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
	if !m.cutoverOn || !strings.Contains(m.testView(), "Backup: /gateway-state/gateway-before-typed-graph-20261106-1200") {
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
	next, _ := m.Update(graphCutoverStatusMsg{backup: "/gateway-state/gateway-before-typed-graph-20261106-1200"})
	m = next.(Model)
	m = selectSettingsKind(m, "graph-cutover")
	m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.rename.Value() != "/gateway-state/gateway-before-typed-graph-20261106-1200" {
		t.Fatalf("prefill %q", m.rename.Value())
	}
}

func TestGraphCutoverConfirmRequiresY(t *testing.T) {
	s := &cutoverSource{}
	m := newModel(80, 24)
	m.src = s
	m.tab = settingsTab
	m.mode = modeGraphCutoverConfirm
	m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil || m.mode != modeGraphCutoverConfirm || s.calls != 0 {
		t.Fatal("Enter enabled cutover")
	}
	for _, key := range []string{"y", "Y"} {
		n := m
		n, cmd = press(n, tea.KeyPressMsg{Text: key, Code: rune(key[0])})
		if cmd == nil || !n.cutoverBusy {
			t.Fatalf("%s did not confirm", key)
		}
	}
}
func TestGraphCutoverReconcilesEnableError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		on        bool
		statusErr error
		want      string
	}{
		{"completed", true, nil, "Typed Repo/Agent Graph is on"},
		{"pending", false, nil, "no answer in time; it may still complete: check this screen again"},
		{"unknown", false, errors.New("offline"), "may still complete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &cutoverSource{err: context.DeadlineExceeded, statusOn: tc.on, statusErr: tc.statusErr}
			m := newModel(120, 34)
			m.src = s
			m.tab = settingsTab
			m.mode = modeGraphCutoverConfirm
			m.cutoverPath = "/saved/backup"
			m, cmd := press(m, tea.KeyPressMsg{Text: "y", Code: 'y'})
			if cmd == nil {
				t.Fatal("no enable command")
			}
			next, _ := m.Update(cmd())
			m = next.(Model)
			if s.statusCalls != 1 || !strings.Contains(m.testView(), tc.want) {
				t.Fatalf("status calls %d: %s", s.statusCalls, m.testView())
			}
			if tc.on && !strings.Contains(m.testView(), "/saved/backup") {
				t.Fatal(m.testView())
			}
			if tc.statusErr != nil && !strings.Contains(m.testView(), "context deadline exceeded") {
				t.Fatal("original error hidden: " + m.testView())
			}
		})
	}
}

func TestGraphCutoverScreens80(t *testing.T) {
	m := newModel(80, 24)
	m.tab = settingsTab
	m.cutoverLoaded = true
	m.cutoverSuggested = "/gateway/backups/pre-cutover"
	m.cutoverBlockers = []string{"upgrade required: machine build-vm has not reported typed Graph support"}
	m = selectSettingsKind(m, "graph-cutover")
	golden(t, "graph_cutover_status_80x24", m.testView())
	m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	golden(t, "graph_cutover_confirm_80x24", m.testView())
}

func TestGraphCutoverSanitizesOutput(t *testing.T) {
	m := newModel(120, 34)
	m.tab = settingsTab
	m.mode = modeGraphCutoverConfirm
	m.cutoverPath = "/backup/\x1b[31munsafe\nnext"
	if strings.Contains(m.View().Content, "\x1b[31m") || strings.Contains(m.testView(), "unsafe\nnext") {
		t.Fatal("unsafe path in confirmation")
	}
	m.mode = modeGraphCutoverResult
	m.cutoverError = "error\x1b[31m bad\nnext"
	if strings.Contains(m.View().Content, "\x1b[31m") || strings.Contains(m.testView(), "bad\nnext") {
		t.Fatal("unsafe error in result")
	}
}
