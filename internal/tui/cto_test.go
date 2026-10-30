package tui

import (
	"context"
	"encoding/json"
	"errors"
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

func (f *fakeCTOSource) CTOEnter(context.Context) (protocol.EnterPlan, error) {
	return protocol.EnterPlan{Argv: []string{"fake"}}, nil
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
func (f *fakeCTOSource) CTOGraphHistory(context.Context, int64) ([]protocol.CTOGraphChange, error) {
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
	msg := m.ctoPollCmd()()
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
	if m.cto == nil || m.focus != focusSessions {
		t.Fatal("Esc did not return focus to sessions")
	}
}

func TestCTOBoxBottomAlignsTheConversation(t *testing.T) {
	m := New(&fakeCTOSource{fakeSource: &fakeSource{snap: fakeSnapshot()}})
	m.width, m.height = 80, 24
	m.cto = &ctoState{messages: []string{"oldest", "newest"}}
	out := m.ctoView(80, 10)
	if len(out) != 10 {
		t.Fatalf("ctoView gave %d lines", len(out))
	}
	if !strings.Contains(out[len(out)-3], "newest") {
		t.Fatalf("newest line is not above the input:\n%s", strings.Join(out, "\n"))
	}
	if !strings.Contains(out[len(out)-4], "oldest") {
		t.Fatalf("oldest line is not above newest:\n%s", strings.Join(out, "\n"))
	}
	if strings.TrimSpace(strings.Trim(out[1], "│")) != "" {
		t.Fatalf("conversation starts at the top of the box:\n%s", strings.Join(out, "\n"))
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
	updated, _ = m.update(m.ctoPollCmd()())
	m = updated.(Model)
	if m.mapOn || m.focus != focusCTO || !strings.Contains(m.View(), "CTO") {
		t.Fatal("C did not focus the CTO on Sessions")
	}
	updated, cmd = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = updated.(Model)
	if cmd != nil || !m.cto.confirming {
		t.Fatal("new conversation did not ask for confirmation")
	}
	updated, cmd = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = updated.(Model)
	updated, _ = m.update(cmd())
	m = updated.(Model)
	if src.fresh != 1 {
		t.Fatal("new conversation not requested")
	}
	updated, _ = m.update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.cto == nil || m.focus != focusSessions {
		t.Fatal("escape did not return to sessions")
	}
}

func TestCTOPaneShowsRecordedActions(t *testing.T) {
	src := &fakeCTOSource{fakeSource: &fakeSource{snap: fakeSnapshot()}, events: protocol.CTOEvents{Actions: []protocol.CTOAction{{ID: 1, Text: "sent to tenebrae-53: hello"}, {ID: 2, Text: "started worker shop"}, {ID: 3, Text: "read graph"}, {ID: 4, Text: "failed charter_set: no such node"}}}, history: []protocol.CTOGraphChange{{ID: 8, Kind: "charter", Node: "cogforge", Attr: "maturity", OldValue: "early", NewValue: "mature"}}}
	m := New(src)
	m.width = 80
	m.height = 24
	updated, _ := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("C")})
	m = updated.(Model)
	updated, _ = m.update(m.ctoPollCmd()())
	m = updated.(Model)
	v := strings.Join(m.cto.messages, "\n")
	for _, want := range []string{"sent to tenebrae-53", "started worker shop", "read graph", "failed charter_set", "cogforge.maturity: early -> mature"} {
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
	updated, _ = m.update(m.ctoPollCmd()())
	m = updated.(Model)
	m.mapOn = true
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
	if !strings.Contains(strings.Join(m.cto.messages, "\n"), "undid graph change") {
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
	updated, _ = m.update(m.ctoPollCmd()())
	m = updated.(Model)
	m.mapOn = true
	src.history = []protocol.CTOGraphChange{{ID: 5, Kind: "node", Node: "architect", NewValue: "architect"}}
	m.cto.polling = true
	updated, cmd = m.update(m.ctoPollCmd()())
	m = updated.(Model)
	if cmd == nil || !m.mp.loading {
		t.Fatal("CTO graph change did not refresh map")
	}
}

func TestCTOPaneSanitizesConversation(t *testing.T) {
	src := &fakeCTOSource{fakeSource: &fakeSource{}, events: protocol.CTOEvents{Messages: []json.RawMessage{json.RawMessage(`{"type":"assistant","text":"bad\u001b[2Jtext"}`)}, Actions: []protocol.CTOAction{{Text: "sent: \x1b[2J"}}}}
	m := New(src)
	updated, _ := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("C")})
	m = updated.(Model)
	updated, _ = m.update(m.ctoPollCmd()())
	m = updated.(Model)
	if strings.Contains(m.View(), "\x1b") {
		t.Fatal("terminal control escaped the CTO pane")
	}
}

func TestCTOActionOrderFollowsConversation(t *testing.T) {
	src := &fakeCTOSource{fakeSource: &fakeSource{}, events: protocol.CTOEvents{Messages: []json.RawMessage{json.RawMessage(`{"type":"user","text":"change it","time":{"created":1000}}`), json.RawMessage(`{"type":"assistant","content":[{"type":"text","text":"done"}],"time":{"created":3000}}`)}, Actions: []protocol.CTOAction{{Text: "sent to peer: hello", At: 2500000000}}}, history: []protocol.CTOGraphChange{{Kind: "charter", Node: "node", Attr: "purpose", NewValue: "new", At: 2000000000}}}
	m := New(src)
	updated, _ := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("C")})
	m = updated.(Model)
	updated, _ = m.update(m.ctoPollCmd()())
	m = updated.(Model)
	joined := strings.Join(m.cto.messages, "|")
	a, b, c, d := strings.Index(joined, "you:"), strings.Index(joined, "charter"), strings.Index(joined, "sent to"), strings.Index(joined, "cto:")
	if !(a < b && b < c && c < d) {
		t.Fatalf("wrong order: %s", joined)
	}
}

