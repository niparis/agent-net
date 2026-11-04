package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestHelpListsSettings(t *testing.T) {
	m := keys(newModel(80, 24), "6", "?")
	text := strings.Join(strings.Fields(ansi.Strip(strings.Join(m.helpContent(80), "\n"))), " ")
	for _, want := range []string{"Settings", "integrations", "desktop notifications", "restart tips"} {
		if !strings.Contains(text, want) {
			t.Errorf("help lacks %q", want)
		}
	}
}

func TestHelpScrollsAndWrapsAt80(t *testing.T) {
	m := keys(newModel(80, 24), "?", "?")
	var seen strings.Builder
	for i := 0; i < len(m.helpContent(80))+20; i++ {
		v := ansi.Strip(m.testView())
		if strings.Contains(v, "…") {
			t.Fatalf("help has an ellipsis:\n%s", v)
		}
		seen.WriteString(v)
		seen.WriteByte('\n')
		m = keys(m, "down")
	}
	all := strings.Join(strings.Fields(seen.String()), " ")
	for _, line := range m.helpContent(80) {
		line = strings.Join(strings.Fields(ansi.Strip(line)), " ")
		if line != "" && !strings.Contains(all, line) {
			t.Errorf("help line unreachable: %q", line)
		}
	}
	for _, want := range []string{"more below", "more above", "delegation policy", "Messages", "select the previous or next message", "In overlays", "states", "open the selected message body"} {
		if !strings.Contains(all, want) {
			t.Errorf("help did not show %q", want)
		}
	}
	m = keys(m, "up")
	if !strings.Contains(ansi.Strip(m.testView()), "more above") {
		t.Fatal("up did not scroll back")
	}
}

func TestToolsHelpScrolls(t *testing.T) {
	m := keys(newModel(80, 24), "5", "tab", "?")
	m = send(m, tea.WindowSizeMsg{Width: 80, Height: 10})
	var seen strings.Builder
	for i := 0; i < len(m.helpContent(80))+20; i++ {
		seen.WriteString(ansi.Strip(m.testView()))
		m = keys(m, "j")
	}
	for _, want := range []string{"Keys · Tools", "add an MCP server or a skill from a folder", "open the actions for the selected tool", "? all screens", "esc close", "more below", "more above"} {
		if !strings.Contains(seen.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestToolsHelpWrapsAndEveryLineCanBeReached(t *testing.T) {
	m := keys(newModel(40, 10), "5", "?")
	var seen strings.Builder
	for i := 0; i < len(m.helpContent(40))+20; i++ {
		v := ansi.Strip(m.testView())
		if strings.Contains(v, "…") {
			t.Fatalf("tools help has an ellipsis:\n%s", v)
		}
		seen.WriteString(v)
		seen.WriteByte('\n')
		m = keys(m, "j")
	}
	seenText := strings.Join(strings.Fields(seen.String()), " ")
	for _, line := range m.helpContent(40) {
		line = strings.Join(strings.Fields(ansi.Strip(line)), " ")
		if line != "" && !strings.Contains(seenText, line) {
			t.Errorf("tools help line unreachable: %q", line)
		}
	}
	if !strings.Contains(seen.String(), "more below") || !strings.Contains(seen.String(), "more above") {
		t.Fatal("tools scroll indicators missing")
	}
}
