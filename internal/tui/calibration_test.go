package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

// quotaPanelText also checks that a visible panel closes before the next one.
func quotaPanelText(t *testing.T, view, title string) string {
	t.Helper()
	start := strings.Index(view, "╭─"+title)
	if start < 0 {
		t.Fatalf("missing %s panel:\n%s", title, view)
	}
	var body []string
	for i, line := range strings.Split(view[start:], "\n") {
		if i > 0 && strings.HasPrefix(line, "╭") {
			t.Fatalf("%s panel has no bottom border:\n%s", title, view)
		}
		body = append(body, line)
		if strings.HasPrefix(line, "╰") {
			return strings.Join(body, "\n")
		}
	}
	t.Fatalf("%s panel has no bottom border:\n%s", title, view)
	return ""
}

func TestQuotasHasNoQualityPanel(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := newModel(size[0], size[1])
		m.tab = quotasTab
		view := ansi.Strip(m.testView())
		if strings.Contains(view, "Quality analysis") {
			t.Errorf("Quality panel remains in Quotas:\n%s", view)
		}
		for _, key := range []string{"d", "left", "right"} {
			if keyAction(contextQuotas, key) != "" {
				t.Errorf("Quality-only key %q remains in Quotas", key)
			}
		}
		golden(t, fmt.Sprintf("calibration_%dx%d", size[0], size[1]), m.testView())
	}
}

func TestQuotaOverflowPreservesAllPanelBordersAcrossHeights(t *testing.T) {
	// Include the room available with both the current CTO box and a shorter
	// box. Scroll through Subscriptions and Workers, including joins.
	for _, height := range []int{8, 12, 17, 22, 28} {
		m := newModel(120, 34)
		m.snap.WorkerProfiles = []protocol.WorkerProfile{{Harness: "codex", Model: "gpt-6.1-sol"}}
		var seen strings.Builder
		for scroll := range 80 {
			m.quotaScroll = scroll
			rows := m.quotasView(120, height)
			if len(rows) != height {
				t.Fatalf("height %d scroll %d: got %d rows", height, scroll, len(rows))
			}
			open := false
			for _, row := range rows {
				row = ansi.Strip(row)
				seen.WriteString(row + "\n")
				switch {
				case strings.HasPrefix(row, "╭"):
					if open {
						t.Fatalf("height %d scroll %d: panel lost its bottom border", height, scroll)
					}
					open = true
				case strings.HasPrefix(row, "╰"):
					if !open {
						t.Fatalf("height %d scroll %d: panel lost its top border", height, scroll)
					}
					open = false
				case strings.HasPrefix(row, "│"):
					if !open {
						t.Fatalf("height %d scroll %d: panel body lost its top border", height, scroll)
					}
				}
			}
			if open {
				t.Fatalf("height %d scroll %d: panel lost its bottom border", height, scroll)
			}
		}
		for _, want := range []string{"Subscriptions", "Workers"} {
			if !strings.Contains(seen.String(), want) {
				t.Errorf("height %d: scrolling never showed %q", height, want)
			}
		}
	}
}

func TestQuotaCalibrationGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 40}, {120, 34}} {
		m := newModel(size[0], size[1])
		m.tab = 2
		view := m.testView()
		for _, line := range lines(view) {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("wide line at %d columns: %q", size[0], line)
			}
		}
		golden(t, fmt.Sprintf("calibration_%dx%d", size[0], size[1]), view)
	}
}
