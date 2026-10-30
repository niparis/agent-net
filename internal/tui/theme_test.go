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
