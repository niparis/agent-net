package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

// TestCTOStoppedHistoryProofScenario renders the CTO box after a timeline read
// of a stopped Codex CTO. The box shows the newest line; scrolling up shows the
// start of the stopped line and the stored action above it. Frames at 80x24 and
// 120x34 go to AGENT_NET_PROOF_DIR when it is set
// (scripts/cto-stopped-history-proof.sh).
func TestCTOStoppedHistoryProofScenario(t *testing.T) {
	const stopped = "The CTO is stopped. This is its conversation as it was at the stop. Send it a message or press Enter to start it."
	events := protocol.CTOEvents{Actions: []protocol.CTOAction{
		{ID: 1, Text: "approved p_7 and applied", At: 1},
		{ID: 2, Text: stopped, At: 2},
	}}
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		src := &fakeCTOSource{
			fakeSource: &fakeSource{snap: fakeSnapshot()},
			events:     events,
			settings:   protocol.CTOSettings{Harness: "codex", Model: "gpt-fake", CodexModel: "gpt-fake"},
		}
		m := New(src)
		m.snap = fakeSnapshot()
		m.width, m.height = size[0], size[1]
		updated, _ := m.update(tea.KeyPressMsg{Text: "C", Code: []rune("C")[0]})
		m = updated.(Model)
		updated, _ = m.update(m.ctoPollCmd()())
		m = updated.(Model)
		writeCTOStoppedFrame(t, size, "open", "press C and wait for the timeline read",
			"the newest line comes from the stored timeline of the stopped CTO", ansi.Strip(m.testView()))
		seenStart, seenStored := false, false
		for step := 1; step <= 5 && !(seenStart && seenStored); step++ {
			next, _ := m.ctoKey(tea.KeyPressMsg{Code: tea.KeyPgUp})
			m = next.(Model)
			frame := ansi.Strip(m.testView())
			if !seenStart && strings.Contains(frame, "The CTO is stopped.") {
				seenStart = true
				writeCTOStoppedFrame(t, size, fmt.Sprintf("scroll-%d", step), "press PgUp in the CTO box",
					"the start of the line that says the CTO is stopped", frame)
			}
			if !seenStored && strings.Contains(frame, "approved p_7 and applied") {
				seenStored = true
				writeCTOStoppedFrame(t, size, fmt.Sprintf("scroll-%d", step), "press PgUp in the CTO box",
					"the stored action line above the stopped line", frame)
			}
		}
		if !seenStart || !seenStored {
			t.Fatalf("%dx%d: start seen %t, stored action seen %t", size[0], size[1], seenStart, seenStored)
		}
	}
}

func writeCTOStoppedFrame(t *testing.T, size [2]int, step, action, expected, frame string) {
	t.Helper()
	dir := os.Getenv("AGENT_NET_PROOF_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	capture := fmt.Sprintf("build: %s\nstep: %s in the CTO box at %dx%d\naction: %s\nexpected: %s\nerrors: none\n\n%s\n",
		os.Getenv("AGENT_NET_PROOF_BUILD"), step, size[0], size[1], action, expected, frame)
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("cto-stopped-%s-%dx%d.txt", step, size[0], size[1])), []byte(capture), 0o600); err != nil {
		t.Fatal(err)
	}
}
