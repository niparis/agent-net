package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

type fakeCTOSource struct {
	*fakeSource
	saved    protocol.CTOSettingsRequest
	settings protocol.CTOSettings
	sent     []string
	events   protocol.CTOEvents
	pages    map[int]protocol.CTOEvents
	fresh    int
	history  []protocol.CTOGraphChange
	undone   []int64
}

func (f *fakeCTOSource) CTOEnter(context.Context) (protocol.EnterPlan, error) {
	return protocol.EnterPlan{Argv: []string{"fake"}}, nil
}
func (f *fakeCTOSource) CTOSend(_ context.Context, text string) error {
	f.sent = append(f.sent, text)
	return nil
}
func (f *fakeCTOSource) CTOEvents(_ context.Context, index int) (protocol.CTOEvents, error) {
	if page, ok := f.pages[index]; ok {
		return page, nil
	}
	return f.events, nil
}
func (f *fakeCTOSource) CTOSettings(context.Context) (protocol.CTOSettings, error) {
	if f.settings.Harness == "" && f.settings.Model == "" && f.settings.OpenCodeModel == "" && f.settings.ClaudeModel == "" {
		return protocol.CTOSettings{Model: "fake/m1", Harness: "opencode", OpenCodeModel: "fake/m1"}, nil
	}
	return f.settings, nil
}
func (f *fakeCTOSource) SetCTOSettings(_ context.Context, req protocol.CTOSettingsRequest) (protocol.CTOSettings, error) {
	f.saved = req
	if req.Harness != nil {
		f.settings.Harness = *req.Harness
	}
	if req.Model != nil {
		if f.settings.Harness == "claude" {
			f.settings.ClaudeModel = *req.Model
		} else {
			f.settings.OpenCodeModel = *req.Model
		}
		f.settings.Model = *req.Model
	}
	return f.settings, nil
}
func (f *fakeCTOSource) CTOGraphHistory(context.Context, int64) ([]protocol.CTOGraphChange, error) {
	return f.history, nil
}
func (f *fakeCTOSource) CTOUndo(_ context.Context, id int64) error {
	f.undone = append(f.undone, id)
	return nil
}
func (f *fakeCTOSource) CTONew(context.Context) error { f.fresh++; return nil }
func TestCTOPanePollAndEnter(t *testing.T) {
	src := &fakeCTOSource{fakeSource: &fakeSource{snap: fakeSnapshot()}, events: protocol.CTOEvents{Messages: []json.RawMessage{json.RawMessage(`{"type":"assistant","text":"hello"}`)}, Next: 1}}
	m := New(src)
	m.width = 80
	m.height = 24
	updated, _ := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("C")})
	m = updated.(Model)
	if m.cto == nil || m.focus != focusCTO {
		t.Fatal("C did not focus the CTO box")
	}
	msg := m.ctoPollCmd()()
	updated, _ = m.update(msg)
	m = updated.(Model)
	if !strings.Contains(m.View(), "fake/m1") || !strings.Contains(m.View(), "hello") {
		t.Fatalf("missing model or reply:\n%s", m.View())
	}
	updated, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("question")})
	m = updated.(Model)
	updated, cmd := m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("Enter did not request the CTO plan")
	}
	if _, ok := cmd().(enterMsg); !ok {
		t.Fatal("Enter did not return an enter plan")
	}
	if len(src.sent) != 0 {
		t.Fatalf("the read-only box sent a message: %v", src.sent)
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
	for _, want := range []string{"sent to tenebrae-53", "started worker shop", "read graph", "failed charter_set"} {
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
	a, c, d := strings.Index(joined, "you:"), strings.Index(joined, "sent to"), strings.Index(joined, "cto:")
	if strings.Contains(joined, "charter") || !(a < c && c < d) {
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
	if got := m.ctoView(80, 4)[0]; !strings.Contains(got, "CTO · claude · opus") {
		t.Fatalf("title: %s", got)
	}
}

func TestCTOTitlePrefersModelInUse(t *testing.T) {
	src := &fakeCTOSource{fakeSource: &fakeSource{}, events: protocol.CTOEvents{Model: "fake/m2"}}
	m := New(src)
	updated, _ := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("C")})
	m = updated.(Model)
	updated, _ = m.update(m.ctoPollCmd()())
	m = updated.(Model)
	if m.cto.configuredModel != "fake/m1" {
		t.Fatalf("saved model changed unexpectedly: %q", m.cto.configuredModel)
	}
	if got := m.ctoView(80, 4)[0]; !strings.Contains(got, "CTO · opencode · fake/m2") {
		t.Fatalf("title does not show model in use: %s", got)
	}
}

