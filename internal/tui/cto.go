package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

type ctoSource interface {
	CTONew(context.Context) error
	CTOSettings(context.Context) (protocol.CTOSettings, error)
	SetCTOSettings(context.Context, protocol.CTOSettingsRequest) (protocol.CTOSettings, error)
	CTOGraphHistory(context.Context, int64) ([]protocol.CTOGraphChange, error)
	CTOUndo(context.Context, int64) error
}
type ctoWindowSource interface {
	CTOGraphWindow(context.Context, bool) (protocol.CTOGraphWindow, error)
}
type ctoWindowMsg struct {
	window     protocol.CTOGraphWindow
	err        error
	generation int
}

type ctoState struct {
	windowUntil                time.Time
	settingsForm               *ctoSettingsForm
	configuredModel            string
	polling                    bool
	confirming                 bool
	opening                    bool
	title, model, harness, err string
	enterNotice                string
	opencodeModel              string
	claudeModel                string
	messages                   []string
	renderCache                map[ctoRenderKey][]string
	scroll, pageRows           int
	scrollMax                  int
	olderCursor                int
	olderLoading               bool
	olderPages                 []protocol.CTOEvents
	lastPoll                   ctoPollMsg
	history                    []protocol.CTOGraphChange
	local                      []protocol.CTOAction
	historyLoaded              bool
	lastGraphID                int64
	generation                 int
}
type ctoSettingsForm struct {
	harness       string
	titleInput    textinput.Model
	input         textinput.Model
	field         int // 0 harness, 1 title, 2 model
	err           string
	opencodeDraft string
	claudeDraft   string
}
type ctoSettingsMsg struct {
	settings   protocol.CTOSettings
	err        error
	generation int
}
type ctoPollMsg struct {
	windowUntil     time.Time
	configuredModel string
	opencodeModel   string
	claudeModel     string
	title           string
	events          protocol.CTOEvents
	history         []protocol.CTOGraphChange
	model           string
	harness         string
	err             error
	generation      int
	olderPage       bool
}
type ctoOlderMsg struct {
	events     protocol.CTOEvents
	err        error
	generation int
}
type ctoUndoMsg struct {
	id         int64
	err        error
	generation int
}
type ctoNewMsg struct {
	err        error
	generation int
}

func (m Model) startCTO() (tea.Model, tea.Cmd) {
	if _, ok := m.src.(ctoSource); !ok {
		m.notice = notice{err: true, text: "CTO is unavailable"}
		return m, nil
	}
	m.cto = &ctoState{polling: true, generation: m.ctoGeneration + 1}
	m.ctoGeneration++
	return m, m.ctoPollCmd()
}

// ctoSessionID is the network session of the CTO. Before the snapshot lists it
// the fixed name is used, and the daemon resolves it.
func (m Model) ctoSessionID() string {
	for _, a := range m.snap.Agents {
		if a.Harness == "cto" {
			return a.SessionID
		}
	}
	return protocol.CTONodeName
}

// openCTO shows that the CTO is opening before the daemon answers, and ignores
// another Enter while that first open is still in flight. The Sessions row and
// the CTO box both use it, so both behave the same.
func (m Model) openCTO(id string) (Model, tea.Cmd) {
	if m.cto != nil {
		if m.cto.opening {
			return m, nil
		}
		c := *m.cto
		c.opening = true
		c.enterNotice = "Opening the CTO…"
		c.scroll = 0
		m.cto = &c
	}
	return m, m.ctoEnterCmd(id)
}

