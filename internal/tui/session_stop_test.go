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

func (f *fakeSource) SessionStop(_ context.Context, id string) (string, error) {
	f.stopped = id
	return "Stopped session; it stays in the service.", f.actErr
}

func TestStopOwnerSessionConfirm(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		m, src := workerModel(size.w, size.h)
		m = quietFixtures(m)
		m = selectID(m, "s_0000000000000001")
		before := lines(ansi.Strip(m.testView()))
		m, _ = press(m, tea.KeyPressMsg{Text: "x", Code: []rune("x")[0]})
		if m.mode != modeConfirm || src.stopped != "" {
			t.Fatalf("mode %v stopped %q", m.mode, src.stopped)
		}
		view := ansi.Strip(m.testView())
		// The key line changes; every other row stays put.
		joined := strings.Join(strings.Fields(view), " ")
		name := "tenebrae-53"
		if size.w == 80 {
			name = "tenebrae-…"
		}
		want := strings.Join(strings.Fields("End "+name+" in your terminal? Conversation kept.  enter/y: stop · esc/n: keep"), " ")
		if !strings.Contains(joined, want) {
			t.Fatalf("missing confirm %q:\n%s", want, view)
		}
		after := lines(view)
		if len(after) != size.h {
			t.Fatalf("height %d", len(after))
		}
		for i, line := range before {
			if i < size.h-1 && line != after[i] {
				t.Fatalf("layout shifted at row %d: %q -> %q", i, line, after[i])
			}
		}
		golden(t, "view_"+itoa(size.w)+"x"+itoa(size.h)+"_confirm_stop_owner", m.testView())
		m, cmd := press(m, tea.KeyPressMsg{Text: "y", Code: []rune("y")[0]})
		m = finish(t, m, cmd)
		if src.stopped != "s_0000000000000001" || m.notice.err || !strings.Contains(m.notice.text, "stays in the service") {
			t.Fatalf("stop %q %+v", src.stopped, m.notice)
		}
	}
}

func TestStopShowsPendingRowBeforeReply(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		m, _ := workerModel(size.w, size.h)
		m = quietFixtures(m)
		id := "s_0000000000000001"
		for i := range m.snap.Agents {
			if m.snap.Agents[i].SessionID == id {
				m.snap.Agents[i].Mode = protocol.ModeBackground
			}
		}
		m = selectID(m, id)
		m, _ = press(m, keyRune("x"))
		m, cmd := press(m, keyRune("y"))
		if cmd == nil {
			t.Fatal("confirm did not start stop")
		}
		view := ansi.Strip(m.testView())
		if !strings.Contains(view, "stopping…") {
			t.Fatalf("pending status missing before reply:\n%s", view)
		}
		golden(t, "view_"+itoa(size.w)+"x"+itoa(size.h)+"_stopping", m.testView())
	}
}

func TestStopPendingClearsOnErrorSnapshotAndTimeout(t *testing.T) {
	m, src := workerModel(120, 34)
	id := "s_0000000000000001"
	for i := range m.snap.Agents {
		if m.snap.Agents[i].SessionID == id {
			m.snap.Agents[i].Mode = protocol.ModeBackground
		}
	}
	m = selectID(m, id)
	m, _ = press(m, keyRune("x"))
	m, cmd := press(m, keyRune("y"))
	src.actErr = fmt.Errorf("stop failed")
	m = finish(t, m, cmd)
	if !m.notice.err || strings.Contains(ansi.Strip(m.testView()), "stopping…") {
		t.Fatalf("error did not restore row and show notice: %+v", m.notice)
	}

	src.actErr = nil
	m, _ = press(m, keyRune("x"))
	m, cmd = press(m, keyRune("y"))
	_ = cmd
	var remaining []protocol.AgentView
	for _, a := range m.snap.Agents {
		if a.SessionID != id {
			remaining = append(remaining, a)
		}
	}
	m.snap.Agents = remaining
	m.applyFetch(fetchedMsg{snap: m.snap, at: t0})
	if strings.Contains(ansi.Strip(m.testView()), "tenebrae-53") {
		t.Fatal("session row survived snapshot removal")
	}

	m, src = workerModel(120, 34)
	for i := range m.snap.Agents {
		if m.snap.Agents[i].SessionID == id {
			m.snap.Agents[i].Mode = protocol.ModeBackground
		}
	}
	m = selectID(m, id)
	m, _ = press(m, keyRune("x"))
	m, cmd = press(m, keyRune("y"))
	_ = cmd
	m.now = func() time.Time { return t0.Add(31 * time.Second) }
	next, _ := m.update(tickMsg{})
	m = next.(Model)
	if strings.Contains(ansi.Strip(m.testView()), "stopping…") || !strings.Contains(m.notice.text, "still running:") {
		t.Fatalf("timeout did not clear pending row and notify: %+v", m.notice)
	}
}