func TestCTOMessageShowsHarnessFailure(t *testing.T) {
	for _, raw := range []string{`{"type":"idle","outcome":"failed","error":{"type":"provider.upstream","message":"unknown model"}}`, `{"type":"assistant","error":{"name":"APIError","data":{"message":"no credentials"}}}`} {
		if got := ctoMessage(json.RawMessage(raw)); !strings.HasPrefix(got, "error: ") {
			t.Errorf("failure hidden: %s -> %q", raw, got)
		}
	}
}

func TestCTOEnterOnFocusedBoxRequestsPlan(t *testing.T) {
	src := &fakeCTOSource{fakeSource: &fakeSource{}}
	m := New(src)
	model, _ := m.startCTO()
	m = model.(Model)
	_, cmd := m.ctoKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter did not request a plan")
	}
	if _, ok := cmd().(enterMsg); !ok {
		t.Fatal("enter did not return an enter plan")
	}
}

func TestCTOBoxHasNoInput(t *testing.T) {
	src := &fakeCTOSource{fakeSource: &fakeSource{}}
	m := New(src)
	model, _ := m.startCTO()
	m = model.(Model)
	m.cto.messages = []string{"cto: hello"}
	for _, key := range []string{"hello", "x", "a"} {
		next, cmd := m.ctoKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		if cmd != nil {
			t.Fatalf("%q started a command", key)
		}
		m = next.(Model)
	}
	if len(src.sent) != 0 || len(m.cto.messages) != 1 || m.cto.messages[0] != "cto: hello" {
		t.Fatalf("typing changed the box: sent=%v messages=%v", src.sent, m.cto.messages)
	}
}

func TestCTOBoxOKeyDoesNothing(t *testing.T) {
	src := &fakeCTOSource{fakeSource: &fakeSource{}}
	m := New(src)
	model, _ := m.startCTO()
	m = model.(Model)
	m.focus = focusCTO
	next, cmd := m.ctoKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd != nil {
		t.Fatal("o still opened the full CTO")
	}
	if next.(Model).focus != focusCTO {
		t.Fatal("o changed the focus")
	}
}

func TestCTOBoxHintAndHelp(t *testing.T) {
	m := New(&fakeCTOSource{fakeSource: &fakeSource{}})
	model, _ := m.startCTO()
	m = model.(Model)
	m.focus = focusCTO
	view := ansi.Strip(strings.Join(m.ctoView(80, 8), "\n"))
	if !strings.Contains(view, "enter: open the CTO · tab: back") {
		t.Fatalf("box hint missing:\n%s", view)
	}
	line := ansi.Strip(m.helpLine(100))
	for _, want := range []string{"enter", "open CTO", "tab", "back"} {
		if !strings.Contains(line, want) {
			t.Errorf("key line missing %q: %s", want, line)
		}
	}
	for _, old := range []string{"send", "full screen"} {
		if strings.Contains(line, old) {
			t.Errorf("key line still says %q: %s", old, line)
		}
	}
	help := ansi.Strip(strings.Join(m.helpContent(100), "\n"))
	if !strings.Contains(help, "enter opens the full CTO") {
		t.Fatalf("help does not describe Enter: %s", help)
	}
}

func TestCTOModelFormSaveAndCancel(t *testing.T) {
	src := &fakeCTOSource{fakeSource: &fakeSource{snap: fakeSnapshot()}}
	m := keys(New(src), "C", "m")
	if m.cto.settingsForm == nil {
		t.Fatal("model form did not open")
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRight})
	m = send(m, tea.KeyMsg{Type: tea.KeyTab})
	m.cto.settingsForm.input.SetValue("opus")
	next, cmd := m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("save did not start")
	}
	next, _ = m.update(cmd())
	m = next.(Model)
	if src.saved.Harness == nil || *src.saved.Harness != "claude" || src.saved.Model == nil || *src.saved.Model != "opus" || !strings.Contains(m.View(), "CTO · claude · opus") {
		t.Fatalf("save: %+v view %s", src.saved, m.View())
	}
	m = keys(m, "m")
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.cto.settingsForm != nil || m.focus != focusCTO {
		t.Fatal("cancel failed")
	}
}

