package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// deskModel is the Desk with everything on it: asks and an open log with a
// CTO line that can be undone.
func deskModel(t *testing.T, w, h int) Model {
	t.Helper()
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: deliveryScenes()}, w, h))
	m.snap.Agents = append(m.snap.Agents, agent("s_00000000000000f1", "n_mac", "Mac", "claude", "ingest-forge", nil))
	m.asks = askItems([]protocol.InboxItem{
		{ID: "i_q", Kind: "question", SessionID: "s_00000000000000f1", AskerName: "ingest-forge", Text: "Merge PR #3? CI green.", Recommendation: "yes", Options: []string{"yes", "no"}, Status: "open", CreatedAt: t0.Add(-2 * time.Hour)},
		{ID: "i_a", Kind: "approval", SessionID: "s_0000000000000004", AskerName: "strategies", Text: "Release v1.2.3 to the machines", Status: "open", CreatedAt: t0.Add(-30 * time.Minute)},
	})
	m.deskLogOpen = true
	m.deskLog = []protocol.DeskLogEntry{
		{ID: 3, At: t0.Add(-5 * time.Minute), Source: "cto", Text: "CTO applied graph edge set app -> ops", Options: []string{"graph_undo", `{"id":42}`}},
		{ID: 2, At: t0.Add(-20 * time.Minute), Source: "gateway", Text: "Reminded 2 sessions about messages they have not type answered"},
		{ID: 1, At: t0.Add(-3 * time.Hour), Source: "apps", Text: "Connected: phone (owner_app, Phone, example.test)"},
	}
	return m
}

// The Desk shows Needs you and the Log, and one cursor walks both.
func TestDeskShowsNeedsYouAndLogOneCursor(t *testing.T) {
	m := deskModel(t, 120, 40)
	view := ansi.Strip(m.testView())
	for _, want := range []string{"Desk · chief of staff", "Needs you · 2", "Log · 3", "Merge PR #3? CI green.", "CTO applied graph edge set", "Reminded 2 sessions"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "sorted ") || strings.Contains(view, "Handled for you") {
		t.Fatalf("the Desk still shows Handled for you:\n%s", view)
	}
	if strings.Contains(view, "In flight") || strings.Contains(view, "changes of ") {
		t.Fatalf("the Desk still shows In flight:\n%s", view)
	}
	asks := m.askRowCount()
	if asks != 2 {
		t.Fatalf("%d asks", asks)
	}
	for i := 0; i < asks+len(m.deskLogRows())-1; i++ {
		m = send(m, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if sec, i := m.deskPosition(); sec != deskSecLog || i != 2 {
		t.Fatalf("the cursor did not reach the last log line: %d %d", sec, i)
	}
	m = send(m, tea.KeyPressMsg{Code: tea.KeyUp})
	if sec, i := m.deskPosition(); sec != deskSecLog || i != 1 {
		t.Fatalf("up did not step back inside the log: %d %d", sec, i)
	}
	m.inboxIndex = asks
	if sec, i := m.deskPosition(); sec != deskSecLog || i != 0 {
		t.Fatalf("the cursor is not on the first log line: %d %d", sec, i)
	}
}

// The log is closed until the owner opens it with l, and l fetches the lines.
func TestDeskLogOpensWithL(t *testing.T) {
	src := &ctoUndoSource{}
	m := newModel(100, 30)
	m.src = src
	m.tab = inboxTab
	if strings.Contains(ansi.Strip(m.testView()), "Log · 0") || !strings.Contains(ansi.Strip(m.testView()), "log closed (l opens it)") {
		t.Fatalf("the log is not closed by default:\n%s", ansi.Strip(m.testView()))
	}
	next, cmd := m.Update(tea.KeyPressMsg{Text: "l", Code: 'l'})
	m = next.(Model)
	if !m.deskLogOpen || cmd == nil {
		t.Fatal("l did not open the log and fetch it")
	}
	cmd()
	if len(src.methods) != 1 || src.methods[0] != protocol.OpDeskLog {
		t.Fatalf("calls %v", src.methods)
	}
	next, _ = m.Update(tea.KeyPressMsg{Text: "l", Code: 'l'})
	if next.(Model).deskLogOpen {
		t.Fatal("l did not close the log")
	}
}

// The ask of a change no longer gets a row of its own on the Desk.
func TestDeskHasNoInFlight(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := deskModel(t, size[0], size[1])
		view := ansi.Strip(m.testView())
		t.Logf("frame %dx%d, open:\n%s", size[0], size[1], view)
		for _, gone := range []string{"In flight", "changes of ", "show all changes", "Try it"} {
			if strings.Contains(view, gone) {
				t.Errorf("%dx%d: the Desk still shows %q:\n%s", size[0], size[1], gone, view)
			}
		}
		// The cursor walks from the asks into the Log, with no stop in between.
		for i := 0; i < m.askRowCount(); i++ {
			m = send(m, tea.KeyPressMsg{Code: tea.KeyDown})
		}
		if sec, i := m.deskPosition(); sec != deskSecLog || i != 0 {
			t.Fatalf("%dx%d: the cursor is not on the first log line: %d %d", size[0], size[1], sec, i)
		}
		t.Logf("frame %dx%d, after scrolling to the Log:\n%s", size[0], size[1], ansi.Strip(m.testView()))
		before := m.inboxIndex
		m = send(m, tea.KeyPressMsg{Text: "v", Code: 'v'})
		if m.inboxIndex != before {
			t.Errorf("%dx%d: v moved the cursor", size[0], size[1])
		}
		if action := keyAction(contextInbox, "v"); action != "" {
			t.Errorf("%dx%d: v is still bound to %q", size[0], size[1], action)
		}
		if action := keyAction(contextGraph, "D"); action != "" {
			t.Errorf("%dx%d: Graph D is still bound to %q", size[0], size[1], action)
		}
	}
}

func TestDeskGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}, {160, 40}} {
		m := deskChiefModel(t, size[0], size[1])
		golden(t, "desk_all_"+itoa(size[0]), m.testView())
		m.inboxIndex = m.askRowCount()
		golden(t, "desk_log_selected_"+itoa(size[0]), m.testView())
	}
}

// Handled for you and its should-have-asked key are gone. Run with -v to see
// the frames at 80x24 and 120x34.
func TestDeskHasNoHandledForYou(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := deskChiefModel(t, size[0], size[1])
		view := ansi.Strip(m.testView())
		t.Logf("frame %dx%d:\n%s", size[0], size[1], view)
		for _, gone := range []string{"Handled for you", "sorted ", "should have asked"} {
			if strings.Contains(view, gone) {
				t.Errorf("%dx%d: the Desk still shows %q:\n%s", size[0], size[1], gone, view)
			}
		}
		m.inboxIndex = 0
		m, _ = deskPress(t, m, tea.KeyPressMsg{Text: "m", Code: 'm'})
		if m.deskPending != nil || m.inboxEditing {
			t.Errorf("%dx%d: m still starts a mark", size[0], size[1])
		}
	}
}
