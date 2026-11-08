package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// deskModel is the Desk with everything on it: asks, changes in flight and an
// open log with a CTO line that can be undone.
func deskModel(t *testing.T, w, h int) Model {
	t.Helper()
	m := openDelivery(t, deliveryModel(&fakeDelivery{changes: deliveryScenes()}, w, h))
	m.asks = askItems([]protocol.InboxItem{
		{ID: "i_q", Kind: "question", SessionID: "s_0000000000000002", AskerName: "ingest-forge", Text: "Merge PR #3? CI green.", Recommendation: "yes", Options: []string{"yes", "no"}, Status: "open", CreatedAt: t0.Add(-2 * time.Hour)},
		{ID: "i_a", Kind: "approval", SessionID: "s_0000000000000004", AskerName: "strategies", Text: "Release v1.2.3 to the machines", Status: "open", CreatedAt: t0.Add(-30 * time.Minute)},
	})
	m.deskLogOpen = true
	m.deskLog = []protocol.DeskLogEntry{
		{ID: 3, At: t0.Add(-5 * time.Minute), Source: "cto", Text: "CTO applied graph edge set app -> ops", Options: []string{"graph_undo", `{"id":42}`}},
		{ID: 2, At: t0.Add(-20 * time.Minute), Source: "gateway", Text: "Reminded 2 sessions about messages they have not answered"},
		{ID: 1, At: t0.Add(-3 * time.Hour), Source: "apps", Text: "Connected: phone (owner_app, Phone, example.test)"},
	}
	return m
}

// The Desk shows the three sections, and one cursor walks all of them.
func TestDeskShowsThreeSectionsAndOneCursor(t *testing.T) {
	m := deskModel(t, 120, 40)
	view := ansi.Strip(m.testView())
	for _, want := range []string{"Desk · 2 need you", "Needs you · 2", "In flight · ", "Log · 3", "Merge PR #3? CI green.", "CTO applied graph edge set", "Reminded 2 sessions"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q:\n%s", want, view)
		}
	}
	asks, changes := m.askRowCount(), len(m.deliveryVisible())
	if asks != 2 || changes == 0 {
		t.Fatalf("%d asks and %d changes", asks, changes)
	}
	for i := 0; i < asks+changes+len(m.deskLogRows())-1; i++ {
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
	if sec, i := m.deskPosition(); sec != deskSecChange || i != 0 {
		t.Fatalf("the cursor is not on the first change: %d %d", sec, i)
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

// The ask of a change shows on the change's row in flight.
func TestDeskChangeRowShowsItsAsk(t *testing.T) {
	m := deskModel(t, 120, 40)
	ch := m.deliveryVisible()[0]
	m.asks = append(m.asks, protocol.Ask{ID: "i_s", Kind: protocol.AskKindStep, State: protocol.AskStateOpen, FromKind: protocol.AskFromSession, From: "caller",
		AboutKind: protocol.AskAboutChange, AboutID: ch.ID, Question: "Owner: press v and try it", Next: protocol.AskNextOwner, CreatedAt: t0})
	view := ansi.Strip(m.testView())
	if !strings.Contains(view, "asks you: Owner: press v and try it") {
		t.Fatalf("the change row does not show its ask:\n%s", view)
	}
	if got := m.waitingCount(); got != 3 {
		t.Fatalf("waiting count %d, want 3", got)
	}
}

func TestDeskGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := deskModel(t, size[0], size[1])
		golden(t, "desk_all_"+itoa(size[0]), m.testView())
		m.inboxIndex = m.askRowCount() + len(m.deliveryVisible())
		golden(t, "desk_log_selected_"+itoa(size[0]), m.testView())
	}
}
