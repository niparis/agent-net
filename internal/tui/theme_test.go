package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"strings"
	"testing"
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
