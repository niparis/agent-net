package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

type standingFake struct {
	fakeSource
	orders []protocol.StandingOrder
	notes  []protocol.StandingOrder
	calls  []protocol.StandingOrdersRequest
}

func (f *standingFake) Call(_ context.Context, op string, params any) (json.RawMessage, error) {
	if op != protocol.OpStandingOrders {
		return json.RawMessage(`[]`), nil
	}
	q := params.(protocol.StandingOrdersRequest)
	f.calls = append(f.calls, q)
	var res protocol.StandingOrdersResult
	res.Notes = f.notes
	switch {
	case q.Restore != nil:
		f.orders = append(f.orders, *q.Restore)
	case q.Action == protocol.StandingActionSave:
		o := protocol.StandingOrder{ID: "so_new", Text: q.Text, SavedAt: 1700000000, Source: protocol.StandingSourceOwner}
		f.orders = append(f.orders, o)
		res.Order = &o
	case q.Action == protocol.StandingActionRemove:
		for i, o := range f.notes {
			if o.ID == q.ID {
				cp := o
				res.Order = &cp
				f.notes = append(f.notes[:i], f.notes[i+1:]...)
				res.Notes = f.notes
			}
		}
		for i, o := range f.orders {
			if o.ID == q.ID {
				cp := o
				res.Order = &cp
				f.orders = append(f.orders[:i], f.orders[i+1:]...)
				break
			}
		}
	}
	res.Orders = f.orders
	return json.Marshal(res)
}

func runStanding(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command")
	}
	next, _ := m.Update(cmd())
	return next.(Model)
}

func TestSettingsStandingOrdersAddRemoveUndo(t *testing.T) {
	f := &standingFake{fakeSource: fakeSource{snap: fakeSnapshot()}, orders: []protocol.StandingOrder{{ID: "so_1", Text: "Do what I say.", SavedAt: 1700000000, Source: protocol.StandingSourceCTO}}}
	m := newModel(120, 100)
	m.src = f
	m.tab = settingsTab
	m = runStanding(t, m, m.loadStandingOrders())
	view := ansi.Strip(m.testView())
	for _, want := range []string{"Standing orders and notes", "Do what I say.", "CTO,", "Add a standing order"} {
		if !strings.Contains(view, want) {
			t.Errorf("settings lacks %q:\n%s", want, view)
		}
	}

	m = selectSettingsKind(m, "standing-add")
	m, _ = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.mode != modeStandingAdd {
		t.Fatalf("enter on add: mode %v", m.mode)
	}
	m = typeText(m, "Keep it simple.")
	m, cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = runStanding(t, m, cmd)
	last := f.calls[len(f.calls)-1]
	if last.Action != protocol.StandingActionSave || last.Text != "Keep it simple." {
		t.Fatalf("save call: %+v", last)
	}
	if !strings.Contains(ansi.Strip(m.testView()), "Keep it simple.") || !strings.Contains(ansi.Strip(m.testView()), "you,") {
		t.Fatal("the saved order is not listed as the owner's")
	}

	// x removes the selected order, u puts it back.
	for i, row := range m.settingsItems() {
		if row.kind == "standing" && m.standing[row.index].ID == "so_1" {
			m.settingsRow = i
		}
	}
	m, cmd = press(m, keyRunes("x"))
	m = runStanding(t, m, cmd)
	last = f.calls[len(f.calls)-1]
	if last.Action != protocol.StandingActionRemove || last.ID != "so_1" || len(m.standing) != 1 {
		t.Fatalf("remove: %+v standing=%+v", last, m.standing)
	}
	if m.standingRemoved == nil || m.standingRemoved.ID != "so_1" {
		t.Fatal("the removed order is not kept for undo")
	}
	m, cmd = press(m, keyRunes("u"))
	m = runStanding(t, m, cmd)
	last = f.calls[len(f.calls)-1]
	if last.Restore == nil || last.Restore.ID != "so_1" || last.Restore.Source != protocol.StandingSourceCTO || len(m.standing) != 2 {
		t.Fatalf("undo: %+v standing=%+v", last, m.standing)
	}
}

// The owner sees the notes agents keep where the standing orders are, with
// their scope and kind, sees which ones nobody used for 30 days, and deletes one
// with x. The frames are the text the owner reads at 80x24 and 120x34.
func TestSettingsListsNotesWithScopeKindAndStaleFlagAndDeletes(t *testing.T) {
	notes := []protocol.StandingOrder{
		{ID: "so_n1", Name: "deploy-order", Description: "what ships first", Kind: "fact", Scope: "project", ScopeRef: "demo", SavedAt: 1700000000, Source: protocol.StandingSourceAgent},
		{ID: "so_n2", Name: "report-style", Description: "short plain words", Kind: "feedback", Scope: "owner", SavedAt: 1700000000, Source: protocol.StandingSourceAgent, Stale: true},
	}
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		f := &standingFake{fakeSource: fakeSource{snap: fakeSnapshot()}, notes: append([]protocol.StandingOrder(nil), notes...),
			orders: []protocol.StandingOrder{{ID: "so_1", Text: "Do what I say.", SavedAt: 1700000000, Source: protocol.StandingSourceOwner}}}
		m := newModel(size[0], size[1])
		m.src = f
		m.tab = settingsTab
		m = runStanding(t, m, m.loadStandingOrders())
		// The last row is selected, so the list is on screen down to it.
		m.settingsRow = len(m.settingsItems()) - 1
		for i, row := range m.settingsItems() {
			if row.kind == "note" {
				m.settingsRow = i
			}
		}
		view := ansi.Strip(m.testView())
		for _, want := range []string{"Standing orders and notes", "Note [fact] deploy-order", "(project demo)", "Note [feedback] report-style", "(owner)", "unused 30 days"} {
			if !strings.Contains(view, want) {
				t.Errorf("%dx%d lacks %q:\n%s", size[0], size[1], want, view)
			}
		}
		golden(t, fmt.Sprintf("settings_notes_%dx%d", size[0], size[1]), m.testView())
	}

	f := &standingFake{fakeSource: fakeSource{snap: fakeSnapshot()}, notes: append([]protocol.StandingOrder(nil), notes...)}
	m := newModel(120, 34)
	m.src = f
	m.tab = settingsTab
	m = runStanding(t, m, m.loadStandingOrders())
	m = selectSettingsKind(m, "note")
	m, cmd := press(m, keyRunes("x"))
	m = runStanding(t, m, cmd)
	last := f.calls[len(f.calls)-1]
	if last.Action != protocol.StandingActionRemove || last.ID != "so_n1" || len(m.notes) != 1 {
		t.Fatalf("delete note: %+v notes=%+v", last, m.notes)
	}
	m, cmd = press(m, keyRunes("u"))
	m = runStanding(t, m, cmd)
	if last = f.calls[len(f.calls)-1]; last.Restore == nil || last.Restore.Name != "deploy-order" {
		t.Fatalf("undo must carry the whole note: %+v", last)
	}
}
