package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
)

// The Harness and Model switches of the New session form. Left and right act
// only on the field that has the focus, and the model follows the harness.

func focusSessionField(m Model, to sessionFormField) Model {
	if m.sform.cur != to {
		m.sform.focus(to) // test setup only; the key paths have their own tests
	}
	return m
}

func TestNewSessionHarnessCyclesInFixedOrder(t *testing.T) {
	m, _ := workerModel(120, 34)
	m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{
		{Name: "pi"}, {Name: "opencode"}, {Name: "codex"}, {Name: "claude"},
	}
	m = selectID(m, "s_0000000000000002") // strategies, claude, on Mac
	m, _ = press(m, sessionKey())
	m = focusSessionField(m, sHarness)
	if got := m.sform.value(sHarness); got != "claude" {
		t.Fatalf("harness %q, want the selected row's harness", got)
	}
	folder := m.sform.folder
	for i, want := range []string{"codex", "opencode", "pi", "claude"} {
		m, _ = press(m, tea.KeyMsg{Type: tea.KeyRight})
		if got := m.sform.value(sHarness); got != want {
			t.Fatalf("right %d gave harness %q, want %q", i+1, got, want)
		}
		if m.sform.folder != folder {
			t.Fatalf("right on Harness changed Folder to %q", m.sform.folder)
		}
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyLeft})
	if got := m.sform.value(sHarness); got != "pi" {
		t.Fatalf("left from claude gave harness %q, want pi", got)
	}
}

func TestNewSessionFolderKeysOnlyActOnFolder(t *testing.T) {
	m, _ := workerModel(120, 34)
	m = selectID(m, "s_0000000000000002")
	m, _ = press(m, sessionKey())
	harness := m.sform.value(sHarness)
	m = focusSessionField(m, sFolder)
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.sform.folder != protocol.FolderWorktree {
		t.Fatalf("right on Folder gave %q, want a new worktree", m.sform.folder)
	}
	if got := m.sform.value(sHarness); got != harness {
		t.Fatalf("right on Folder changed harness from %q to %q", harness, got)
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.sform.folder != protocol.FolderProject {
		t.Fatalf("left on Folder gave %q, want the project folder", m.sform.folder)
	}
	if got := m.sform.value(sHarness); got != harness {
		t.Fatalf("left on Folder changed harness from %q to %q", harness, got)
	}
}

func TestNewSessionModelKeysDoNotChangeFolder(t *testing.T) {
	m, _ := workerModel(120, 34)
	m = selectID(m, "s_0000000000000002")
	m, _ = press(m, sessionKey())
	m = focusSessionField(m, sModel)
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.sform.folder != protocol.FolderProject {
		t.Fatalf("right on Model changed Folder to %q", m.sform.folder)
	}
	if got := m.sform.value(sHarness); got != "claude" {
		t.Fatalf("right on Model changed harness to %q", got)
	}
}

func TestNewSessionFourUpsReachHarness(t *testing.T) {
	m, _ := workerModel(120, 34)
	m = selectID(m, "s_0000000000000001") // busy, so the form shows Branch
	m, _ = press(m, sessionKey())
	if !m.sform.worktree() {
		t.Fatal("the busy project did not open on a worktree, so Branch is hidden")
	}
	if m.sform.cur != sFirstMessage {
		t.Fatalf("focus %v, want First message", m.sform.cur)
	}
	for i := 1; i <= 4; i++ {
		m, _ = press(m, tea.KeyMsg{Type: tea.KeyUp})
		if i < 4 && m.sform.cur == sHarness {
			t.Fatalf("harness reached after %d Up presses", i)
		}
	}
	if m.sform.cur != sHarness {
		t.Fatalf("four Up presses gave focus %v, want Harness", m.sform.cur)
	}
}

func TestNewSessionTypingOnHarnessDoesNothing(t *testing.T) {
	m, _ := workerModel(120, 34)
	m = selectID(m, "s_0000000000000002")
	m, _ = press(m, sessionKey())
	m = focusSessionField(m, sHarness)
	want := m.sform.value(sHarness)
	for _, k := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("x")},
		{Type: tea.KeyBackspace},
		{Type: tea.KeyDelete},
		{Type: tea.KeySpace},
		{Type: tea.KeyRunes, Runes: []rune("pi")},
	} {
		m, _ = press(m, k)
	}
	if got := m.sform.value(sHarness); got != want {
		t.Fatalf("typing changed harness from %q to %q", want, got)
	}
	if !strings.Contains(m.View(), "◂ "+want+" ▸") {
		t.Fatalf("the form does not show Harness as a choice:\n%s", m.View())
	}
}

