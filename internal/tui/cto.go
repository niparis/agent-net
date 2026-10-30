package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

type ctoSource interface {
	CTOSend(context.Context, string) error
	CTONew(context.Context) error
	CTOSettings(context.Context) (protocol.CTOSettings, error)
	CTOEvents(context.Context, int) (protocol.CTOEvents, error)
	CTOGraphHistory(context.Context) ([]protocol.CTOGraphChange, error)
	CTOUndo(context.Context, int64) error
}
type ctoState struct {
	input            textinput.Model
	editing, polling bool
	model, err       string
	messages         []string
	history          []protocol.CTOGraphChange
	local            []string
	historyLoaded    bool
	lastGraphID      int64
	generation       int
}
type ctoPollMsg struct {
	events     protocol.CTOEvents
	history    []protocol.CTOGraphChange
	model      string
	err        error
	generation int
}
type ctoSendMsg struct {
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
		history, err := src.CTOGraphHistory(ctx)
		model := settings.Model
		if model == "" {
			model = "default"
		}
		return ctoPollMsg{events: events, history: history, model: model, err: err, generation: generation}
	}
}
func ctoMessage(raw json.RawMessage) string {
	var v struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return ""
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
	if k.Type == tea.KeyEsc {
		m.cto = nil
		return m, nil
	}
	if !c.editing {
		switch k.String() {
		case "enter", "i":
			c.editing = true
			return m, c.input.Focus()
		case "u":
			if len(c.history) == 0 {
				c.err = "No CTO graph change to undo"
				return m, nil
			}
			id := c.history[len(c.history)-1].ID
			src := m.src.(ctoSource)
			g := c.generation
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), Timeout)
				defer cancel()
				return ctoUndoMsg{id: id, err: src.CTOUndo(ctx, id), generation: g}
			}
		case "n":
			src := m.src.(ctoSource)
			g := c.generation
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), Timeout)
				defer cancel()
				return ctoNewMsg{err: src.CTONew(ctx), generation: g}
			}
		}
		return m, nil
	}
	if k.Type == tea.KeyEnter {
		text := strings.TrimSpace(c.input.Value())
		if text == "" {
			c.editing = false
			c.input.Blur()
			return m, nil
		}
		c.input.SetValue("")
		c.editing = false
		c.input.Blur()
		src := m.src.(ctoSource)
		g := c.generation
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), operator.StartTimeout)
			defer cancel()
			return ctoSendMsg{err: src.CTOSend(ctx, text), generation: g}
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
	c.model = r.model
	c.messages = nil
	c.history = r.history
	for _, h := range r.history {
		switch h.Kind {
		case "charter":
			c.messages = append(c.messages, fmt.Sprintf("charter %s.%s: %s -> %s", h.Node, h.Attr, h.OldValue, h.NewValue))
		case "edge":
			c.messages = append(c.messages, "edge "+h.OldValue+" -> "+h.NewValue)
		case "node":
			c.messages = append(c.messages, "node "+h.Node+": "+h.OldValue+" -> "+h.NewValue)
		}
	}
	for _, a := range r.events.Actions {
		c.messages = append(c.messages, a.Text)
	}
	c.messages = append(c.messages, c.local...)
	for _, raw := range r.events.Messages {
		if text := ctoMessage(raw); text != "" {
			c.messages = append(c.messages, text)
		}
	}
}
