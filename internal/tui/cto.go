package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

type ctoSource interface {
	CTOEnter(context.Context) (protocol.EnterPlan, error)
	CTOSend(context.Context, string) error
	CTONew(context.Context) error
	CTOSettings(context.Context) (protocol.CTOSettings, error)
	SetCTOSettings(context.Context, protocol.CTOSettingsRequest) (protocol.CTOSettings, error)
	CTOEvents(context.Context, int) (protocol.CTOEvents, error)
	CTOGraphHistory(context.Context, int64) ([]protocol.CTOGraphChange, error)
	CTOUndo(context.Context, int64) error
}
type ctoState struct {
	input               textinput.Model
	settingsForm        *ctoSettingsForm
	configuredModel     string
	editing, polling    bool
	confirming          bool
	model, harness, err string
	messages            []string
	history             []protocol.CTOGraphChange
	local               []protocol.CTOAction
	sendErrors          []ctoLocalError
	polledErrors        map[string]int
	historyLoaded       bool
	lastGraphID         int64
	generation          int
}
type ctoSettingsForm struct {
	harness    string
	input      textinput.Model
	modelFocus bool
}
type ctoSettingsMsg struct {
	settings   protocol.CTOSettings
	err        error
	generation int
}
type ctoPollMsg struct {
	configuredModel string
	events          protocol.CTOEvents
	history         []protocol.CTOGraphChange
	model           string
	harness         string
	err             error
	generation      int
}
type ctoLocalError struct {
	text     string
	at       int64
	baseline int
}
type ctoSendMsg struct {
	baseline   map[string]int
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
	ti := textinput.New()
	ti.Prompt = "> "
	ti.Placeholder = "write to the CTO..."
	ti.CharLimit = 65536
	m.cto = &ctoState{input: ti, polling: true, generation: m.ctoGeneration + 1}
	m.ctoGeneration++
	return m, m.ctoPollCmd()
}
func (m Model) ctoPollCmd() tea.Cmd {
	src := m.src.(ctoSource)
	generation := m.cto.generation
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		settings, err := src.CTOSettings(ctx)
		if err != nil {
			return ctoPollMsg{err: err, generation: generation}
		}
		events, err := src.CTOEvents(ctx, 0)
		if err != nil {
			return ctoPollMsg{err: err, generation: generation}
		}
		history, err := src.CTOGraphHistory(ctx, events.GraphAfter)
		model := settings.Model
		if model == "" && events.Model != "" {
			model = events.Model
		}
		if model == "" {
			model = "default"
		}
		harness := settings.Harness
		if harness == "" {
			harness = "opencode"
		}
		return ctoPollMsg{events: events, history: history, model: model, harness: harness, configuredModel: settings.Model, err: err, generation: generation}
	}
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
func (m Model) ctoKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := m.cto
	if c.settingsForm != nil {
		return m.ctoSettingsKey(k)
	}
	if k.Type == tea.KeyEsc {
		c.editing = false
		c.input.Blur()
		c.confirming = false
		m.focus = focusSessions
		if m.tab == 1 && !m.mapOn {
			m.focus = focusFeed
		}
		return m, nil
	}
	if k.String() == "tab" {
		c.editing = false
		c.input.Blur()
		m.focus = focusSessions
		if m.tab == 1 && !m.mapOn {
			m.focus = focusFeed
		}
		return m, nil
	}
	if c.confirming {
		c.confirming = false
		if k.String() != "y" {
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
	if c.input.Value() == "" {
		if _, ok := m.src.(ctoSource); !ok && (k.String() == "o" || k.String() == "n" || k.String() == "u") {
			c.err = "CTO is unavailable"
			return m, nil
		}
		switch k.String() {
		case "m":
			ti := textinput.New()
			ti.Prompt = ""
			ti.CharLimit = 4000
			ti.SetValue(c.configuredModel)
			harness := c.harness
			if harness == "" {
				harness = "opencode"
			}
			c.settingsForm = &ctoSettingsForm{harness: harness, input: ti}
			return m, nil
		case "o":
			src := m.src.(ctoSource)
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), operator.StartTimeout)
				defer cancel()
				plan, err := src.CTOEnter(ctx)
				return enterMsg{name: "CTO", plan: plan, err: err}
			}
		case "enter":
			c.editing = true
			return m, c.input.Focus()
		case "u":
			src := m.src.(ctoSource)
			g := c.generation
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), Timeout)
				defer cancel()
				history, err := src.CTOGraphHistory(ctx, 0)
				if err != nil {
					return ctoUndoMsg{err: err, generation: g}
				}
				if len(history) == 0 {
					return ctoUndoMsg{err: errors.New("no CTO graph change to undo"), generation: g}
				}
				id := history[len(history)-1].ID
				return ctoUndoMsg{id: id, err: src.CTOUndo(ctx, id), generation: g}
			}
		case "n":
			c.confirming = true
			return m, nil
		}
	}
	if k.Type == tea.KeyEnter {
		text := strings.TrimSpace(c.input.Value())
		if text == "" {
			return m, nil
		}
		c.input.SetValue("")
		src := m.src.(ctoSource)
		g := c.generation
		baseline := make(map[string]int, len(c.polledErrors))
		for text, count := range c.polledErrors {
			baseline[text] = count
		}
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), operator.StartTimeout)
			defer cancel()
			return ctoSendMsg{err: src.CTOSend(ctx, text), generation: g, baseline: baseline}
		}
	}
	var cmd tea.Cmd
	c.input, cmd = c.input.Update(k)
	return m, cmd
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
	c.configuredModel = r.configuredModel
	c.model = r.model
	c.harness = r.harness
	c.history = r.history
	type line struct {
		at   int64
		text string
	}
	var lines []line
	polledErrors := make(map[string]int)
	for _, raw := range r.events.Messages {
		if text := ctoMessage(raw); text != "" {
			var v struct {
				Time struct {
					Created int64 `json:"created"`
				} `json:"time"`
			}
			json.Unmarshal(raw, &v)
			lines = append(lines, line{v.Time.Created * int64(1000000), text})
			if strings.HasPrefix(text, "error: ") {
				polledErrors[text]++
			}
		}
	}
	for _, h := range r.history {
		var text string
		switch h.Kind {
		case "charter":
			text = fmt.Sprintf("charter %s.%s: %s -> %s", h.Node, h.Attr, h.OldValue, h.NewValue)
		case "edge":
			text = "edge " + h.OldValue + " -> " + h.NewValue
		case "node":
			text = "node " + h.Node + ": " + h.OldValue + " -> " + h.NewValue
		}
		if text != "" {
			lines = append(lines, line{h.At, text})
		}
	}
	for _, a := range r.events.Actions {
		lines = append(lines, line{a.At, a.Text})
	}
	var pending []protocol.CTOAction
	for _, a := range c.local {
		found := false
		for _, saved := range r.events.Actions {
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
	c.polledErrors = polledErrors
	var pendingErrors []ctoLocalError
	matched := make(map[string]int)
	for _, e := range c.sendErrors {
		next := max(e.baseline, matched[e.text]) + 1
		if next <= polledErrors[e.text] {
			matched[e.text] = next
			continue
		}
		pendingErrors = append(pendingErrors, e)
		lines = append(lines, line{e.at, e.text})
	}
	c.sendErrors = pendingErrors
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].at < lines[j].at })
	c.messages = nil
	for _, v := range lines {
		c.messages = append(c.messages, v.text)
	}
}