func TestCTOModelFormSwapsPerHarnessModelsAndSuggestions(t *testing.T) {
	snap := fakeSnapshot()
	for i := range snap.Nodes {
		if snap.Nodes[i].NodeID != snap.NodeID {
			continue
		}
		for j := range snap.Nodes[i].Harnesses {
			switch snap.Nodes[i].Harnesses[j].Name {
			case "opencode":
				snap.Nodes[i].Harnesses[j].Models = []string{"openai/gpt-6"}
			case "claude":
				snap.Nodes[i].Harnesses[j].Models = []string{"claude-sonnet-5-5", "claude-haiku-4-5"}
			}
		}
	}
	src := &fakeCTOSource{fakeSource: &fakeSource{snap: snap}, settings: protocol.CTOSettings{
		Model: "openai/gpt-6", Harness: "opencode", OpenCodeModel: "openai/gpt-6", ClaudeModel: "haiku",
	}}
	m := New(src)
	m.snap = snap
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = keys(m, "C")
	next, _ := m.update(m.ctoPollCmd()())
	m = next.(Model)
	m = keys(m, "m")
	if got := m.cto.settingsForm.input.Value(); got != "openai/gpt-6" {
		t.Fatalf("OpenCode model field = %q", got)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRight})
	if got := m.cto.settingsForm.input.Value(); got != "haiku" {
		t.Fatalf("Claude model field = %q", got)
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"Model:   haiku", "Suggestions: haiku, claude-haiku-4-5"} {
		if !strings.Contains(view, want) {
			t.Errorf("Claude form missing %q:\n%s", want, view)
		}
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyLeft})
	if got := m.cto.settingsForm.input.Value(); got != "openai/gpt-6" {
		t.Fatalf("OpenCode model was not restored: %q", got)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRight})
	if got := m.cto.settingsForm.input.Value(); got != "haiku" {
		t.Fatalf("Claude model was not restored: %q", got)
	}
	m.cto.settingsForm.input.SetValue("")
	view = ansi.Strip(m.View())
	for _, want := range []string{"Model:   default", "Suggestions: opus, sonnet, haiku, fable", "claude-sonnet-5-5"} {
		if !strings.Contains(view, want) {
			t.Errorf("Claude default form missing %q:\n%s", want, view)
		}
	}
	m.cto.settingsForm.input.SetValue("claude-sonnet")
	m = send(m, tea.KeyMsg{Type: tea.KeyDown})
	m = send(m, tea.KeyMsg{Type: tea.KeyTab})
	if got := m.cto.settingsForm.input.Value(); got != "claude-sonnet-5-5" {
		t.Fatalf("Claude tab completion = %q", got)
	}
}

func TestCTOModelFormGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		snap := fakeSnapshot()
		for i := range snap.Nodes {
			if snap.Nodes[i].NodeID != snap.NodeID {
				continue
			}
			for j := range snap.Nodes[i].Harnesses {
				if snap.Nodes[i].Harnesses[j].Name == "claude" {
					snap.Nodes[i].Harnesses[j].Models = []string{"claude-sonnet-5-5"}
				}
			}
		}
		src := &fakeCTOSource{fakeSource: &fakeSource{snap: snap}}
		m := New(src)
		m.loc, m.now = time.UTC, func() time.Time { return t0 }
		m = send(m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = refresh(m, src.fakeSource)
		m.snap = snap
		m.src = src
		m = send(m, m.ctoPollCmd()())
		m = keys(m, "C", "m")
		golden(t, fmt.Sprintf("cto_model_%dx%d", size[0], size[1]), m.View())
		m = send(m, tea.KeyMsg{Type: tea.KeyRight})
		golden(t, fmt.Sprintf("cto_model_claude_default_%dx%d", size[0], size[1]), m.View())
	}
}

func TestCTOProviderErrorsSuggestModelChange(t *testing.T) {
	for _, msg := range []string{"insufficient credits", "quota exceeded", "unknown model"} {
		got := ctoMessage(json.RawMessage(fmt.Sprintf(`{"type":"idle","error":{"message":%q}}`, msg)))
		if !strings.HasSuffix(got, " (m to change the model)") {
			t.Errorf("missing hint: %q", got)
		}
	}
	if got := ctoMessage(json.RawMessage(`{"error":{"message":"network unavailable"}}`)); strings.Contains(got, "m to change") {
		t.Fatal(got)
	}
}

func TestCTOWrapsLongLines(t *testing.T) {
	m := Model{cto: &ctoState{messages: []string{"cto: " + strings.Repeat("word ", 18)}, err: strings.Repeat("failure ", 16)}}
	view := m.ctoView(30, 12)
	joined := strings.Join(view, "\n")
	if !strings.Contains(joined, "  failure") {
		t.Fatalf("lines not wrapped:\n%s", joined)
	}
	for _, line := range view {
		if strings.Contains(line, "…") && !strings.Contains(line, "enter: open the CTO") {
			t.Fatalf("line clipped: %q", line)
		}
		if ansi.StringWidth(line) > 30 {
			t.Fatalf("line too wide: %q", line)
		}
	}
	m.cto.messages = []string{strings.Repeat("x", 90), "newest"}
	m.cto.err = ""
	view = m.ctoView(30, 4)
	if !strings.Contains(strings.Join(view, "\n"), "newest") {
		t.Fatal("newest line hidden")
	}
}

