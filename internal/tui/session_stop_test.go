package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func (f *fakeSource) SessionStop(_ context.Context, id string) (string, error) {
	f.stopped = id
	return "Stopped session; it stays in the service.", f.actErr
}

func TestStopOwnerSessionConfirm(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 34}} {
		m, src := workerModel(size.w, size.h)
		m = selectID(m, "s_0000000000000001")
		before := lines(ansi.Strip(m.View()))
		m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
		if m.mode != modeConfirm || src.stopped != "" {
			t.Fatalf("mode %v stopped %q", m.mode, src.stopped)
		}
		view := ansi.Strip(m.View())
		// The key line changes; every other row stays put.
		joined := strings.Join(strings.Fields(view), " ")
		name := "tenebrae-53"
		want := strings.Join(strings.Fields("Stop "+name+"? Conversation kept: Enter resumes it."+confirmSuffix), " ")
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
		golden(t, "view_"+itoa(size.w)+"x"+itoa(size.h)+"_confirm_stop_owner", m.View())
		m, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
		m = finish(t, m, cmd)
		if src.stopped != "s_0000000000000001" || m.notice.err || !strings.Contains(m.notice.text, "stays in the service") {
			t.Fatalf("stop %q %+v", src.stopped, m.notice)
		}
	}
}

func TestStopOwnerSessionCancelAndRefusal(t *testing.T) {
	m, src := workerModel(120, 34)
	m = selectID(m, "s_0000000000000001")
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	for _, key := range []tea.KeyMsg{{Type: tea.KeyEsc}, {Type: tea.KeyRunes, Runes: []rune("n")}} {
		cancelled, cmd := press(m, key)
		if cmd != nil || cancelled.mode != modeNormal || src.stopped != "" {
			t.Fatalf("cancel: mode %v stopped %q", cancelled.mode, src.stopped)
		}
	}
	src.actErr = fmt.Errorf("tenebrae-53 runs in your terminal on Mac: end it there")
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = finish(t, m, cmd)
	if !m.notice.err || m.notice.text != "Not stopped: tenebrae-53 runs in your terminal on Mac: end it there" {
		t.Fatalf("refusal: %+v", m.notice)
	}
}
