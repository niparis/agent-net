package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestHelpScrollsAndWrapsAt80(t *testing.T) {
	m := keys(newModel(80, 24), "?")
	var seen strings.Builder
	for i := 0; i < 100; i++ {
		v := ansi.Strip(m.View())
		if strings.Contains(v, "…") {
			t.Fatalf("help has an ellipsis:\n%s", v)
		}
		seen.WriteString(v)
		seen.WriteByte('\n')
		m = keys(m, "down")
	}
	all := seen.String()
	for _, line := range m.helpContent(80) {
		line = strings.TrimSpace(ansi.Strip(line))
		if line != "" && !strings.Contains(all, line) {
			t.Errorf("help line unreachable: %q", line)
		}
	}
	for _, want := range []string{"more below", "more above", "delegation policy:", "Messages", "left/right: message", "Conversation", "Everywhere", "Reading the screen", "a figure that is missing", "the first line of the body"} {
		if !strings.Contains(all, want) {
			t.Errorf("help did not show %q", want)
		}
	}
	m = keys(m, "up")
	if !strings.Contains(ansi.Strip(m.View()), "more above") {
		t.Fatal("up did not scroll back")
	}
}

func TestToolsHelpScrolls(t *testing.T) {
	m := keys(newModel(80, 24), "5", "?")
	m = send(m, tea.WindowSizeMsg{Width: 80, Height: 10})
	var seen strings.Builder
	for i := 0; i < 30; i++ {
		seen.WriteString(ansi.Strip(m.View()))
		m = keys(m, "j")
	}
	for _, want := range []string{"Tools keys", "switch screens", "search or filter", "close help", "more below", "more above"} {
		if !strings.Contains(seen.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestHelpShowsWaitingAndNotificationSettings(t *testing.T) {
	m := keys(newModel(80, 24), "?")
	for _, want := range []string{"desktop notifications: on (toggle)", "answer a waiting session, else open its conversation", "open the conversation"} {
		if !strings.Contains(ansi.Strip(m.View()), want) {
			t.Errorf("help lacks %q", want)
		}
	}
	m.snap.NotificationsDisabled = true
	if !strings.Contains(ansi.Strip(m.View()), "desktop notifications: off (toggle)") {
		t.Fatal("help does not show disabled notifications")
	}
}

func TestToolsHelpWrapsAndEveryLineCanBeReached(t *testing.T) {
	m := keys(newModel(40, 10), "5", "?")
	var seen strings.Builder
	for i := 0; i < 100; i++ {
		v := ansi.Strip(m.View())
		if strings.Contains(v, "…") {
			t.Fatalf("tools help has an ellipsis:\n%s", v)
		}
		seen.WriteString(v)
		seen.WriteByte('\n')
		m = keys(m, "j")
	}
	for _, line := range m.helpContent(40) {
		line = strings.TrimSpace(ansi.Strip(line))
		if line != "" && !strings.Contains(seen.String(), line) {
			t.Errorf("tools help line unreachable: %q", line)
		}
	}
	if !strings.Contains(seen.String(), "more below") || !strings.Contains(seen.String(), "more above") {
		t.Fatal("tools scroll indicators missing")
	}
}