// ctoEnterCmd asks session_enter for the CTO session. The Sessions row and the
// CTO box use it, so both show the same plan.
func (m Model) ctoEnterCmd(id string) tea.Cmd {
	src, sock, hsock, hpane := m.src, m.tmuxSocket, m.herdrSocket, m.herdrPaneID
	name := m.ctoTitle()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), operator.StartTimeout)
		defer cancel()
		plan, err := src.SessionEnter(ctx, protocol.EnterRequest{SessionID: id, TmuxSocket: sock, HerdrSocket: hsock, HerdrPaneID: hpane})
		return enterMsg{name: name, plan: plan, err: err, cto: true}
	}
}
func (m Model) ctoPollCmd() tea.Cmd {
	src := m.src.(ctoSource)
	generation := m.cto.generation
	session := m.ctoSessionID()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		var window protocol.CTOGraphWindow
		if ws, ok := src.(ctoWindowSource); ok {
			var err error
			window, err = ws.CTOGraphWindow(ctx, false)
			if err != nil {
				return ctoPollMsg{err: err, generation: generation}
			}
		}
		settings, err := src.CTOSettings(ctx)
		if err != nil {
			return ctoPollMsg{err: err, generation: generation}
		}
		page, err := m.src.SessionText(ctx, protocol.TextQuery{SessionID: session, Limit: protocol.TextMaxLimit})
		if err != nil {
			return ctoPollMsg{err: err, generation: generation}
		}
		events := ctoEventsFromPage(page)
		history, err := src.CTOGraphHistory(ctx, m.cto.lastGraphID)
		model := settings.Model
		if model == "" {
			model = "default"
		}
		harness := settings.Harness
		if harness == "" {
			harness = "opencode"
		}
		openCodeModel, claudeModel := settings.OpenCodeModel, settings.ClaudeModel
		if openCodeModel == "" && claudeModel == "" && settings.Model != "" {
			if harness == "claude" {
				claudeModel = settings.Model
			} else {
				openCodeModel = settings.Model
			}
		}
		return ctoPollMsg{windowUntil: window.Until, events: events, history: history, model: model, harness: harness, title: protocol.CTOTitleOrDefault(settings.Title), configuredModel: settings.Model, opencodeModel: openCodeModel, claudeModel: claudeModel, err: err, generation: generation}
	}
}
func (m Model) ctoOlderCmd() tea.Cmd {
	generation, cursor := m.cto.generation, m.cto.olderCursor
	session := m.ctoSessionID()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		page, err := m.src.SessionText(ctx, protocol.TextQuery{SessionID: session, Before: strconv.Itoa(cursor), Limit: protocol.TextMaxLimit})
		return ctoOlderMsg{events: ctoEventsFromPage(page), err: err, generation: generation}
	}
}

// ctoEventsFromPage turns a page of the CTO session's text into the timeline
// the box draws. The daemon marks a graph action with the cto_action tool and a
// harness failure with cto_error.
func ctoEventsFromPage(page protocol.TextPage) protocol.CTOEvents {
	out := protocol.CTOEvents{Messages: []json.RawMessage{}, Actions: []protocol.CTOAction{}}
	for _, e := range page.Entries {
		switch e.Tool {
		case "cto_action":
			out.Actions = append(out.Actions, protocol.CTOAction{Text: e.Text, At: e.Time * int64(time.Millisecond)})
		case "cto_error":
			raw, _ := json.Marshal(map[string]any{"type": "assistant", "error": map[string]string{"message": e.Text}})
			out.Messages = append(out.Messages, raw)
		default:
			raw, _ := json.Marshal(map[string]any{"type": e.Role, "text": e.Text, "time": map[string]int64{"created": e.Time}})
			out.Messages = append(out.Messages, raw)
		}
	}
	if n, err := strconv.Atoi(page.Older); err == nil {
		out.Older = n
	}
	if n, err := strconv.Atoi(page.Newer); err == nil {
		out.Next = n
	}
	return out
}

func mergeCTOEventPages(older []protocol.CTOEvents, latest protocol.CTOEvents) protocol.CTOEvents {
	out := protocol.CTOEvents{Messages: []json.RawMessage{}, Actions: []protocol.CTOAction{}}
	for _, page := range older {
		out.Messages = append(out.Messages, page.Messages...)
		out.Actions = append(out.Actions, page.Actions...)
	}
	out.Messages = append(out.Messages, latest.Messages...)
	out.Actions = append(out.Actions, latest.Actions...)
	return out
}