func TestCTOLongErrorGolden(t *testing.T) {
	src := &fakeCTOSource{fakeSource: &fakeSource{snap: fakeSnapshot()}}
	m := New(src)
	m.loc, m.now = time.UTC, func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = refresh(m, src.fakeSource)
	m.src = src
	m = send(m, m.ctoPollCmd()())
	m = keys(m, "C")
	m.cto.err = "Provider request failed because the account has insufficient credits. Add credits or choose another model before retrying the request. The request was not completed."
	golden(t, "cto_long_error_80x24", m.View())
}

func TestCTOMultilineReplyPreserved(t *testing.T) {
	m := New(&fakeCTOSource{fakeSource: &fakeSource{}})
	m.cto = &ctoState{generation: 1}
	m.applyCTOPoll(ctoPollMsg{generation: 1, events: protocol.CTOEvents{Messages: []json.RawMessage{json.RawMessage(`{"type":"assistant","text":"first\nsecond\n\nlast"}`)}}})
	if got := strings.Join(m.ctoView(40, 10), "\n"); !strings.Contains(got, "first") || !strings.Contains(got, "second") || strings.Contains(got, "first second") {
		t.Fatalf("lost line breaks: %s", got)
	}
}

func TestCTOMarkdownReply(t *testing.T) {
	m := New(&fakeCTOSource{fakeSource: &fakeSource{}})
	m.cto = &ctoState{messages: []string{"cto: **bold** and `code`\n\n| attr | draft |\n| --- | --- |\n| one | two |", "you: **plain**"}}
	got := ansi.Strip(strings.Join(m.ctoView(60, 15), "\n"))
	if strings.Contains(got, "**bold**") || strings.Contains(got, "`code`") || !strings.Contains(got, "bold") || !strings.Contains(got, "one") || !strings.Contains(got, "**plain**") {
		t.Fatalf("bad markdown rendering:\n%s", got)
	}
}