func (m Model) ctoSettingsKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := m.cto
	f := c.settingsForm
	switch k.Type {
	case tea.KeyEsc:
		c.settingsForm = nil
		return m, nil
	case tea.KeyUp, tea.KeyShiftTab:
		f.modelFocus = false
		f.input.Blur()
		return m, nil
	case tea.KeyDown:
		f.modelFocus = true
		return m, f.input.Focus()
	case tea.KeyTab:
		if !f.modelFocus {
			f.modelFocus = true
			return m, f.input.Focus()
		}
		candidates := m.ctoModelCandidates(f.harness, f.input.Value())
		if len(candidates) == 1 {
			f.input.SetValue(candidates[0])
			f.input.CursorEnd()
		}
		return m, nil
	case tea.KeyLeft, tea.KeyRight:
		if !f.modelFocus {
			if f.harness == "opencode" {
				f.harness = "claude"
			} else {
				f.harness = "opencode"
			}
			return m, nil
		}
	case tea.KeyEnter:
		harness, model := f.harness, strings.TrimSpace(f.input.Value())
		g := c.generation
		c.settingsForm = nil
		src := m.src.(ctoSource)
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), Timeout)
			defer cancel()
			settings, err := src.SetCTOSettings(ctx, protocol.CTOSettingsRequest{Harness: &harness, Model: &model})
			return ctoSettingsMsg{settings, err, g}
		}
	}
	if f.modelFocus {
		var cmd tea.Cmd
		f.input, cmd = f.input.Update(k)
		return m, cmd
	}
	return m, nil
}

func (m Model) ctoModelCandidates(harness, query string) []string {
	var models []string
	if harness == "claude" {
		models = []string{"opus", "sonnet", "fable"}
	} else {
		for _, n := range m.snap.Nodes {
			if n.NodeID == m.snap.NodeID {
				for _, h := range n.Harnesses {
					if h.Name == "opencode" {
						models = append(models, h.Models...)
					}
				}
			}
		}
	}
	var out []string
	for _, model := range models {
		if strings.Contains(strings.ToLower(model), strings.ToLower(query)) {
			out = append(out, model)
		}
	}
	return out
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