func TestCTOPanePollingUpdatesStreamingMessage(t *testing.T) {
	src := &fakeCTOSource{fakeSource: &fakeSource{}, events: protocol.CTOEvents{Messages: []json.RawMessage{json.RawMessage(`{"id":"msg_1","type":"assistant","content":[{"type":"text","text":"par"}]}`)}, Next: 1}}
	m := New(src)
	updated, cmd := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("C")})
	m = updated.(Model)
	updated, _ = m.update(m.ctoPollCmd()())
	m = updated.(Model)
	src.events.Messages[0] = json.RawMessage(`{"id":"msg_1","type":"assistant","content":[{"type":"text","text":"partial reply"}]}`)
	updated, cmd = m.update(tickMsg{})
	m = updated.(Model)
	if cmd == nil || !m.cto.polling {
		t.Fatal("tick did not poll CTO")
	}
	updated, _ = m.update(m.ctoPollCmd()())
	m = updated.(Model)
	if len(m.cto.messages) != 1 || !strings.Contains(m.cto.messages[0], "partial reply") {
		t.Fatalf("stream: %+v", m.cto.messages)
	}
}

func TestMapRefreshesWhenCTOPaneClosed(t *testing.T) {
	m := mapModel(120, 40, fakeGraph())
	m.mapOn = true
	m.mp.loading = false
	updated, cmd := m.update(tickMsg{})
	m = updated.(Model)
	if cmd == nil || !m.mp.loading {
		t.Fatal("open map did not refresh while CTO pane was closed")
	}
}

func TestCTOTitleShowsHarnessAndModel(t *testing.T) {
	m := Model{cto: &ctoState{harness: "claude", model: "opus"}}
	if got := m.ctoView(80, 4)[0]; !strings.Contains(got, "CTO (claude · opus)") {
		t.Fatalf("title: %s", got)
	}
}

func TestCTOMessageShowsHarnessFailure(t *testing.T) {
	for _, raw := range []string{`{"type":"idle","outcome":"failed","error":{"type":"provider.upstream","message":"unknown model"}}`, `{"type":"assistant","error":{"name":"APIError","data":{"message":"no credentials"}}}`} {
		if got := ctoMessage(json.RawMessage(raw)); !strings.HasPrefix(got, "error: ") {
			t.Errorf("failure hidden: %s -> %q", raw, got)
		}
	}
}

func TestCTOOpenFullRequestsPlan(t *testing.T) {
	src := &fakeCTOSource{fakeSource: &fakeSource{}}
	m := New(src)
	model, _ := m.startCTO()
	m = model.(Model)
	_, cmd := m.ctoKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd == nil {
		t.Fatal("open full did not request a plan")
	}
	if _, ok := cmd().(enterMsg); !ok {
		t.Fatal("open full did not return an enter plan")
	}
}

func TestCTOSendErrorSurvivesPoll(t *testing.T) {
	src := &fakeCTOSource{fakeSource: &fakeSource{}}
	model, _ := New(src).startCTO()
	m := model.(Model)
	updated, _ := m.Update(ctoSendMsg{err: errors.New("send failed"), generation: m.cto.generation})
	m = updated.(Model)
	m.applyCTOPoll(ctoPollMsg{generation: m.cto.generation})
	if !strings.Contains(strings.Join(m.cto.messages, "\n"), "error: send failed") {
		t.Fatalf("error lost: %+v", m.cto.messages)
	}
}
