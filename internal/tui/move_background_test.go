package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// Enter on a hand-started Claude session asks before it moves the session to
// the background. y runs the move; n leaves the session alone.
func TestEnterOffersMoveToBackground(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		for _, yes := range []bool{true, false} {
			m, src := workerModel(size[0], size[1])
			m = selectID(m, workerID)
			src.enterPlan = protocol.EnterPlan{MoveToBackground: true, Note: "runs in a terminal outside agent-net"}
			m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
			m, _ = press2(m, cmd())
			if m.moveConfirm == nil {
				t.Fatalf("%dx%d: Enter did not ask", size[0], size[1])
			}
			v := ansi.Strip(m.testView())
			if !strings.Contains(v, "to the background?") || !strings.Contains(v, confirmHint) {
				t.Fatalf("%dx%d: the question is not on screen:\n%s", size[0], size[1], v)
			}
			if len(src.sessionResumes) != 0 {
				t.Fatal("the session moved before the owner answered")
			}
			key := keyRune("n")
			if yes {
				key = keyRune("y")
			}
			m, cmd = press(m, key)
			if m.moveConfirm != nil {
				t.Fatal("the question stayed")
			}
			if !yes {
				if cmd != nil || len(src.sessionResumes) != 0 {
					t.Fatal("n moved the session")
				}
				continue
			}
			cmd()
			if len(src.sessionResumes) != 1 || !src.sessionResumes[0].MoveToBackground || src.sessionResumes[0].SessionID != workerID {
				t.Fatalf("resume calls: %+v", src.sessionResumes)
			}
		}
	}
}
