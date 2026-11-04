package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

type windowCTOSource struct {
	*fakeCTOSource
	until   time.Time
	toggles int
}

func (f *windowCTOSource) CTOGraphWindow(_ context.Context, toggle bool) (protocol.CTOGraphWindow, error) {
	if toggle {
		f.toggles++
		if f.until.IsZero() {
			f.until = t0.Add(30 * time.Minute)
		} else {
			f.until = time.Time{}
		}
	}
	return protocol.CTOGraphWindow{Until: f.until}, nil
}
func TestCTOWindowKeyAndGoldens(t *testing.T) {
	for _, width := range []int{80, 120} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m, base := titledCTO(t)
			src := &windowCTOSource{fakeCTOSource: base}
			m.src = src
			m.focus = focusCTO
			m.proposals = []protocol.CTOProposal{{ID: "p_1", Status: "open"}, {ID: "p_2", Status: "open"}, {ID: "p_3", Status: "approved"}}
			next, cmd := m.ctoKey(tea.KeyPressMsg{Text: "w", Code: []rune("w")[0]})
			m = next.(Model)
			if cmd == nil {
				t.Fatal("w did not toggle window")
			}
			m = send(m, cmd())
			if src.toggles != 1 {
				t.Fatal("toggle missing")
			}
			box := strings.Join(m.ctoView(width, 10), "\n")
			if !strings.Contains(box, "I'm driving") || !strings.Contains(box, "30:00") {
				t.Fatalf("window absent:\n%s", box)
			}
			if !strings.Contains(box, "w: stop driving") || strings.Contains(box, "w: drive 30m") {
				t.Fatalf("active window hint disagrees with title:\n%s", box)
			}
			plain := ansi.Strip(box)
			if !strings.Contains(plain, "2 open proposals") || strings.Contains(plain, "…") {
				t.Fatalf("window/proposal title clipped:\n%s", plain)
			}
			for _, line := range strings.Split(plain, "\n") {
				if ansi.StringWidth(line) > width {
					t.Fatalf("line exceeds %d columns: %s", width, line)
				}
			}
			golden(t, fmt.Sprintf("cto_window_%d", width), box)
			m.now = func() time.Time { return t0.Add(5 * time.Minute) }
			if box = strings.Join(m.ctoView(width, 10), "\n"); !strings.Contains(box, "25:00") {
				t.Fatalf("countdown absent:\n%s", box)
			}
			m.now = func() time.Time { return t0.Add(30 * time.Minute) }
			box = strings.Join(m.ctoView(width, 10), "\n")
			if !strings.Contains(box, "w: drive 30m") || strings.Contains(box, "w: stop driving") {
				t.Fatalf("expired window hint: \n%s", box)
			}
			if strings.Contains(box, "I'm driving") {
				t.Fatal("expired window displayed")
			}
			m.now = func() time.Time { return t0 }
			next, cmd = m.ctoKey(tea.KeyPressMsg{Text: "w", Code: []rune("w")[0]})
			m = next.(Model)
			m = send(m, cmd())
			if !m.cto.windowUntil.IsZero() {
				t.Fatal("window did not close")
			}
		})
	}
}

func TestCTOWindowProposalTitleFits(t *testing.T) {
	for _, width := range []int{80, 120} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := ctoPart8Model(width, 24, "Network architecture CTO", 2)
			m.cto.windowUntil = t0.Add(30 * time.Minute)
			m.cto.model = "provider/" + strings.Repeat("long-model-", 8)
			header := ansi.Strip(m.ctoView(width, 10)[0])
			for _, want := range []string{"Network architecture CTO", "I'm driving 30:00 · w close", "2 open proposals"} {
				if !strings.Contains(header, want) {
					t.Fatalf("missing %q in header: %s", want, header)
				}
			}
			if strings.Contains(header, "…") || ansi.StringWidth(header) > width {
				t.Fatalf("clipped header: %s", header)
			}
		})
	}
}
