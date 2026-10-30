package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func keyRunes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// typeText types s into the open prompt, one key at a time.
func typeName(m Model, s string) Model {
	for _, r := range s {
		m, _ = press(m, keyRunes(string(r)))
	}
	return m
}

func TestRenameKeySavesTheMachineName(t *testing.T) {
	m, src := workerModel(160, 40)
	const target = "s_0000000000000001"
	m = selectID(m, target)
	a, _ := m.selectedAgent()
	m, cmd := press(m, keyRunes("N"))
	if m.mode != modeRename {
		t.Fatalf("N must open the prompt: mode %v", m.mode)
	}
	// The prompt starts with the name shown now.
	if got := m.rename.Value(); got == "" {
		t.Fatal("the prompt is empty: it must start with the current name")
	}
	foot := lines(m.View())
	if !strings.Contains(foot[len(foot)-1], "enter saves, empty resets, esc cancels") {
		t.Errorf("prompt line: %q", foot[len(foot)-1])
	}
	// esc cancels without a call.
	if mm, _ := press(m, tea.KeyMsg{Type: tea.KeyEsc}); mm.mode != modeNormal || len(src.renames) != 0 {
		t.Fatalf("esc: %v %v", mm.mode, src.renames)
	}
	// A bad name keeps the prompt open and calls nothing.
	m.rename.SetValue("")
	m = typeName(m, "bad name!")
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeRename || cmd != nil || len(src.renames) != 0 || !m.notice.err {
		t.Fatalf("bad name: mode %v renames %v notice %+v", m.mode, src.renames, m.notice)
	}
	m.rename.SetValue("")
	m = typeName(m, "laptop-1")
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeNormal {
		t.Fatalf("enter must close the prompt: %v", m.mode)
	}
	m = finish(t, m, cmd)
	if len(src.renames) != 1 || src.renames[0] != (renameCall{a.NodeID, "laptop-1"}) || m.notice.err {
		t.Fatalf("rename: %v %+v", src.renames, m.notice)
	}
	if !strings.Contains(m.notice.text, "laptop-1") {
		t.Errorf("notice: %q", m.notice.text)
	}
}

func TestRenameWithEmptyNameResetsAndUnchangedDoesNothing(t *testing.T) {
	m, src := workerModel(160, 40)
	m = selectID(m, "s_0000000000000001")
	a, _ := m.selectedAgent()
	for i := range src.snap.Nodes {
		if src.snap.Nodes[i].NodeID == a.NodeID {
			src.snap.Nodes[i].Hostname, src.snap.Nodes[i].DisplayName = "laptop-1", "laptop-1"
		}
	}
	m = refresh(m, src)
	m, _ = press(m, keyRunes("N"))
	if m.rename.Value() != "laptop-1" {
		t.Fatalf("prefill: %q", m.rename.Value())
	}
	m.rename.SetValue("")
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = finish(t, m, cmd)
	if len(src.renames) != 1 || src.renames[0] != (renameCall{a.NodeID, ""}) {
		t.Fatalf("reset: %v", src.renames)
	}

	// With no owner name, saving the prompt as it opened changes nothing.
	for i := range src.snap.Nodes {
		src.snap.Nodes[i].DisplayName = ""
	}
	m = refresh(m, src)
	m, _ = press(m, keyRunes("N"))
	m.rename.SetValue(m.renameShown)
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || m.mode != modeNormal || len(src.renames) != 1 {
		t.Fatalf("unchanged: %v %v", cmd != nil, src.renames)
	}
}

func TestRenameErrorAndHelp(t *testing.T) {
	m, src := workerModel(160, 40)
	src.renameErr = errTest("another machine is already called laptop-1")
	m = selectID(m, "s_0000000000000001")
	m, _ = press(m, keyRunes("N"))
	m.rename.SetValue("laptop-1")
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = send(m, cmd())
	if !m.notice.err || !strings.Contains(m.notice.text, "already called") {
		t.Errorf("notice: %+v", m.notice)
	}
	m, _ = press(m, keyRunes("?"))
	if !strings.Contains(strings.Join(lines(m.View()), "\n"), "name the machine of the session") {
		t.Error("help lacks the N key")
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }
