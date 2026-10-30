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
	sent   []string
	events protocol.CTOEvents
	fresh  int
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