func ctoMessage(raw json.RawMessage) string {
	var v struct {
		Type    string          `json:"type"`
		Error   json.RawMessage `json:"error"`
		Text    string          `json:"text"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	if len(v.Error) > 0 && string(v.Error) != "null" {
		var e struct {
			Message string `json:"message"`
			Name    string `json:"name"`
			Data    struct {
				Message string `json:"message"`
			} `json:"data"`
		}
		if json.Unmarshal(v.Error, &e) == nil {
			msg := firstNonEmpty(firstNonEmpty(e.Message, e.Data.Message), firstNonEmpty(e.Name, string(v.Error)))
			return "error: " + ctoErrorHint(msg)
		}
		return "error: " + ctoErrorHint(string(v.Error))
	}
	if v.Type != "user" && v.Type != "assistant" {
		return ""
	}
	text := v.Text
	for _, p := range v.Content {
		if p.Type == "text" {
			text += p.Text
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	who := "you"
	if v.Type == "assistant" {
		who = "cto"
	}
	return fmt.Sprintf("%s: %s", who, text)
}
func (m Model) ctoKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := m.cto
	if c.settingsForm != nil {
		return m.ctoSettingsKey(k)
	}
	key := keyAction(contextCTO, k.String())
	if key == "help" {
		m.openHelp()
		return m, nil
	}
	if key == "back" {
		c.confirming = false
		m.focus = focusSessions
		if m.tab == messagesTab && !m.mapOn {
			m.focus = focusFeed
		}
		return m, nil
	}
	if key == "skip tip" && !c.confirming && m.onboardingStep().id != "" {
		return m.onboardingWrite("onboarding_set", protocol.OnboardingUpdate{Step: m.onboardingStep().id, State: "skipped"})
	}
	if c.confirming {
		yes, _ := confirmAnswer(k)
		c.confirming = false
		if !yes {
			return m, nil
		}
		src := m.src.(ctoSource)
		g := c.generation
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), Timeout)
			defer cancel()
			return ctoNewMsg{err: src.CTONew(ctx), generation: g}
		}
	}
	switch key {
	case actPageUp:
		c.scroll += max(c.pageRows, 1)
		if c.scroll >= c.scrollMax && c.olderCursor > 0 && !c.olderLoading {
			c.olderLoading = true
			return m, m.ctoOlderCmd()
		}
		return m, nil
	case actPageDown:
		c.scroll = max(c.scroll-max(c.pageRows, 1), 0)
		return m, nil
	}
	switch key {
	case "drive window":
		src, ok := m.src.(ctoWindowSource)
		if !ok {
			c.err = "CTO graph window is unavailable"
			return m, nil
		}
		g := c.generation
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), Timeout)
			defer cancel()
			window, err := src.CTOGraphWindow(ctx, true)
			return ctoWindowMsg{window: window, err: err, generation: g}
		}
	case "model":
		ti := newTextInput()
		ti.Prompt = ""
		ti.CharLimit = 4000
		harness := c.harness
		if harness == "" {
			harness = "opencode"
		}
		ti.Placeholder = "default"
		ti.SetValue(c.modelForHarness(harness))
		title := newTextInput()
		title.Prompt = ""
		title.CharLimit = 24
		title.SetValue(protocol.CTOTitleOrDefault(c.title))
		c.settingsForm = &ctoSettingsForm{harness: harness, titleInput: title, input: ti, opencodeDraft: c.opencodeModel, claudeDraft: c.claudeModel}
		return m, nil
	case "undo":
		src := m.src.(ctoSource)
		g := c.generation
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), Timeout)
			defer cancel()
			history, err := src.CTOGraphHistory(ctx, m.cto.lastGraphID)
			if err != nil {
				return ctoUndoMsg{err: err, generation: g}
			}
			if len(history) == 0 {
				return ctoUndoMsg{err: errors.New("no CTO graph change to undo"), generation: g}
			}
			id := history[len(history)-1].ID
			return ctoUndoMsg{id: id, err: src.CTOUndo(ctx, id), generation: g}
		}
	case "new conversation":
		c.confirming = true
		return m, nil
	}
	if key == "open CTO" {
		return m.openCTO(m.ctoSessionID())
	}
	return m, nil
}
func (m *Model) applyCTOPoll(r ctoPollMsg) {
	c := m.cto
	if c == nil || c.generation != r.generation {
		return
	}
	c.polling = false
	if r.err != nil {
		c.err = sanitize(r.err.Error())
		return
	}
	c.err = ""
	if !r.olderPage {
		c.lastPoll = r
		c.olderCursor = r.events.Older
	}
	events := mergeCTOEventPages(c.olderPages, r.events)
	c.configuredModel = r.configuredModel
	c.opencodeModel = r.opencodeModel
	c.claudeModel = r.claudeModel
	c.model = r.model
	c.harness = r.harness
	c.windowUntil = r.windowUntil
	c.title = protocol.CTOTitleOrDefault(r.title)
	c.history = r.history
	type line struct {
		at   int64
		text string
	}
	var lines []line
	for _, raw := range events.Messages {
		if text := ctoMessage(raw); text != "" {
			var v struct {
				Time struct {
					Created int64 `json:"created"`
				} `json:"time"`
			}
			json.Unmarshal(raw, &v)
			lines = append(lines, line{v.Time.Created * int64(1000000), text})
		}
	}
	for _, a := range events.Actions {
		lines = append(lines, line{a.At, a.Text})
	}
	var pending []protocol.CTOAction
	for _, a := range c.local {
		found := false
		for _, saved := range events.Actions {
			if saved.Text == a.Text {
				found = true
				break
			}
		}
		if !found {
			pending = append(pending, a)
			lines = append(lines, line{a.At, a.Text})
		}
	}
	c.local = pending
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].at < lines[j].at })
	c.messages = nil
	for i := 0; i < len(lines); i++ {
		v := lines[i]
		node, _, ok := strings.Cut(strings.TrimPrefix(v.text, "charter "), ": ")
		if strings.HasPrefix(v.text, "charter ") && ok && strings.HasSuffix(v.text, " updated · u to undo") {
			j := i + 1
			for j < len(lines) && lines[j].at-v.at <= int64(5e9) && strings.HasPrefix(lines[j].text, "charter "+node+": ") && strings.HasSuffix(lines[j].text, " updated · u to undo") {
				j++
			}
			if j-i > 1 {
				v.text = fmt.Sprintf("charter %s: %d fields updated · u to undo", node, j-i)
				i = j - 1
			}
		}
		c.messages = append(c.messages, v.text)
	}
}

func (f *ctoSettingsForm) setField(n int) tea.Cmd {
	f.field = (n + 3) % 3
	f.titleInput.Blur()
	f.input.Blur()
	switch f.field {
	case 1:
		return f.titleInput.Focus()
	case 2:
		return f.input.Focus()
	}
	return nil
}

func (m Model) ctoSettingsKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := m.cto
	f := c.settingsForm
	switch keyAction(contextForm, k.String()) {
	case "cancel":
		c.settingsForm = nil
		return m, nil
	case "previous field":
		return m, f.setField(f.field - 1)
	case "next field":
		return m, f.setField(f.field + 1)
	case "tab field":
		if f.field == 2 {
			candidates := m.ctoModelCandidates(f.harness, f.input.Value())
			if len(candidates) == 1 {
				f.input.SetValue(candidates[0])
				f.input.CursorEnd()
				return m, nil
			}
		}
		return m, f.setField(f.field + 1)
	case "previous choice", "next choice":
		if f.field == 0 {
			if f.harness == "opencode" {
				f.opencodeDraft = f.input.Value()
				f.harness = "claude"
				f.input.SetValue(f.claudeDraft)
			} else {
				f.claudeDraft = f.input.Value()
				f.harness = "opencode"
				f.input.SetValue(f.opencodeDraft)
			}
			f.input.CursorEnd()
			return m, nil
		}
	case "submit":
		harness, model := f.harness, strings.TrimSpace(f.input.Value())
		title := strings.TrimSpace(f.titleInput.Value())
		if _, err := protocol.NormalizeCTOTitle(title); err != nil {
			f.err = err.Error()
			return m, nil
		}
		g := c.generation
		c.settingsForm = nil
		src := m.src.(ctoSource)
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), Timeout)
			defer cancel()
			settings, err := src.SetCTOSettings(ctx, protocol.CTOSettingsRequest{Title: &title, Harness: &harness, Model: &model})
			return ctoSettingsMsg{settings, err, g}
		}
	}
	switch f.field {
	case 1:
		var cmd tea.Cmd
		f.titleInput, cmd = f.titleInput.Update(k)
		return m, cmd
	case 2:
		var cmd tea.Cmd
		f.input, cmd = f.input.Update(k)
		return m, cmd
	}
	return m, nil
}

func (m Model) ctoModelCandidates(harness, query string) []string {
	var models []string
	if harness == "claude" {
		models = []string{"opus", "sonnet", "haiku", "fable"}
	}
	for _, n := range m.snap.Nodes {
		if n.NodeID != m.snap.NodeID {
			continue
		}
		for _, h := range n.Harnesses {
			if h.Name == harness {
				models = append(models, h.Models...)
			}
		}
	}
	var out []string
	seen := make(map[string]bool, len(models))
	for _, model := range models {
		if !seen[model] && strings.Contains(strings.ToLower(model), strings.ToLower(query)) {
			seen[model] = true
			out = append(out, model)
		}
	}
	return out
}

func (c *ctoState) modelForHarness(harness string) string {
	if harness == "claude" {
		return c.claudeModel
	}
	return c.opencodeModel
}

func ctoErrorHint(msg string) string {
	lower := strings.ToLower(msg)
	for _, term := range []string{"credit", "quota", "unknown model", "model not found", "model does not exist"} {
		if strings.Contains(lower, term) {
			return msg + " (m to change the model)"
		}
	}
	return msg
}
