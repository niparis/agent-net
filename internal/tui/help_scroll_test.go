package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// The help must not carry its own copy of a count: it names the number the
// integrations form really draws.
func TestHelpIntegrationCountMatchesTheCode(t *testing.T) {
	m := keys(newModel(80, 24), "?")
	text := strings.Join(strings.Fields(ansi.Strip(strings.Join(m.helpContent(80), "\n"))), " ")
	want := fmt.Sprintf("integrations for all machines: %d checkboxes (a form)", len(protocol.IntegrationSpecs()))
	if !strings.Contains(text, want) {
		t.Fatalf("help does not name the real number of integrations:\nwant %q in\n%s", want, text)
	}
}

// Notifications are an overlay setting, so the line belongs in the last
// section that lists the help key itself.
func TestHelpNotificationsInOverlays(t *testing.T) {
	text := ansi.Strip(strings.Join(keys(newModel(80, 24), "?").helpContent(80), "\n"))
	overlays := strings.Index(text, " In overlays")
	n := strings.Index(text, "desktop notifications")
	if overlays < 0 || n < 0 {
		t.Fatalf("help lacks a heading or the notifications line:\n%s", text)
	}
	if n < overlays {
		t.Fatalf("desktop notifications is outside In overlays:\n%s", text)
	}
}

func TestHelpScrollsAndWrapsAt80(t *testing.T) {
	m := keys(newModel(80, 24), "?")
	var seen strings.Builder
	for i := 0; i < len(m.helpContent(80))+20; i++ {
		v := ansi.Strip(m.View())
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
	for _, want := range []string{"Tools keys", "add an MCP server or a skill from a folder", "Differences", "close help", "more below", "more above"} {
		if !strings.Contains(seen.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestHelpShowsWaitingAndNotificationSettings(t *testing.T) {
	m := keys(newModel(80, 24), "?")
	content := strings.Join(strings.Fields(ansi.Strip(strings.Join(m.helpContent(80), "\n"))), " ")
	for _, want := range []string{"desktop notifications: on (toggle)", "go into the selected thing"} {
		if !strings.Contains(content, want) {
			t.Errorf("help lacks %q", want)
		}
	}
	m.snap.NotificationsDisabled = true
	if !strings.Contains(strings.Join(strings.Fields(ansi.Strip(strings.Join(m.helpContent(80), "\n"))), " "), "desktop notifications: off (toggle)") {
		t.Fatal("help does not show disabled notifications")
	}
}

func TestToolsHelpWrapsAndEveryLineCanBeReached(t *testing.T) {
	m := keys(newModel(40, 10), "5", "?")
	var seen strings.Builder
	for i := 0; i < len(m.helpContent(40))+20; i++ {
		v := ansi.Strip(m.View())
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
