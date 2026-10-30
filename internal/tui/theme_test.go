package tui

import (
	"github.com/charmbracelet/lipgloss"
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