func TestStopOwnerSessionCancelAndRefusal(t *testing.T) {
	m, src := workerModel(120, 34)
	m = selectID(m, "s_0000000000000001")
	m, _ = press(m, tea.KeyPressMsg{Text: "x", Code: []rune("x")[0]})
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyEsc}, {Text: "n", Code: []rune("n")[0]}} {
		cancelled, cmd := press(m, key)
		if cmd != nil || cancelled.mode != modeNormal || src.stopped != "" {
			t.Fatalf("cancel: mode %v stopped %q", cancelled.mode, src.stopped)
		}
	}
	src.actErr = fmt.Errorf("session process no longer matches its registered process")
	m, cmd := press(m, tea.KeyPressMsg{Text: "y", Code: []rune("y")[0]})
	m = finish(t, m, cmd)
	if !m.notice.err || m.notice.text != "Not stopped: session process no longer matches its registered process" {
		t.Fatalf("refusal: %+v", m.notice)
	}
}

func TestStopExitedClaudeConfirmation(t *testing.T) {
	m, _ := workerModel(80, 24)
	// An exited session is ended: it shows in the all view, not in Working.
	m.activeOnly, m.showOffline = false, true
	id := "s_0000000000000001"
	for i := range m.snap.Agents {
		if m.snap.Agents[i].SessionID == id {
			m.snap.Agents[i].Status = "exited"
			m.snap.Agents[i].Mode = "background"
		}
	}
	m = selectID(m, id)
	m, _ = press(m, keyRune("x"))
	// Even a long name must leave the action keys readable.
	m.confirmName = strings.Repeat("procurement", 12)
	for _, width := range []int{40, 80, 120} {
		line := ansi.Strip(m.helpLine(width))
		if !strings.HasSuffix(line, "enter/y: stop · esc/n: keep") || strings.Contains(strings.ToLower(line), "resume") {
			t.Fatalf("misleading stop confirmation at %d columns: %q", width, line)
		}
	}
}

func TestOpenCodeCloseConfirm(t *testing.T) {
	for _, mode := range []string{"", "background", "foreground"} {
		t.Run(mode, func(t *testing.T) {
			m, _ := workerModel(120, 34)
			id := "s_0000000000000001"
			for i := range m.snap.Agents {
				if m.snap.Agents[i].SessionID == id {
					m.snap.Agents[i].Harness = "opencode"
					m.snap.Agents[i].Mode = mode
				}
			}
			m = selectID(m, id)
			m, _ = press(m, tea.KeyPressMsg{Text: "x", Code: 'x'})
			view := ansi.Strip(m.testView())
			if mode == "foreground" {
				if !m.confirmTerminal || m.confirmClose || !strings.Contains(view, "End ") || !strings.Contains(view, "in your terminal? Conversation kept.") {
					t.Fatal(view)
				}
			} else if !m.confirmClose || m.confirmTerminal || !strings.Contains(view, "Close ") || !strings.Contains(view, "enter/y: close") {
				t.Fatal(view)
			}
		})
	}
}