func TestNewSessionHarnessChangeResetsAndSuppliesModels(t *testing.T) {
	m, _ := workerModel(120, 34)
	for i := range m.snap.Nodes[0].Harnesses {
		switch m.snap.Nodes[0].Harnesses[i].Name {
		case "claude":
			m.snap.Nodes[0].Harnesses[i].Models = []string{"claude-a", "claude-b"}
		case "opencode":
			m.snap.Nodes[0].Harnesses[i].Models = []string{"oc-a", "oc-b"}
		}
	}
	m = selectID(m, "s_0000000000000002")
	m, _ = press(m, sessionKey())
	m = focusSessionField(m, sHarness)
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRight}) // claude -> opencode (no codex on Mac)
	if got := m.sform.value(sHarness); got != "opencode" {
		t.Fatalf("harness %q, want opencode", got)
	}
	if got := m.sform.value(sModel); got != "" {
		t.Fatalf("model %q after the harness change, want the harness default", got)
	}
	m = focusSessionField(m, sModel)
	for i, want := range []string{"oc-a", "oc-b", "", "oc-a"} {
		m, _ = press(m, tea.KeyMsg{Type: tea.KeyRight})
		if got := m.sform.value(sModel); got != want {
			t.Fatalf("right %d on Model gave %q, want %q", i+1, got, want)
		}
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyLeft})
	if got := m.sform.value(sModel); got != "" {
		t.Fatalf("left from the first model gave %q, want the default", got)
	}
	// A second harness change still resets the model.
	m = focusSessionField(m, sHarness)
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRight}) // opencode -> pi
	if got := m.sform.value(sHarness); got != "pi" {
		t.Fatalf("harness %q, want pi", got)
	}
	if got := m.sform.value(sModel); got != "" {
		t.Fatalf("model %q after the second harness change, want the default", got)
	}
}

func TestNewSessionMachineWithoutPiDoesNotOfferPi(t *testing.T) {
	m, _ := workerModel(120, 34)
	m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{
		{Name: "claude"}, {Name: "codex"}, {Name: "opencode"},
	}
	m = selectID(m, "s_0000000000000002")
	m, _ = press(m, sessionKey())
	m = focusSessionField(m, sHarness)
	seen := []string{m.sform.value(sHarness)}
	for i := 0; i < 5; i++ {
		m, _ = press(m, tea.KeyMsg{Type: tea.KeyRight})
		seen = append(seen, m.sform.value(sHarness))
	}
	for _, h := range seen {
		if h == "pi" {
			t.Fatalf("a machine without pi offered pi: %v", seen)
		}
	}
	want := "claude codex opencode claude codex opencode"
	if got := strings.Join(seen, " "); got != want {
		t.Fatalf("harness cycle %q, want %q", got, want)
	}
}

func TestNewSessionHarnessUsesTheOtherMachinesList(t *testing.T) {
	m, _ := workerModel(120, 34)
	m = selectID(m, "s_0000000000000004") // ci-runner, pi, on build-vm
	m, _ = press(m, sessionKey())
	m = focusSessionField(m, sHarness)
	if got := m.sform.value(sHarness); got != "pi" {
		t.Fatalf("harness %q, want pi", got)
	}
	for i := 0; i < 4; i++ {
		m, _ = press(m, tea.KeyMsg{Type: tea.KeyRight})
		if got := m.sform.value(sHarness); got != "pi" {
			t.Fatalf("right %d offered %q, but build-vm reports only pi", i+1, got)
		}
	}
}

func TestNewSessionProjectTabCompletes(t *testing.T) {
	m, _ := workerModel(120, 34)
	m = selectID(m, "s_0000000000000002")
	m, _ = press(m, sessionKey())
	m = focusSessionField(m, sProject)
	m.sform.in[sProject].SetValue("strat")
	m.sform.in[sProject].CursorEnd()
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyTab})
	if got := m.sform.value(sProject); got != "strategies" {
		t.Fatalf("tab completed Project to %q, want strategies", got)
	}
}

func TestNewSessionBranchTakesTextAndMoveKeys(t *testing.T) {
	m, _ := workerModel(120, 34)
	m = selectID(m, "s_0000000000000002")
	m, _ = press(m, sessionKey())
	m.setSessionFolder(protocol.FolderWorktree)
	m = focusSessionField(m, sBranch)
	before := m.sform.value(sBranch)
	m = typeText(m, "x")
	if got := m.sform.value(sBranch); got != before+"x" {
		t.Fatalf("typing on Branch gave %q, want %q", got, before+"x")
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyLeft})
	if got := m.sform.value(sBranch); got != before+"x" {
		t.Fatalf("left edited Branch: %q", got)
	}
	if m.sform.folder != protocol.FolderWorktree {
		t.Fatalf("left on Branch changed Folder to %q", m.sform.folder)
	}
}

func TestNewSessionChoiceGoldens(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		name := func(s string) string { return "view_" + itoa(size.w) + "x" + itoa(size.h) + "_" + s }

		m, _ := workerModel(size.w, size.h)
		for i := range m.snap.Nodes[0].Harnesses {
			switch m.snap.Nodes[0].Harnesses[i].Name {
			case "claude":
				m.snap.Nodes[0].Harnesses[i].Models = []string{"claude-a", "claude-b"}
			case "opencode":
				m.snap.Nodes[0].Harnesses[i].Models = []string{"oc-a", "oc-b"}
			}
		}
		m = selectID(m, "s_0000000000000002")
		m, _ = press(m, sessionKey())
		m = focusSessionField(m, sHarness)
		golden(t, name("new_session_harness"), m.View())

		m, _ = press(m, tea.KeyMsg{Type: tea.KeyRight}) // claude -> opencode
		m = focusSessionField(m, sModel)
		golden(t, name("new_session_model"), m.View())
	}
}
