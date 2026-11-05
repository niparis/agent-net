package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The Sessions table shows at a glance whether a session has a terminal: a
// "◐" before the name means it runs in the background. A foreground session,
// or one whose harness does not report its mode, gets no mark.

func sessionRow(t *testing.T, table, name string) string {
	t.Helper()
	for _, line := range lines(table) {
		if strings.Contains(line, name) {
			return line
		}
	}
	t.Fatalf("no row for %q in:\n%s", name, table)
	return ""
}

func TestSessionsMarkBackgroundRuns(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := newModel(size[0], size[1])
		table := ansi.Strip(strings.Join(m.sessionsTable(size[0], 12), "\n"))
		for _, tc := range []struct {
			name string
			want bool
		}{
			{"tenebrae-53", false}, // claude in a terminal
			{"strategies", true},   // claude --bg
			{"web", false},         // opencode does not report its mode
			{"ci-runner", true},    // pi, no terminal
		} {
			row := sessionRow(t, table, tc.name)
			if got := strings.Contains(row, "◐"); got != tc.want {
				t.Errorf("%d columns, %s: marked %v, want %v in %q", size[0], tc.name, got, tc.want, row)
			}
		}
	}
}

func TestBackgroundMarkerKeepsTheTreeGlyphs(t *testing.T) {
	m := treeModel(120, 34, treeFixture())
	table := ansi.Strip(strings.Join(m.sessionsTable(120, 20), "\n"))
	if !strings.Contains(table, "├─ backtest-a") || !strings.Contains(table, "└─ backtest-on-a-remo") {
		t.Fatalf("the worker tree glyphs changed:\n%s", table)
	}
	// A worker is always background and the tree already says so: no second mark.
	if line := sessionRow(t, table, "backtest-a"); strings.Contains(line, "◐") {
		t.Errorf("a nested worker is marked twice: %q", line)
	}
	// Its caller is a plain background session, so it does carry the mark.
	if line := sessionRow(t, table, "strategies"); !strings.Contains(line, "◐ strategies") {
		t.Errorf("the background caller is not marked: %q", line)
	}
}

func TestDetailSaysHowTheSessionRuns(t *testing.T) {
	m := newModel(120, 34)
	for _, tc := range []struct {
		id, want string
	}{
		{"s_0000000000000001", "runs     in a terminal"},
		{"s_0000000000000002", "runs     in the background (claude --bg)"},
		{"s_0000000000000004", "runs     in the background (pi, no terminal)"},
	} {
		m.selected = tc.id
		if v := detailText(m); !strings.Contains(v, tc.want) {
			t.Errorf("%s: detail lacks %q:\n%s", tc.id, tc.want, v)
		}
	}
	// OpenCode never reports the mode: say nothing rather than guess.
	m.selected = "s_0000000000000003"
	if v := detailText(m); strings.Contains(v, "runs ") {
		t.Errorf("an unknown mode got a runs line:\n%s", v)
	}
}

func TestWorkerDetailSaysItRunsInTheBackground(t *testing.T) {
	m, _ := workerModel(120, 34)
	m = selectID(m, workerID)
	if v := detailText(m); !strings.Contains(v, "runs     in the background (worker)") {
		t.Fatalf("the worker detail lacks its run mode:\n%s", v)
	}
}

func TestHelpExplainsTheBackgroundMarker(t *testing.T) {
	v := keys(newModel(120, 40), "?").testView()
	for _, want := range []string{"◐", "runs in the background", "no terminal"} {
		if !strings.Contains(v, want) {
			t.Errorf("the help screen lacks %q:\n%s", want, v)
		}
	}
}
