package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/niparis/agent-net/internal/protocol"
)

type fakeCTOSource struct {
	*fakeSource
	sent    []string
	events  protocol.CTOEvents
	fresh   int
	history []protocol.CTOGraphChange
	undone  []int64
}

func (f *fakeCTOSource) CTOSend(_ context.Context, text string) error {
	f.sent = append(f.sent, text)
	return nil
}
func (f *fakeCTOSource) CTOEvents(_ context.Context, index int) (protocol.CTOEvents, error) {
	return f.events, nil
}
func (f *fakeCTOSource) CTOSettings(context.Context) (protocol.CTOSettings, error) {
	return protocol.CTOSettings{Model: "fake/m1"}, nil
}
func (f *fakeCTOSource) CTOGraphHistory(context.Context) ([]protocol.CTOGraphChange, error) {
	return f.history, nil
}
func (f *fakeCTOSource) CTOUndo(_ context.Context, id int64) error {
	f.undone = append(f.undone, id)
	return nil
}
func (f *fakeCTOSource) CTONew(context.Context) error { f.fresh++; return nil }
func TestCTOPaneSendAndPoll(t *testing.T) {
	src := &fakeCTOSource{fakeSource: &fakeSource{snap: fakeSnapshot()}, events: protocol.CTOEvents{Messages: []json.RawMessage{json.RawMessage(`{"type":"assistant","text":"hello"}`)}, Next: 1}}
	m := New(src)
	m.width = 80
	m.height = 24
	updated, cmd := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("C")})
	m = updated.(Model)
	if m.cto == nil || cmd == nil {
		t.Fatal("C did not open the CTO")
	}
	msg := cmd()
	updated, _ = m.update(msg)
	m = updated.(Model)
	if !strings.Contains(m.View(), "fake/m1") || !strings.Contains(m.View(), "hello") {
		t.Fatalf("missing model or reply:\n%s", m.View())
	}
	updated, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	updated, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("question")})
	m = updated.(Model)
	updated, cmd = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("send did not start")
	}
	msg = cmd()
	updated, _ = m.update(msg)
	m = updated.(Model)
	if len(src.sent) != 1 || src.sent[0] != "question" {
		t.Fatalf("sent: %v", src.sent)
	}
	updated, _ = m.update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.cto != nil {
		t.Fatal("Esc did not close CTO")
	}
}

func TestCTOPaneNewConversationAndMap(t *testing.T) {
	src := &fakeCTOSource{fakeSource: &fakeSource{snap: fakeSnapshot(), graph: fakeGraph()}}
	m := New(src)
	m.width = 120
	m.height = 40
	m.mapOn = true
	m.mp.loaded = true
	updated, cmd := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("C")})
	m = updated.(Model)
	updated, _ = m.update(cmd())
	m = updated.(Model)
	if !strings.Contains(m.View(), "Map") || !strings.Contains(m.View(), "CTO") {
		t.Fatal("map and CTO not visible together")
	}
	updated, cmd = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("new conversation did not start")
	}
	updated, _ = m.update(cmd())
	m = updated.(Model)
	if src.fresh != 1 {
		t.Fatal("new conversation not requested")
	}
	updated, _ = m.update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.cto != nil || !m.mapOn {
		t.Fatal("closing CTO did not retain map")
	}
}

func TestCTOPaneShowsRecordedActions(t *testing.T) {
	src := &fakeCTOSource{fakeSource: &fakeSource{snap: fakeSnapshot()}, events: protocol.CTOEvents{Actions: []protocol.CTOAction{{ID: 1, Text: "sent to tenebrae-53: hello"}, {ID: 2, Text: "started worker shop"}}}, history: []protocol.CTOGraphChange{{ID: 8, Kind: "charter", Node: "cogforge", Attr: "maturity", OldValue: "early", NewValue: "mature"}}}
	m := New(src)
	m.width = 80
	m.height = 24
	updated, cmd := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("C")})
	m = updated.(Model)
	updated, _ = m.update(cmd())
	m = updated.(Model)
	v := m.View()
	for _, want := range []string{"sent to tenebrae-53", "started worker shop", "cogforge.maturity: early -> mature"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
}

func TestCTOShowsUndoAndRefreshesMap(t *testing.T) {
	src := &fakeCTOSource{fakeSource: &fakeSource{snap: fakeSnapshot(), graph: fakeGraph()}, history: []protocol.CTOGraphChange{{ID: 2, Kind: "charter", Node: "cogforge", Attr: "purpose", NewValue: "x"}, {ID: 9, Kind: "charter", Node: "cogforge", Attr: "maturity", NewValue: "mature"}}}
	m := New(src)
	m.width = 120
	m.height = 40
	m.mapOn = true
	updated, cmd := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("C")})
	m = updated.(Model)
	updated, _ = m.update(cmd())
	m = updated.(Model)
	updated, cmd = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("undo not started")
	}
	updated, cmd = m.update(cmd())
	m = updated.(Model)
	if len(src.undone) != 1 || src.undone[0] != 9 {
		t.Fatalf("wrong undo: %v", src.undone)
	}
	if !strings.Contains(m.View(), "undid graph change") {
		t.Fatalf("undo not shown:\n%s", m.View())
	}
	if cmd == nil || !m.mp.loading {
		t.Fatal("map not refreshed after undo")
	}
}

func TestCTOGraphChangeRefreshesOpenMap(t *testing.T) {
	src := &fakeCTOSource{fakeSource: &fakeSource{snap: fakeSnapshot(), graph: fakeGraph()}}
	m := New(src)
	m.width = 120
	m.height = 40
	m.mapOn = true
	updated, cmd := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("C")})
	m = updated.(Model)
	updated, _ = m.update(cmd())
	m = updated.(Model)
	src.history = []protocol.CTOGraphChange{{ID: 5, Kind: "node", Node: "architect", NewValue: "architect"}}
	m.cto.polling = true
	updated, cmd = m.update(m.ctoPollCmd()())
	m = updated.(Model)
	if cmd == nil || !m.mp.loading {
		t.Fatal("CTO graph change did not refresh map")
	}
}