func TestCTOConversationScroll(t *testing.T) {
	m := New(&fakeCTOSource{fakeSource: &fakeSource{}})
	m.focus = focusCTO
	m.cto = &ctoState{messages: []string{"one", "two", "three", "four", "five", "six", "seven"}}
	view := func() string { return ansi.Strip(strings.Join(m.ctoView(40, 6), "\n")) }
	if got := view(); !strings.Contains(got, "seven") || !strings.Contains(got, "↑ more") {
		t.Fatalf("initial view: %s", got)
	}
	next, _ := m.ctoKey(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(Model)
	next, _ = m.ctoKey(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(Model)
	if got := view(); strings.Contains(got, "seven") || !strings.Contains(got, "one") {
		t.Fatalf("scrolled view: %s", got)
	}
	next, _ = m.ctoKey(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = next.(Model)
	next, _ = m.ctoKey(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = next.(Model)
	if got := view(); !strings.Contains(got, "seven") {
		t.Fatalf("returned view: %s", got)
	}
}

func TestCTOMarkdownGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m := New(&fakeCTOSource{fakeSource: &fakeSource{snap: fakeSnapshot()}})
		m.cto = &ctoState{messages: []string{"you: Please draft the charter.", "cto: Here is the **draft** with `scope`:\n\n| attr | draft |\n| --- | --- |\n| purpose | Build tools |\n| scope | The network |\n| owner | Team |\n| maturity | Early |\n| inputs | Requests |\n| outputs | Results |\n| dependencies | Gateway |\n| limits | No deploys |\n| review | Weekly |\n\nLet me know what you would change."}}
		m.focus = focusCTO
		view := strings.Join(m.ctoView(size[0], size[1]), "\n")
		golden(t, fmt.Sprintf("cto_markdown_%dx%d", size[0], size[1]), view)
	}
}

func TestCTOEnterEmptyPlanShowsError(t *testing.T) {
	m := New(&fakeCTOSource{fakeSource: &fakeSource{}})
	m, _ = m.onEnter(enterMsg{name: "CTO"})
	if !m.notice.err || !strings.Contains(m.notice.text, "Cannot enter CTO") {
		t.Fatalf("empty CTO plan must show an error: %+v", m.notice)
	}
}

func TestCTOGroupsCharterActions(t *testing.T) {
	m := New(&fakeCTOSource{fakeSource: &fakeSource{}})
	m.cto = &ctoState{generation: 1}
	m.applyCTOPoll(ctoPollMsg{generation: 1, events: protocol.CTOEvents{Actions: []protocol.CTOAction{
		{Text: "charter cogforge: purpose updated · u to undo", At: 1},
		{Text: "charter cogforge: maturity updated · u to undo", At: 2},
	}}})
	if len(m.cto.messages) != 1 || m.cto.messages[0] != "charter cogforge: 2 fields updated · u to undo" {
		t.Fatalf("charter group: %v", m.cto.messages)
	}
}

func TestCTOMergedTimelineKeepsSwitchDivider(t *testing.T) {
	m := New(&fakeCTOSource{fakeSource: &fakeSource{}})
	m.cto = &ctoState{generation: 1}
	m.applyCTOPoll(ctoPollMsg{generation: 1, harness: "claude", model: "opus", events: protocol.CTOEvents{
		Messages: []json.RawMessage{json.RawMessage(`{"type":"user","text":"old message","time":{"created":1000}}`), json.RawMessage(`{"type":"assistant","text":"new message","time":{"created":3000}}`)},
		Actions:  []protocol.CTOAction{{Text: "switched to claude · opus · 14:02", At: 2_000_000_000}},
	}})
	joined := strings.Join(m.cto.messages, "|")
	if !strings.Contains(joined, "old message") || !strings.Contains(joined, "switched to claude · opus · 14:02") || !strings.Contains(joined, "new message") {
		t.Fatal(joined)
	}
	m.applyCTOPoll(ctoPollMsg{generation: 1, harness: "claude", model: "opus", events: protocol.CTOEvents{
		Messages: []json.RawMessage{json.RawMessage(`{"type":"user","text":"old message","time":{"created":1000}}`), json.RawMessage(`{"type":"assistant","text":"new message","time":{"created":3000}}`)},
		Actions:  []protocol.CTOAction{{Text: "switched to claude · opus · 14:02", At: 2_000_000_000}},
	}})
	if joined = strings.Join(m.cto.messages, "|"); strings.Count(joined, "old message") != 1 || strings.Count(joined, "new message") != 1 {
		t.Fatal(joined)
	}
}

func TestCTOSwitchDividerGolden(t *testing.T) {
	m := New(&fakeCTOSource{fakeSource: &fakeSource{}})
	m.cto = &ctoState{generation: 1}
	m.focus = focusCTO
	m.applyCTOPoll(ctoPollMsg{generation: 1, harness: "claude", model: "opus", events: protocol.CTOEvents{
		Messages: []json.RawMessage{json.RawMessage(`{"type":"user","text":"OpenCode message","time":{"created":1793282700000}}`), json.RawMessage(`{"type":"assistant","text":"Claude reply","time":{"created":1793282820000}}`)},
		Actions:  []protocol.CTOAction{{Text: "switched to claude · opus · 14:07", At: 1793282760000000000}},
	}})
	golden(t, "cto_harness_switch_80x10", strings.Join(m.ctoView(80, 10), "\n"))
}

func TestCTOScrollLoadsOlderTimelinePage(t *testing.T) {
	src := &fakeCTOSource{fakeSource: &fakeSource{}, pages: map[int]protocol.CTOEvents{
		-1: {Messages: []json.RawMessage{json.RawMessage(`{"type":"user","text":"older","time":{"created":1000}}`)}, Older: 0},
	}}
	m := New(src)
	m.cto = &ctoState{generation: 1}
	m.applyCTOPoll(ctoPollMsg{generation: 1, harness: "opencode", model: "fake/m1", events: protocol.CTOEvents{
		Messages: []json.RawMessage{json.RawMessage(`{"type":"user","text":"newer","time":{"created":2000}}`)}, Older: 1,
	}})
	m.cto.scrollMax = 0
	m.cto.pageRows = 1
	next, cmd := m.ctoKey(tea.KeyMsg{Type: tea.KeyPgUp})
	if cmd == nil {
		t.Fatal("scrolling past the oldest loaded entry did not request a page")
	}
	m = next.(Model)
	page := cmd()
	next, _ = m.Update(page)
	m = next.(Model)
	joined := strings.Join(m.cto.messages, "|")
	if !strings.Contains(joined, "you: older") || !strings.Contains(joined, "you: newer") || m.cto.olderCursor != 0 {
		t.Fatalf("older page was not added: messages=%q cursor=%d", joined, m.cto.olderCursor)
	}
}
