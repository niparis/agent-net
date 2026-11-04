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
	m.tab = quotasTab
	m.snap.WorkerLog = []protocol.WorkerLogEntry{{StartedAt: 123, Title: "Inspect history", Model: "test:high", Status: "running", Hostname: "test-machine", Project: "demo", Harness: "codex", Caller: "Agent One", CallerHost: "test-machine", Assessment: &protocol.WorkerAssessment{Reasoning: 3, D: 2.1, Label: "normal", JudgeModel: "judge", Reasons: map[string]string{"reasoning": "history checks"}, PickerLabel: "selected normal"}}}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	if !m.workersLog {
		t.Fatal("tab did not switch")
	}
	for _, w := range []int{200, 120, 80, 45} {
		lines := m.workersLogView(w, 35)
		got := ansi.Strip(strings.Join(lines, "\n"))
		for _, want := range []string{"Workers log", "Inspect history", "normal", "D 2.10", "judge", "history checks", "test-machine", "Agent One", "Agent One (test-machine)", "3/5"} {
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
	if got := ansi.Strip(strings.Join(m.workersLogView(120, 35), "\n")); strings.Contains(got, "Assessment: -") {
		t.Fatalf("empty assessment should be hidden: %s", got)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(Model)
	if m.workerLogDetail != 1 {
		t.Fatal("details did not scroll")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	if m.workersLog || !m.delivery.on {
		t.Fatal("tab did not move on to Delivery")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m = next.(Model); m.workersLog || m.delivery.on {
		t.Fatal("tab did not return to Quotas")
	}
}

func TestWorkersLogGoldens(t *testing.T) {
	m := New(&fakeSource{snap: fakeSnapshot()})
	m.tab = quotasTab
	m.snap = fakeSnapshot()
	m.haveData = true
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.workersLog = true
	a := &protocol.WorkerAssessment{Reasoning: 3, Scope: 2, Coupling: 2, Uncertainty: 1, Verification: 2, Risk: 1, D: 2.1, Label: "normal", JudgeModel: "fake/judge", Reasons: map[string]string{"reasoning": "Join stored runs and current sessions.", "scope": "One read method and one sub-tab.", "coupling": "Operator gate and TUI refresh.", "uncertainty": "Known table schema.", "verification": "Gateway and TUI tests.", "risk": "Read only history."}, PickerLabel: "normal"}
	for i, status := range []string{"running", "done", "blocked", "failed", "timed out", "stopped"} {
		callers := []struct{ name, host string }{
			{"owner", "test-machine"},
			{"Agent One", "caller-machine"},
			{"CTO", "owner-machine"},
			{"Agent One", ""},
			{"CTO", ""},
			{"", ""},
		}
		m.snap.WorkerLog = append(m.snap.WorkerLog, protocol.WorkerLogEntry{StartedAt: 1700000000 - int64(i)*100, Title: "Review worker history and difficulty scores", Model: "test-model:high", Status: status, Difficulty: "normal", Hostname: "test-machine", Harness: "codex", Project: "demo", Caller: callers[i].name, CallerHost: callers[i].host, RunSeconds: 120, Assessment: a})
	}
	m.snap.WorkerLog[0].ModelNote = "codex test-model:high (34.5, good enough; eligible for this task and available on the chosen machine)"
	m.snap.WorkerLog[1].Failure = "the worker ended after its hand-back; a message from you starts it again 1 automatic continue was sent."
	m.snap.WorkerLog[1].ModelNote = "difficulty R3 S2 C2 U1 V2 K1 · D 2.10 · normal -> codex test-model:high (34.5, good enough; eligible for this task and available on the chosen machine)"
	m.snap.WorkerLog[3].Failure = "the model call failed: the provider was unavailable"
	m.snap.WorkerLog[5].Assessment = nil
	m.workerLogRow = 1
	for _, size := range [][2]int{{120, 40}, {80, 40}} {
		m.width, m.height = size[0], size[1]
		golden(t, fmt.Sprintf("workers_log_%dx%d", size[0], size[1]), m.View())
	}
	// A judge failure has a distinct compact layout with no score rows.
	m.snap.WorkerLog[1].Assessment = &protocol.WorkerAssessment{Label: "normal", D: 1.2, Error: "judge timed out while evaluating the worker run; retry later"}
	m.workerLogRow = 1
	m.width, m.height = 120, 40
	golden(t, "workers_log_judge_failed_120x40", m.View())
	m.workerLogRow = 3
	golden(t, "workers_log_failed_120x40", m.View())
}

func TestWrapHangingIndent(t *testing.T) {
	got := wrapHanging("Reasoning 4/5 ▰▰▰▰▱ ", "The explanation wraps cleanly beneath its text", 32)
	want := []string{
		"Reasoning 4/5 ▰▰▰▰▱ The",
		"                    explanation",
		"                    wraps",
		"                    cleanly",
		"                    beneath its",
		"                    text",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("wrapHanging() =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestWorkerLogModelParts(t *testing.T) {
	for _, test := range []struct{ harness, model, want string }{
		{"codex", "test-model:high", "codex · test-model · high"},
		{"opencode", "deepseek/deepseek-v4.1:low", "opencode · deepseek · deepseek-v4.1 · low"},
	} {
		if got := workerLogModel(test.harness, test.model); got != test.want {
			t.Errorf("workerLogModel(%q, %q) = %q, want %q", test.harness, test.model, got, test.want)
		}
	}
}

func TestWorkerPickerNoteStripsOnlyMatchingDifficulty(t *testing.T) {
	assessment := &protocol.WorkerAssessment{Reasoning: 3, Scope: 2, Coupling: 2, Uncertainty: 1, Verification: 2, Risk: 1, D: 2.1, Label: "normal"}
	choice := "codex test-model:high (34.5, good enough; eligible for this task and available on the chosen machine)"
	matching := "difficulty " + assessment.DifficultyLine() + " -> " + choice
	if got := workerPickerNote(matching, assessment); got != choice {
		t.Fatalf("matching picker note = %q, want %q", got, choice)
	}
	raw := "difficulty normal -> " + choice
	if got := workerPickerNote(raw, assessment); got != raw {
		t.Fatalf("unmatched picker note = %q, want raw note %q", got, raw)
	}
}

func TestWorkersLogNoteOnlyForFailedStatus(t *testing.T) {
	m := New(&fakeSource{snap: fakeSnapshot()})
	m.tab = quotasTab
	m.workersLog = true
	m.snap.WorkerLog = []protocol.WorkerLogEntry{
		{StartedAt: 1700000000, Title: "Done run", Status: "done", Failure: "the worker ended after its hand-back"},
		{StartedAt: 1699999900, Title: "Failed run", Status: "failed", Failure: "the provider was unavailable"},
	}
	got := ansi.Strip(strings.Join(m.workersLogView(120, 40), "\n"))
	if !strings.Contains(got, "Note: the worker ended after its hand-back") || strings.Contains(got, "Failure: the worker ended after its hand-back") {
		t.Fatalf("done run should show a note: %s", got)
	}
	m.workerLogRow = 1
	got = ansi.Strip(strings.Join(m.workersLogView(120, 40), "\n"))
	if !strings.Contains(got, "Failure: the provider was unavailable") || strings.Contains(got, "Note: the provider was unavailable") {
		t.Fatalf("failed run should show a failure: %s", got)
	}
}

func TestWorkersLogBelongsToQuotasAndPickerNote(t *testing.T) {
	m := New(&fakeSource{snap: fakeSnapshot()})
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if next.(Model).workersLog {
		t.Fatal("Sessions toggled the workers log")
	}
	m.tab = quotasTab
	m.snap.WorkerLog = []protocol.WorkerLogEntry{{Title: "Stored run", Status: "done", Assessment: &protocol.WorkerAssessment{Label: "normal", PickerLabel: "normal"}}}
	m.workersLog = true
	got := ansi.Strip(strings.Join(m.quotasView(80, 30), "\n"))
	if strings.Contains(got, "Picker:") {
		t.Fatalf("missing picker should be hidden: %s", got)
	}
	if strings.Contains(got, "Caller:") {
		t.Fatalf("missing caller should be hidden: %s", got)
	}
	m.snap.WorkerLog[0].ModelNote = "codex gpt-6.1-sol:low (34.5, good enough; a stored picker explanation that wraps across the detail box)"
	got = ansi.Strip(strings.Join(m.quotasView(80, 50), "\n"))
	if !strings.Contains(got, "Picker") || !strings.Contains(got, "codex gpt-6.1-sol:low") || !strings.Contains(got, "detail box)") {
		t.Fatalf("stored picker note missing: %s", got)
	}
	m.workersLog = false
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = next.(Model)
	if !m.quotaShowUnset || m.workersLog {
		t.Fatal("s no longer toggles other subscriptions")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = next.(Model)
	if !m.qualityDetail || m.workersLog {
		t.Fatal("d no longer opens quality details")
	}
}
