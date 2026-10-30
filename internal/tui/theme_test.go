package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"strings"
	"testing"
	"time"
)

func TestThemeRolesProduceColor(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.TrueColor)
	if !strings.Contains(sNeeds.Render("wait"), "\x1b[") {
		t.Fatal("warning has no colour")
	}
	if roleAccent == nil {
		t.Fatal("missing accent role")
	}
}

func TestSharedPieces(t *testing.T) {
	if got := statusLabel("busy"); !strings.Contains(got, "busy") {
		t.Fatalf("status: %q", got)
	}
	if got := panel("Machines", []string{"one"}, 20, roleAccent); len(got) != 3 || !strings.Contains(got[0], "Machines") {
		t.Fatalf("panel: %q", got)
	}
	if got := keyHints("q", "quit"); !strings.Contains(got, "q quit") {
		t.Fatalf("keys: %q", got)
	}
	if got := progressBar(.5, 4, roleGood); !strings.Contains(got, "██░░") {
		t.Fatalf("bar: %q", got)
	}
}

func TestSessionsUsePanels(t *testing.T) {
	v := newModel(80, 24).View()
	for _, want := range []string{"╭─Machines", "╭─CTO", "╰"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
}

func TestCTOHintFitsAndProgressShowsTrickle(t *testing.T) {
	for _, line := range newModel(80, 24).ctoView(80, 7) {
		if strings.Contains(line, "write to the CTO:") && strings.Contains(line, "…") {
			t.Fatal("CTO hint is clipped")
		}
	}
	if !strings.Contains(progressBar(.01, 4, roleGood), "█") {
		t.Fatal("nonzero progress is invisible")
	}
}

func TestPaddedBusyStatusHasBackground(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.TrueColor)
	if !strings.Contains(statusLabel("busy  "), ";48;") {
		t.Fatalf("padded busy status has no background: %q", statusLabel("busy  "))
	}
}

func TestSelectedSessionKeepsColors(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.TrueColor)
	m := newModel(120, 34)
	v := m.View()
	if !strings.Contains(v, "▸") {
		t.Fatal("missing selection marker")
	}
	if !strings.Contains(v, "48;2;34;48;58") {
		t.Fatal("missing subtle selection background")
	}
	for _, line := range strings.Split(v, "\n") {
		if strings.Contains(line, "▸") && strings.Count(line, "48;2;34;48;58") < 3 {
			t.Fatal("selection background is lost after styled cells")
		}
	}
}

func TestMachineDotColors(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.TrueColor)
	m := newModel(120, 34)
	rows := m.machineRows()
	for _, r := range rows {
		line := m.machineLine(r, 120, 12, 8)
		if r.online && !strings.Contains(line, "38;2;124;207;138m●") {
			t.Fatalf("online dot: %q", line)
		}
	}
	m.now = func() time.Time { return t0.Add(2 * time.Hour) }
	for _, r := range m.machineRows() {
		if _, st := m.nodeUsage(r.nodeID); r.online && st == usageStale && !strings.Contains(m.machineLine(r, 120, 12, 8), sNeeds.Render("●")) {
			t.Fatalf("stale dot is not amber: %q", m.machineLine(r, 120, 12, 8))
		}
	}
}

func TestMachineLoadColors(t *testing.T) {
	if loadStyle(warmMachine).GetForeground() != roleWarning {
		t.Fatal("warm load should use warning")
	}
	if loadStyle(hotMachine).GetForeground() != roleError {
		t.Fatal("hot load should use error")
	}
}

func TestCTOTitleAndFocusedBorder(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.TrueColor)
	m := newModel(120, 34)
	m.cto.harness, m.cto.model = "claude", "sonnet"
	unfocused := m.ctoView(120, 10)[0]
	if !strings.Contains(ansi.Strip(unfocused), "CTO · claude · sonnet") {
		t.Fatalf("title: %q", unfocused)
	}
	m.focus = focusCTO
	if m.ctoView(120, 10)[0] == unfocused {
		t.Fatal("focused border did not change")
	}
}

func TestMachineHarnessesAreNamesOnly(t *testing.T) {
	m := newModel(160, 34)
	for _, r := range m.machineRows() {
		if r.nodeID == "n_mac" {
			line := ansi.Strip(m.machineLine(r, 160, 12, 8))
			if !strings.Contains(line, "claude  opencode  pi") || strings.Contains(line, "2.1.312") {
				t.Fatal(line)
			}
			return
		}
	}
	t.Fatal("missing machine")
}

func TestPanelPadsAndClipsInnerWidth(t *testing.T) {
	lines := panel("title", []string{"abcdefghi", "x"}, 10, roleAccent)
	if got := ansi.Strip(lines[1]); got != "│ abcde… │" {
		t.Fatalf("long body: %q", got)
	}
	if got := ansi.Strip(lines[2]); got != "│ x      │" {
		t.Fatalf("short body: %q", got)
	}
}

func TestMachinePanelDoesNotClipPadding(t *testing.T) {
	m := newModel(80, 24)
	m.usage.have = false
	for _, line := range strings.Split(m.View(), "\n") {
		if strings.Contains(line, "Mac (here)") && strings.Contains(line, "cpu ?") && strings.Contains(line, "…") {
			t.Fatalf("machine row clipped padding: %q", line)
		}
	}
}
