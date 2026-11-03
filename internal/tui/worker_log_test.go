package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkersLogSwitchAndDetails(t *testing.T) {
	m := New(&fakeSource{snap: fakeSnapshot()})
	m.snap.WorkerLog = []protocol.WorkerLogEntry{{StartedAt: 123, Title: "Inspect history", Model: "test:high", Status: "running", Hostname: "test-machine", Project: "demo", Harness: "codex", Assessment: &protocol.WorkerAssessment{Reasoning: 3, D: 2.1, Label: "normal", JudgeModel: "judge", Reasons: map[string]string{"reasoning": "history checks"}, PickerLabel: "selected normal"}}}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	if !m.workersLog {
		t.Fatal("tab did not switch")
	}
	for _, w := range []int{120, 80, 45} {
		lines := m.workersLogView(w, 35)
		got := ansi.Strip(strings.Join(lines, "\n"))
		for _, want := range []string{"Workers log", "Inspect history", "R3", "judge", "history checks", "test-machine"} {
			if !strings.Contains(got, want) {
				t.Errorf("width %d missing %q: %s", w, want, got)
			}
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > w {
				t.Errorf("width %d overflow: %q", w, line)
			}
		}
	}
	m.snap.WorkerLog[0].Assessment = nil
	if got := ansi.Strip(strings.Join(m.workersLogView(120, 35), "\n")); !strings.Contains(got, "Assessment: -") {
		t.Fatal(got)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(Model)
	if m.workerLogDetail != 1 {
		t.Fatal("details did not scroll")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if next.(Model).workersLog {
		t.Fatal("tab did not return")
	}
}

func TestWorkersLogGoldens(t *testing.T) {
	m := New(&fakeSource{snap: fakeSnapshot()})
	m.snap = fakeSnapshot()
	m.haveData = true
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.workersLog = true
	a := &protocol.WorkerAssessment{Reasoning: 3, Scope: 2, Coupling: 2, Uncertainty: 1, Verification: 2, Risk: 1, D: 2.1, Label: "normal", JudgeModel: "fake/judge", Reasons: map[string]string{"reasoning": "Join stored runs and current sessions.", "scope": "One read method and one sub-tab.", "coupling": "Operator gate and TUI refresh.", "uncertainty": "Known table schema.", "verification": "Gateway and TUI tests.", "risk": "Read only history."}, PickerLabel: "normal"}
	for i, status := range []string{"running", "done", "blocked", "failed", "timed out", "stopped"} {
		m.snap.WorkerLog = append(m.snap.WorkerLog, protocol.WorkerLogEntry{StartedAt: 1700000000 - int64(i)*100, Title: "Review worker history and difficulty scores", Model: "test-model:high", Status: status, Difficulty: "normal", Hostname: "test-machine", Harness: "codex", Project: "demo", RunSeconds: 120, Assessment: a})
	}
	m.snap.WorkerLog[5].Assessment = nil
	for _, size := range [][2]int{{120, 40}, {80, 40}} {
		m.width, m.height = size[0], size[1]
		golden(t, fmt.Sprintf("workers_log_%dx%d", size[0], size[1]), m.View())
	}
}
