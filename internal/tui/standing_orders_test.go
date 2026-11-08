package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

type standingFake struct {
	fakeSource
	orders []protocol.StandingOrder
	calls  []protocol.StandingOrdersRequest
}

func (f *standingFake) Call(_ context.Context, op string, params any) (json.RawMessage, error) {
	if op != protocol.OpStandingOrders {
		return json.RawMessage(`[]`), nil
	}
	q := params.(protocol.StandingOrdersRequest)
	f.calls = append(f.calls, q)
	var res protocol.StandingOrdersResult
	switch {
	case q.Restore != nil:
		f.orders = append(f.orders, *q.Restore)
	case q.Action == protocol.StandingActionSave:
		o := protocol.StandingOrder{ID: "so_new", Text: q.Text, SavedAt: 1700000000, Source: protocol.StandingSourceOwner}
		f.orders = append(f.orders, o)
		res.Order = &o
	case q.Action == protocol.StandingActionRemove:
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
	for _, want := range []string{"CTO standing orders", "Do what I say.", "CTO,", "Add a standing order"} {
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
