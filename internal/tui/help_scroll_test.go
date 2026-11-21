package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestHelpListsSettings(t *testing.T) {
	m := keys(newModel(80, 24), "5", "?")
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
	// The down key scrolls one line (checked once here). Every frame builds the
	// whole help again, which is what made this test slow, so the loop sets the
	// scroll offset itself and moves ten lines per frame. The help shows more
	// than ten lines at 80x24, so a line that no frame shows fails below.
	const step = 10
	if down := keys(m, "down"); down.helpScroll != m.helpScroll+1 {
		t.Fatalf("down moved the help from %d to %d, want one line", m.helpScroll, down.helpScroll)
	}
	for i := 0; i < len(m.helpContent(80))+20; i += step {
		m.helpScroll = i
		v := ansi.Strip(m.testView())
		if strings.Contains(v, "…") {
			t.Fatalf("help has an ellipsis:\n%s", v)
		}
		seen.WriteString(v)
		seen.WriteByte('\n')
	}
	m = keys(m, "down") // at the end, this clamps like the real key
	all := strings.Join(strings.Fields(seen.String()), " ")
	for _, line := range m.helpContent(80) {
		line = strings.Join(strings.Fields(ansi.Strip(line)), " ")
		if line != "" && !strings.Contains(all, line) {
			t.Errorf("help line unreachable: %q", line)
		}
	}
	for _, want := range []string{"more below", "more above", "request rule", "Messages", "select the previous or next message", "In overlays", "states", "open the selected message body"} {
		if !strings.Contains(all, want) {
			t.Errorf("help did not show %q", want)
		}
	}
	m = keys(m, "up")
	if !strings.Contains(ansi.Strip(m.testView()), "more above") {
		t.Fatal("up did not scroll back")
	}
}
