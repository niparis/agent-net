package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

// The picker note of a cost pick shows each candidate's cost in the worker log
// detail at both sizes the TUI is checked at, and no line is too wide.
func TestWorkersLogShowsPickerCostAtBothSizes(t *testing.T) {
	note := "bucket 2.67-3.00: deepseek 1.00 [0.90-1.00] n13, lowest expected cost; " +
		"qualifying: opus 0.86 [0.78-0.94] n31 $1.40/run, p 0.55, $2.03 expected, " +
		"deepseek 1.00 [0.90-1.00] n13 $0.07/run, p 0.46, $0.11 expected"
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := New(&fakeSource{snap: fakeSnapshot()})
		m.tab = quotasTab
		m.workersLog = true
		m.haveData = true
		m.loc, m.now = time.UTC, func() time.Time { return t0 }
		m.width, m.height = size[0], size[1]
		m.snap.WorkerLog = []protocol.WorkerLogEntry{{
			StartedAt: 1700000000, Title: "Price a normal task", Model: "test-model:high", Status: "done",
			Difficulty: "normal", Harness: "codex",
			Assessment: &protocol.WorkerAssessment{D: 2.83, Label: "normal", PickerLabel: "normal"},
			ModelNote:  note,
		}}
		next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		m = next.(Model)
		got := ansi.Strip(m.testView())
		for i := 0; i < 40 && !strings.Contains(got, "$0.11"); i++ {
			next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			m = next.(Model)
			got = ansi.Strip(m.testView())
		}
		for _, want := range []string{"$1.40/run", "p 0.55", "$0.11", "$0.07/run", "p 0.46"} {
			if !strings.Contains(got, want) {
				t.Fatalf("%dx%d: the picker cost %q is not on screen:\n%s", size[0], size[1], want, got)
			}
		}
		t.Logf("%dx%d:\n%s", size[0], size[1], got)
		for _, line := range strings.Split(got, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Errorf("%dx%d line overflow: %q", size[0], size[1], line)
			}
		}
	}
}
