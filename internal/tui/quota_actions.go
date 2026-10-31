package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/niparis/agent-net/internal/protocol"
)

func (m Model) quotaConfirmKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "n", "N", "esc":
		m.mode = modeNormal
		m.say("Protection unchanged.")
	case "y", "Y":
		m.mode = modeNormal
		p := m.snap.ProtectedProviders
		p.Providers = append([]string(nil), p.Providers...)
		var providers []string
		for _, name := range p.Providers {
			if !strings.EqualFold(name, m.quotaConfirm) && !(m.quotaUnprotect && protocol.QuotaProviderForModelProvider(name) != "" && protocol.QuotaProviderForModelProvider(name) == protocol.QuotaProviderForModelProvider(m.quotaConfirm)) {
				providers = append(providers, name)
			}
		}
		if !m.quotaUnprotect {
			providers = append(providers, m.quotaConfirm)
		}
		p.Providers = providers
		if p.Reason == "" && len(providers) > 0 {
			p.Reason = "Saved for your orchestrator"
		}
		src, name := m.src, m.quotaConfirm
		m.say("Saving protection...")
		return m, func() tea.Msg {
			caller, ok := src.(interface {
				Call(context.Context, string, any) (json.RawMessage, error)
			})
			if !ok {
				return actionMsg{err: fmt.Errorf("Cannot change protection: operator does not support it")}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := caller.Call(ctx, protocol.OpProtectedProvidersSet, p); err != nil {
				return actionMsg{err: fmt.Errorf("Cannot change protection: %s", explain(err))}
			}
			return actionMsg{text: "Protection updated for " + name + "."}
		}
	}
	return m, nil
}
