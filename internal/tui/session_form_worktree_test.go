package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
)

// The Folder switch of the New session form, and the busy-folder default
// (docs/design/start-sessions.md, build plan part 9).

func openSessionFolder(m Model) (Model, tea.Cmd) {
	m, _ = press(m, sessionKey())
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyUp}) // First message -> Folder
	return m, nil
}

func TestNewSessionFolderSwitchChangesTheStartPlan(t *testing.T) {
	m, src := workerModel(120, 34)
	m = selectID(m, "s_0000000000000002") // strategies, idle, project folder free
	m, _ = press(m, sessionKey())
	if m.sform.folder != protocol.FolderProject {
		t.Fatalf("folder %q, want the project folder", m.sform.folder)
	}
	if v := m.View(); strings.Contains(v, "Branch") {
		t.Fatalf("the project-folder form shows a branch line:\n%s", v)
	}

	m, _ = press(m, tea.KeyMsg{Type: tea.KeyUp}) // First message -> Folder
	if m.sform.cur != sFolder {
		t.Fatalf("focus %v, want Folder", m.sform.cur)
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.sform.folder != protocol.FolderWorktree {
		t.Fatalf("right did not switch to a worktree: %q", m.sform.folder)
	}
	if got := m.sform.value(sBranch); got != "agent-net/strategies-1" {
		t.Fatalf("branch %q, want the lowest free number", got)
	}
	view := m.View()
	for _, want := range []string{"new worktree", "Branch", "agent-net/strategies-1", "from HEAD"} {
		if !strings.Contains(view, want) {
			t.Errorf("the worktree form does not show %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "from HEAD") && !strings.Contains(view, "from main (HEAD)") {
		t.Errorf("the worktree form does not say what the branch starts from:\n%s", view)
	}

	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEnter}) // Folder -> Branch
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEnter}) // Branch -> First message
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter gave no command")
	}
	m, _ = press2(m, cmd())
	if len(src.sessionStarts) != 1 {
		t.Fatalf("session_start asked %+v", src.sessionStarts)
	}
	if got := src.sessionStarts[0]; got.Folder != protocol.FolderWorktree || got.Branch != "agent-net/strategies-1" {
		t.Fatalf("start plan %+v, want a new worktree on agent-net/strategies-1", got)
	}
}

func TestNewSessionProjectFolderPlanHasNoBranch(t *testing.T) {
	m, src := workerModel(120, 34)
	m = selectID(m, "s_0000000000000002")
	m, _ = press(m, sessionKey())
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter gave no command")
	}
	m, _ = press2(m, cmd())
	if len(src.sessionStarts) != 1 {
		t.Fatalf("session_start asked %+v", src.sessionStarts)
	}
	if got := src.sessionStarts[0]; got.Folder != protocol.FolderProject || got.Branch != "" {
		t.Fatalf("start plan %+v, want the project folder and no branch", got)
	}
}

func TestNewSessionBusyFolderDefaultsToWorktree(t *testing.T) {
	m, src := workerModel(120, 34)
	m = selectID(m, "s_0000000000000001") // tenebrae, busy in the project folder
	m, _ = press(m, sessionKey())
	if m.sform.folder != protocol.FolderWorktree {
		t.Fatalf("busy folder default %q, want a new worktree", m.sform.folder)
	}
	if got := m.sform.value(sBranch); got != "agent-net/tenebrae-1" {
		t.Fatalf("branch %q", got)
	}
	view := m.View()
	for _, want := range []string{"new worktree", "tenebrae is busy in the project folder", "from main (HEAD)", "agent-net/tenebrae-1"} {
		if !strings.Contains(view, want) {
			t.Errorf("the busy form does not show %q:\n%s", want, view)
		}
	}

	// Left on the focused Folder sets it back to the project folder, and the
	// plan follows. Left elsewhere in the form does not change the folder.
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyUp}) // First message -> Branch
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyUp}) // Branch -> Folder
	if m.sform.cur != sFolder {
		t.Fatalf("focus %v, want Folder", m.sform.cur)
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.sform.folder != protocol.FolderProject {
		t.Fatalf("left did not return to the project folder: %q", m.sform.folder)
	}
	if v := m.View(); strings.Contains(v, "agent-net/tenebrae-1") {
		t.Errorf("the project-folder form still shows the branch line:\n%s", v)
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEnter}) // Folder -> First message
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter gave no command")
	}
	m, _ = press2(m, cmd())
	if len(src.sessionStarts) != 1 {
		t.Fatalf("session_start asked %+v", src.sessionStarts)
	}
	if got := src.sessionStarts[0]; got.Folder != protocol.FolderProject || got.Branch != "" {
		t.Fatalf("start plan %+v, want the project folder", got)
	}
}

func TestNewSessionWorktreeBranchUsesTheLowestFreeNumber(t *testing.T) {
	m, _ := workerModel(120, 34)
	for i := range m.snap.Projects {
		if m.snap.Projects[i].Name == "tenebrae" {
			m.snap.Projects[i].DefaultBranch = "main"
			m.snap.Projects[i].Worktrees = []string{
				"/Users/n/code/tenebrae.worktrees/agent-net/tenebrae-1",
				"/Users/n/code/tenebrae.worktrees/agent-net/tenebrae-2",
			}
		}
	}
	m = selectID(m, "s_0000000000000001")
	m, _ = press(m, sessionKey())
	if got := m.sform.value(sBranch); got != "agent-net/tenebrae-3" {
		t.Fatalf("branch %q, want agent-net/tenebrae-3 (1 and 2 are taken)", got)
	}
}

func TestNewSessionWorktreeGoldens(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		name := func(s string) string { return "view_" + itoa(size.w) + "x" + itoa(size.h) + "_" + s }

		// Tenebrae is busy in the project folder: the form opens on a new
		// worktree, with the branch line and the reason.
		m, _ := workerModel(size.w, size.h)
		m = selectID(m, "s_0000000000000001")
		m, _ = press(m, sessionKey())
		golden(t, name("new_session_worktree"), m.View())

		// Strategies is idle in a linked worktree: the project folder is free,
		// so the switch stays on it and there is no branch line.
		m, _ = workerModel(size.w, size.h)
		m = selectID(m, "s_0000000000000002")
		m, _ = press(m, sessionKey())
		golden(t, name("new_session_folder"), m.View())
	}
}
