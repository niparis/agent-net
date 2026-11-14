package tui

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

// A role start that no locked check lets through shows one reason line in the
// CTO box, at both owner screen sizes (decision 0162). The text comes from the
// gateway's role picker, which keeps a refusal to one line.
func TestCTORoleRefusalShowsOneReasonLine(t *testing.T) {
	reason := "claude claude-opus-5-5: claude is limited until 2026-11-14 12:00 UTC (a live session reported it)"
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := ctoPart8Model(size[0], size[1], "Atlas", 0)
		m.focus = focusCTO
		m.applyCTOPoll(ctoPollMsg{events: protocol.CTOEvents{Messages: []json.RawMessage{}}, err: errors.New(reason), generation: m.cto.generation})
		plain := ansi.Strip(m.testView())
		if !strings.Contains(plain, "error: claude claude-opus-5-5: claude is limited until") {
			t.Fatalf("%dx%d: the CTO box does not show the reason:\n%s", size[0], size[1], plain)
		}
		if strings.Count(plain, "error: ") != 1 {
			t.Fatalf("%dx%d: the reason is not one line:\n%s", size[0], size[1], plain)
		}
		if size[0] >= 120 && !strings.Contains(plain, "a live session reported it") {
			t.Fatalf("%dx%d: the wide box cuts the reason:\n%s", size[0], size[1], plain)
		}
		t.Logf("frame %dx%d:\n%s", size[0], size[1], plain)
	}
}
