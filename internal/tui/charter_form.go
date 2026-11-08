package tui

import (
	"context"
	"encoding/json"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/niparis/agent-net/internal/protocol"
)

// A field is edited at one level. Inherited text is shown for reference but
// never copied into an override merely by opening the form.
type charterForm struct {
	level, scope string
	charter      protocol.Charter
	attrs        []string
	cur          int
	input        textinput.Model
	err          string
}
type charterSavedMsg struct{ err error }

func (m *Model) startCharterForm(n protocol.Node) {
	f := charterForm{level: "repo", scope: n.Name, charter: n.Charter, input: newTextInput()}
	if n.Ref != nil {
		f.level, f.scope = n.Ref.Kind, n.Ref.ID
	}
	f.input.Prompt = ""
	f.input.CharLimit = protocol.CharterValueMaxBytes
	f.input.SetVirtualCursor(false)
	for _, a := range protocol.CharterGuide() {
		if !protocol.CharterAttributeAtLevel(f.level, a.Name) {
			continue
		}
		f.attrs = append(f.attrs, a.Name)
	}
	m.charterForm = f
	m.charterForm.load()
	m.mode = modeCharterForm
}
func (f *charterForm) load() {
	value := ""
	if f.charter.Sources != nil {
		for _, s := range (*f.charter.Sources)[f.attrs[f.cur]] {
			if s.Level == f.level && s.Scope == f.scope {
				value = s.Value
			}
		}
	} else {
		// Legacy gateways return local fields without provenance.
		raw, _ := json.Marshal(f.charter)
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		_ = json.Unmarshal(fields[f.attrs[f.cur]], &value)
	}
	f.input.SetValue(value)
	f.input.CursorEnd()
}
func (m Model) charterFormKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := &m.charterForm
	switch k.String() {
	case "esc":
		m.mode = modeNormal
		return m, nil
	case "tab", "down", "shift+tab", "up":
		step := 1
		if k.String() == "shift+tab" || k.String() == "up" {
			step = len(f.attrs) - 1
		}
		f.cur = (f.cur + step) % len(f.attrs)
		f.load()
		f.err = ""
		return m, f.input.Focus()
	case "enter":
		attr, value := f.attrs[f.cur], f.input.Value()
		if _, err := protocol.CleanCharterValue(attr, value); err != nil {
			f.err = err.Error()
			return m, nil
		}
		q := protocol.CharterSetRequest{Level: f.level, Node: f.scope, Attr: attr, Value: value}
		src := m.src
		return m, func() tea.Msg {
			caller, ok := src.(inboxCaller)
			if !ok {
				return charterSavedMsg{ErrTooOld}
			}
			ctx, cancel := context.WithTimeout(context.Background(), Timeout)
			defer cancel()
			_, err := caller.Call(ctx, protocol.OpCharterSet, q)
			return charterSavedMsg{err}
		}
	}
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(k)
	return m, cmd
}
func (m Model) charterFormView(w, rows int) []string {
	f := m.charterForm
	out := []string{section("Charter: "+f.level+" "+sanitize(f.scope), w)}
	out = append(out, "  tab/up/down field; enter saves field; empty inherits; esc returns")
	for i, a := range f.attrs {
		prefix := "  "
		if i == f.cur {
			prefix = "> "
		}
		label := a
		if f.charter.Sources != nil {
			var levels []string
			for _, s := range (*f.charter.Sources)[a] {
				levels = append(levels, s.Level+" "+s.Scope)
			}
			if len(levels) > 0 {
				label += " (" + strings.Join(levels, ", ") + ")"
			}
		}
		out = append(out, clip(prefix+label, w))
	}
	out = append(out, "", clip(f.input.View(), w))
	if guide, ok := protocol.CharterGuideFor(f.attrs[f.cur]); ok {
		out = append(out, wrap(guide.Meaning, w)...)
	}
	if f.err != "" {
		out = append(out, sBad.Render(clip(f.err, w)))
	}
	for len(out) < rows {
		out = append(out, "")
	}
	return out[:min(len(out), rows)]
}
