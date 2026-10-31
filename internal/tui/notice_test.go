package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// lastLine is the key-hint row, where an action result is shown.
func lastLine(s string) string {
	ls := lines(s)
	return ls[len(ls)-1]
}

// noticeScreens returns one model per tab and the map, so the layout test
// covers every screen that has a key line.
func noticeScreens(w, h int) []struct {
	name string
	m    Model
} {
	var out []struct {
		name string
		m    Model
	}
	for tab := range tabNames {
		m := newModel(w, h)
		m.tab = tab
		m.mapOn = false
		out = append(out, struct {
			name string
			m    Model
		}{fmt.Sprintf("tab %d", tab+1), m})
	}
	out = append(out, struct {
		name string
		m    Model
	}{"map", keys(newModel(w, h), "4")})
	return out
}

func TestNoticeKeepsTheLayoutAndUsesTheLastLine(t *testing.T) {
	const text = "Back from agent-net (research)."
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		for _, screen := range noticeScreens(size[0], size[1]) {
			base := screen.m.View()
			if got := len(lines(base)); got != size[1] {
				t.Fatalf("%s %dx%d: %d rows before a result", screen.name, size[0], size[1], got)
			}
			with := screen.m
			with.say(text)
			got := with.View()
			if n := len(lines(got)); n != size[1] {
				t.Fatalf("%s %dx%d: %d rows with a result", screen.name, size[0], size[1], n)
			}
			for i := 0; i < size[1]-1; i++ {
				if lines(got)[i] != lines(base)[i] {
					t.Fatalf("%s %dx%d: row %d moved with a result:\n--- without ---\n%s\n--- with ---\n%s",
						screen.name, size[0], size[1], i, base, got)
				}
			}
			last := lastLine(got)
			if !strings.Contains(ansi.Strip(last), text) {
				t.Fatalf("%s %dx%d: last row does not show the result: %q", screen.name, size[0], size[1], ansi.Strip(last))
			}
			if last == lastLine(base) {
				t.Fatalf("%s %dx%d: the hints were not replaced", screen.name, size[0], size[1])
			}
			if style := with.noticeStyle(with.activeNotice()); fmt.Sprint(style.GetForeground()) != fmt.Sprint(sAccent.GetForeground()) {
				t.Fatalf("%s: a result is not in the accent colour", screen.name)
			}
		}
	}
}

func TestNoticeErrorIsMarkedAndRed(t *testing.T) {
	m := newModel(80, 24)
	m.fail("Worker not started.")
	last := ansi.Strip(lastLine(m.View()))
	if !strings.HasPrefix(last, "! Worker not started.") {
		t.Fatalf("error row: %q", last)
	}
	if style := m.noticeStyle(m.activeNotice()); fmt.Sprint(style.GetForeground()) != fmt.Sprint(sBad.GetForeground()) {
		t.Fatal("an error is not in the error colour")
	}
}

func TestNoticeHoldsFadesAndReturnsTheHints(t *testing.T) {
	now := t0
	m := newModel(80, 24)
	m.now = func() time.Time { return now }
	hints := lastLine(m.View())

	m.say("Back from agent-net (research).")
	full := fmt.Sprint(m.noticeStyle(m.activeNotice()).GetForeground())

	now = t0.Add(noticeHold - 100*time.Millisecond)
	if m.activeNotice() == nil || lastLine(m.View()) == hints {
		t.Fatal("the result left before the hold ended")
	}
	now = t0.Add(noticeHold + noticeFade/2)
	n := m.activeNotice()
	if n == nil {
		t.Fatal("the result has no fade step")
	}
	if faded := fmt.Sprint(m.noticeStyle(n).GetForeground()); faded == full {
		t.Fatal("the result did not fade")
	}
	if !strings.Contains(ansi.Strip(lastLine(m.View())), "Back from agent-net") {
		t.Fatal("the fading result is not on screen")
	}

	now = t0.Add(noticeHold + noticeFade + time.Millisecond)
	if m.activeNotice() != nil {
		t.Fatal("the result outlived its fade")
	}
	if got := lastLine(m.View()); got != hints {
		t.Fatalf("the hints did not return: %q", ansi.Strip(got))
	}
}

func TestNoticeErrorHoldsLonger(t *testing.T) {
	now := t0
	m := newModel(80, 24)
	m.now = func() time.Time { return now }
	m.fail("Worker not started.")

	now = t0.Add(noticeHoldErr - time.Second)
	if m.activeNotice() == nil {
		t.Fatal("the error left before the longer hold ended")
	}
	now = t0.Add(noticeHoldErr + noticeFade + time.Millisecond)
	if m.activeNotice() != nil {
		t.Fatal("the error outlived its fade")
	}
}

// A key press still dismisses the result at once.
func TestNoticeDismissedByAKey(t *testing.T) {
	m := newModel(80, 24)
	m.say("Back from agent-net (research).")
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.activeNotice() != nil {
		t.Fatal("the result stayed after a key")
	}
}
