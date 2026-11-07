package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkersLogSwitchAndDetails(t *testing.T) {
	m := New(&fakeSource{snap: fakeSnapshot()})
	m.tab = quotasTab
	m.snap.WorkerLog = []protocol.WorkerLogEntry{{StartedAt: 123, Title: "Inspect history", Model: "test:high", Status: "running", Hostname: "test-machine", Project: "demo", Harness: "codex", Caller: "Agent One", CallerHost: "test-machine", Assessment: &protocol.WorkerAssessment{Reasoning: 3, D: 2.1, Label: "normal", JudgeModel: "judge", Reasons: map[string]string{"reasoning": "history checks"}, PickerLabel: "selected normal"}}}
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = next.(Model)
	if !m.workersLog {
		t.Fatal("tab did not switch")
	}
	for _, w := range []int{200, 120, 80, 45} {
		lines := m.workersLogView(w, 35)
		got := ansi.Strip(strings.Join(lines, "\n"))
		for _, want := range []string{"Workers log", "Inspect history", "normal", "D 2.10", "judge", "history checks", "test-machine", "Agent One", "Agent One (test-machine)", "3"} {
			if !strings.Contains(got, want) {
				t.Errorf("width %d missing %q: %s", w, want, got)
			}
		}
		if strings.Contains(got, "###") || strings.Contains(got, "3/5") {
			t.Errorf("score should be a plain number, width %d: %s", w, got)
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
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = next.(Model)
	if m.workerLogDetail != 0 {
		t.Fatal("right should focus details without scrolling")
	}
	footer := ansi.Strip(m.testView())
	if !strings.Contains(footer, "↑↓ scroll details") || !strings.Contains(footer, "← runs") {
		t.Fatalf("detail focus hints missing: %s", footer)
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = next.(Model)
	if m.workerLogDetail != 1 {
		t.Fatal("down did not scroll focused details")
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = next.(Model)
	footer = ansi.Strip(m.testView())
	if !strings.Contains(footer, "→ details") || strings.Contains(footer, "↑↓ scroll details") {
		t.Fatalf("list focus hints missing after left: %s", footer)
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = next.(Model)
	if m.workersLog {
		t.Fatal("tab did not return to Quotas")
	}

}

func TestWorkersLogResponsivePanelsAndOverflow(t *testing.T) {
	m := New(&fakeSource{snap: fakeSnapshot()})
	m.tab = quotasTab
	m.workersLog = true
	m.haveData = true
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m.snap.WorkerLog = []protocol.WorkerLogEntry{{
		StartedAt: 1700000000, Title: "Review one worker run", Model: "test-model:high", Status: "done", Difficulty: "normal", Harness: "codex",
		Assessment: &protocol.WorkerAssessment{Reasoning: 3, Scope: 2, Coupling: 4, Uncertainty: 1, Verification: 5, Risk: 2, D: 2.6, Label: "normal", Reasons: map[string]string{
			"reasoning": "Check stored runs.", "scope": "One view.", "coupling": "Shared TUI state.", "uncertainty": "Known schema.", "verification": "TUI tests.", "risk": "Read only.",
		}},
		ModelNote: "codex test-model:high (34.5, good enough; no quota pace reading; rotation weight 1.05; deepseek opencode: 4, sol low codex: 3 of the last 10 normal starts; skipped sonnet claude: protected provider (anthropic), saved for your orchestrator; terra codex excluded: its max is hard (automatic picks may be at most one level below their max); sol kept for harder work; terra max kept for harder work)",
	}}

	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m.width, m.height = size[0], size[1]
		view := m.workersLogView(size[0], size[1])
		got := ansi.Strip(m.testView())
		if rows := workerLogPanelRows(view, "Workers log"); rows != 3 {
			t.Errorf("%dx%d worker list has %d rows, want the 3-row minimum", size[0], size[1], rows)
		}
		for _, want := range []string{"normal  D 2.60", "Reasoning", "Scope", "Coupling", "Uncertainty", "Verification", "Risk"} {
			if !strings.Contains(got, want) {
				t.Errorf("%dx%d missing %q: %s", size[0], size[1], want, got)
			}
		}
		if !strings.Contains(got, "↓ ") || !strings.Contains(got, " more lines") {
			t.Errorf("%dx%d missing the overflow cue: %s", size[0], size[1], got)
		}
		if size[1] == 34 && !strings.Contains(got, "Chosen") {
			t.Errorf("120x34 should show the chosen model without scrolling: %s", got)
		}
		for _, line := range strings.Split(got, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Errorf("%dx%d line overflow: %q", size[0], size[1], line)
			}
		}
	}
}

func workerLogPanelRows(view []string, title string) int {
	start, end := -1, -1
	for i, line := range view {
		plain := ansi.Strip(line)
		if strings.HasPrefix(plain, "╭─"+title) {
			start = i
			continue
		}
		if start >= 0 && strings.HasPrefix(plain, "╰") {
			end = i
			break
		}
	}
	if start < 0 || end < start {
		return 0
	}
	return end - start - 1
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
	allRuns := append([]protocol.WorkerLogEntry(nil), m.snap.WorkerLog...)
	for _, size := range [][2]int{{120, 40}, {80, 40}, {80, 24}, {120, 34}, {160, 40}} {
		m.snap.WorkerLog = allRuns
		m.workerLogRow = 1
		if size == [2]int{80, 24} || size == [2]int{120, 34} {
			m.snap.WorkerLog = allRuns[:1]
			m.workerLogRow = 0
		}
		m.width, m.height = size[0], size[1]
		golden(t, fmt.Sprintf("workers_log_%dx%d", size[0], size[1]), m.testView())
	}
	// A judge failure has a distinct compact layout with no score rows.
	m.snap.WorkerLog[1].Assessment = &protocol.WorkerAssessment{Label: "normal", D: 1.2, Error: "judge timed out while evaluating the worker run; retry later"}
	m.workerLogRow = 1
	m.width, m.height = 120, 40
	golden(t, "workers_log_judge_failed_120x40", m.testView())
	m.workerLogRow = 3
	golden(t, "workers_log_failed_120x40", m.testView())
}

func TestWrapHangingIndent(t *testing.T) {
	got := wrapHanging("Reasoning     4  ", "The explanation wraps cleanly beneath its text", 32)
	want := []string{
		"Reasoning     4  The explanation",
		"                 wraps cleanly",
		"                 beneath its",
		"                 text",
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
	if !strings.Contains(got, "Note        the worker ended after its hand-back") || strings.Contains(got, "Failure     the worker ended after its hand-back") {
		t.Fatalf("done run should show a note: %s", got)
	}
	m.workerLogRow = 1
	got = ansi.Strip(strings.Join(m.workersLogView(120, 40), "\n"))
	if !strings.Contains(got, "Failure     the provider was unavailable") || strings.Contains(got, "Note        the provider was unavailable") {
		t.Fatalf("failed run should show a failure: %s", got)
	}
}

func TestWorkersLogBelongsToQuotasAndPickerNote(t *testing.T) {
	m := New(&fakeSource{snap: fakeSnapshot()})
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
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
	next, _ = m.Update(tea.KeyPressMsg{Text: "s", Code: []rune("s")[0]})
	m = next.(Model)
	if !m.quotaShowUnset || m.workersLog {
		t.Fatal("s no longer toggles other subscriptions")
	}
	next, _ = m.Update(tea.KeyPressMsg{Text: "d", Code: []rune("d")[0]})
	m = next.(Model)
	if !m.qualityDetail || m.workersLog {
		t.Fatal("d no longer opens quality details")
	}
}

func TestWorkersLogNarrowKeepsTaskTitle(t *testing.T) {
	r := protocol.WorkerLogEntry{
		StartedAt:  time.Date(2025, 11, 14, 22, 13, 0, 0, time.UTC).Unix(),
		Title:      "Review worker history and difficulty scores",
		Model:      "test-model:high",
		Difficulty: "normal",
		Caller:     "Agent One",
		Status:     "running",
	}
	line := ansi.Strip(workerLogLine(r, 80, false, time.UTC))
	for _, want := range []string{"22:13", "test-model", "norm", "Agent One", "Review worker history and", "running"} {
		if !strings.Contains(line, want) {
			t.Errorf("narrow worker log line is missing %q: %q", want, line)
		}
	}
	for _, gone := range []string{"11-14", "test-model:high", "normal"} {
		if strings.Contains(line, gone) {
			t.Errorf("narrow worker log line still shows %q: %q", gone, line)
		}
	}
	if ansi.StringWidth(line) > 80 {
		t.Errorf("line is %d columns wide: %q", ansi.StringWidth(line), line)
	}
	if !strings.Contains(ansi.Strip(workerLogLine(r, 120, false, time.UTC)), "11-14 22:13") {
		t.Errorf("wide worker log line lost its date: %q", ansi.Strip(workerLogLine(r, 120, false, time.UTC)))
	}
}

func TestWorkerPickerStructured(t *testing.T) {
	r := protocol.WorkerLogEntry{ModelNote: "codex gpt-6-luna:xhigh (23.3, good enough; eligible for this task and available on the chosen machine; codex 0% ahead of pace (week, 33% used at 33% elapsed); rotation weight 0.85; skipped sonnet claude: protected provider (anthropic), saved for your orchestrator; luna codex: 4, deepseek opencode: 3, terra-low: 2, terra medium codex: 1 of the last 10 simple starts; sonnet claude excluded: its max is hard (automatic picks may be at most one level below their max); deepseek opencode kept for harder work; terra codex excluded: its max is hard (automatic picks may be at most one level below their max); sol codex excluded: its max is hard (automatic picks may be at most one level below their max); terra max codex excluded: its max is very_complex (automatic picks may be at most one level below their max); terra-low kept for harder work; terra medium codex kept for harder work)"}
	r.Title = "Secret check: sk- false match inside words"
	r.Harness, r.Model, r.Status = "codex", "gpt-6-luna:xhigh", "done"
	r.Failure = "the worker ended after its hand-back; a message from you starts it again"
	r.Caller, r.CallerHost = "Agent One", "caller-machine"
	r.Hostname, r.Project, r.RunSeconds = "test-machine", "demo", 120
	r.Assessment = &protocol.WorkerAssessment{Label: "simple", D: 2.05, JudgeModel: "opencode-go/deepseek-v4.1-flash",
		Reasoning: 2, Scope: 2, Coupling: 3, Uncertainty: 2, Verification: 1, Risk: 2,
		Reasons: map[string]string{"reasoning": "Check substring boundaries.", "scope": "One secret matcher.", "coupling": "Shared validation path.", "uncertainty": "Known false match.", "verification": "Focused unit tests.", "risk": "Preserve valid detections."}}
	got := ansi.Strip(strings.Join(workerLogDetails(r, 116), "\n"))
	if !strings.Contains(got, "Chosen") || strings.Contains(got, "Picker") || strings.Count(strings.ToLower(got), "automatic picks") != 1 {
		t.Fatal(got)
	}
	for _, want := range []string{"Not picked", "sonnet", "protected provider (anthropic), saved for your orchestrator", "Pace        codex", "Rotation    weight 0.85", "simple starts:", "luna codex 4, deepseek opencode 3", "(last 10)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
	for _, size := range [][2]int{{120, 90}, {80, 90}, {80, 24}, {120, 34}, {160, 40}} {
		m := New(&fakeSource{snap: fakeSnapshot()})
		m.tab = quotasTab
		m.workersLog = true
		m.haveData = true
		m.loc = time.UTC
		m.snap.WorkerLog = []protocol.WorkerLogEntry{r}
		m.width, m.height = size[0], size[1]
		golden(t, fmt.Sprintf("workers_log_picker_%dx%d", size[0], size[1]), m.testView())
	}
}
