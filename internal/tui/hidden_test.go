package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

func hiddenModel(w, h int, src *fakeSource) Model {
	m := New(src)
	m.activeOnly = false
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: w, Height: h})
	return settle(refresh(m, src))
}

func finishHiddenKey(t *testing.T, m Model, key string) Model {
	t.Helper()
	m, cmd := press(m, tea.KeyPressMsg{Text: key, Code: []rune(key)[0]})
	if cmd == nil {
		t.Fatalf("%q did not start a hidden-items update", key)
	}
	next, _ := m.Update(cmd())
	return next.(Model)
}

func containsSession(m Model, id string) bool {
	for _, a := range m.visible() {
		if a.SessionID == id {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestHideSessionPersistsAndCanBeShownAndUnhidden(t *testing.T) {
	const id = "s_0000000000000003"
	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents()}
	m := hiddenModel(120, 34, src)
	m.selected = id
	m.ensureSelection()
	m = finishHiddenKey(t, m, "h")
	if containsSession(m, id) {
		t.Fatalf("hidden session remains in the list: %+v", m.visible())
	}
	if !containsString(src.hidden.Sessions, id) {
		t.Fatalf("hidden session was not saved: %+v", src.hidden)
	}
	if !strings.Contains(m.titleLine(120), "hidden 1") {
		t.Fatalf("session title omits hidden count: %s", m.titleLine(120))
	}
	m = keys(m, "H")
	if !containsSession(m, id) || !strings.Contains(m.testView(), "hidden") {
		t.Fatalf("show hidden did not draw a tagged session:\n%s", m.testView())
	}
	m.selected = id
	m.ensureSelection()
	m = finishHiddenKey(t, m, "h")
	if containsString(src.hidden.Sessions, id) || !containsSession(m, id) {
		t.Fatalf("hidden session was not unhidden: %+v / %+v", src.hidden, m.visible())
	}
	if got := src.hiddenSaved; len(got) != 2 {
		t.Fatalf("saved preference updates: %+v", got)
	}
}

func TestHideSessionKeyDoesNotTakeTheWorkerStopKey(t *testing.T) {
	m := keys(newModel(120, 34), "?")
	v := m.testView()
	for _, want := range []string{"stop the selected worker or session", "hide or unhide the selected session", "cycle the view: active (default) or all, which includes offline sessions", "show or hide sessions hidden with h"} {
		if !strings.Contains(v, want) {
			t.Errorf("help is missing %q:\n%s", want, v)
		}
	}
	for _, width := range []int{80, 120} {
		w, _ := workerModel(width, 34)
		w = selectID(w, workerID)
		line := ansi.Strip(w.helpLine(width))
		if !strings.Contains(line, "x stop") {
			t.Errorf("worker row at width %d does not show x stop: %s", width, line)
		}
		if hints := strings.Split(line, "  "); len(hints) > 8 {
			t.Errorf("worker row at width %d has too many hints: %s", width, line)
		}
	}
}

func TestHiddenSessionSaveFailureKeepsItVisible(t *testing.T) {
	const id = "s_0000000000000003"
	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents(), hiddenSetErr: errors.New("gateway unavailable")}
	m := hiddenModel(100, 30, src)
	m.selected = id
	m.ensureSelection()
	m = finishHiddenKey(t, m, "h")
	if !containsSession(m, id) || !strings.Contains(m.testView(), "gateway unavailable") {
		t.Fatalf("failed save changed local visibility without explaining the error:\n%s", m.testView())
	}
}

func TestHiddenItemsRefreshFromTheSharedPreference(t *testing.T) {
	const id = "s_0000000000000003"
	src := &fakeSource{snap: fakeSnapshot(), events: fakeEvents(), hidden: protocol.HiddenItems{Sessions: []string{id}}}
	m := hiddenModel(120, 34, src)
	if containsSession(m, id) {
		t.Fatal("shared hidden preference was ignored by the session list")
	}
	// A failed read keeps the last shared preference so a transient gateway
	// problem cannot make hidden rows appear unexpectedly.
	src.hiddenGetErr = context.DeadlineExceeded
	m = refresh(m, src)
	if containsSession(m, id) {
		t.Fatal("failed preference refresh discarded the last hidden items")
	}
}
