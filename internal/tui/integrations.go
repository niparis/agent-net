package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
)

// The integrations form (decision 0021): key I. The owner turns the six setup
// integrations on or off for the whole network. Every machine applies the
// saved answers at its next daemon start, or within a minute.

type integrationsForm struct {
	values  protocol.Integrations
	cur     int
	loading bool
	err     string
}

type integrationsLoadedMsg struct {
	res protocol.IntegrationsResult
	err error
}

type integrationsSavedMsg struct {
	res protocol.IntegrationsResult
	err error
}

func (m Model) startIntegrations() (Model, tea.Cmd) {
	m.intg = integrationsForm{loading: true}
	m.mode = modeIntegrations
	m.notice = notice{}
	return m, m.integrationsCmd()
}

func (m Model) integrationsCmd() tea.Cmd {
	src := m.src
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		res, err := src.Integrations(ctx)
		return integrationsLoadedMsg{res: res, err: err}
	}
}

func (m Model) onIntegrationsLoaded(r integrationsLoadedMsg) (Model, tea.Cmd) {
	if m.mode != modeIntegrations {
		return m, nil
	}
	m.intg.loading = false
	if r.err != nil {
		m.intg.err = explain(r.err)
		return m, nil
	}
	if r.res.Set {
		m.intg.values = r.res.Integrations
	}
	return m, nil
}

func (m Model) onIntegrationsSaved(r integrationsSavedMsg) (Model, tea.Cmd) {
	if r.err != nil {
		m.intg.err = explain(r.err)
		return m, nil
	}
	m.mode = modeNormal
	return m.onAction(actionMsg{text: "Integrations saved for all machines. Each machine applies them at its next daemon start or within a minute."})
}

func (m Model) integrationsKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := &m.intg
	switch k.Type {
	case tea.KeyEsc:
		m.mode = modeNormal
		m.say("Integrations unchanged.")
		return m, nil
	case tea.KeyUp, tea.KeyShiftTab:
		f.cur = (f.cur - 1 + len(protocol.IntegrationSpecs())) % len(protocol.IntegrationSpecs())
		return m, nil
	case tea.KeyDown, tea.KeyTab:
		f.cur = (f.cur + 1) % len(protocol.IntegrationSpecs())
		return m, nil
	case tea.KeyEnter:
		if f.loading {
			return m, nil
		}
		return m.saveIntegrations()
	}
	if k.String() == " " && !f.loading {
		s := protocol.IntegrationSpecs()[f.cur]
		f.values.Set(s.ID, !f.values.Get(s.ID))
		f.err = ""
	}
	return m, nil
}

func (m Model) saveIntegrations() (tea.Model, tea.Cmd) {
	f := m.intg
	src := m.src
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := src.SetIntegrations(ctx, f.values)
		return integrationsSavedMsg{res: res, err: err}
	}
}

func (m Model) integrationsView(w, rows int) []string {
	f := m.intg
	out := []string{section("Integrations (all machines)", w)}
	for i, s := range protocol.IntegrationSpecs() {
		mark := "  "
		if i == f.cur {
			mark = "> "
		}
		box := "[ ]"
		if f.values.Get(s.ID) {
			box = "[x]"
		}
		out = append(out, clip(mark+box+" "+sanitize(s.Label), w))
	}
	on := f.values.CountOn()
	out = append(out, "")
	out = append(out, sDim.Render(clip(fmt.Sprintf("  %d of %d on.", on, len(protocol.IntegrationSpecs())), w)))
	if f.loading {
		out = append(out, sDim.Render(clip("  Reading the answers from your network...", w)))
	} else if f.err != "" {
		out = append(out, sBad.Render(clip("  ! "+f.err, w)))
	} else {
		out = append(out, "")
	}
	out = append(out, sDim.Render(clip("  space toggles an item. Enter saves for every machine. Esc cancels.", w)))
	out = append(out, sDim.Render(clip("  A machine applies them at its next daemon start, or within a minute.", w)))
	for len(out) < rows {
		out = append(out, "")
	}
	return out[:rows]
}
