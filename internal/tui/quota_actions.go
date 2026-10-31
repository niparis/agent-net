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
			if !strings.EqualFold(name, m.quotaConfirm) {
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

// quotaModelProvider resolves a quota reader's alias to the model provider.
func (m Model) quotaModelProvider(quota string) string {
	quota = strings.ToLower(quota)
	for _, p := range m.snap.ProtectedProviders.Providers {
		if strings.EqualFold(p, quota) || protocol.QuotaProviderForModelProvider(p) == quota {
			return p
		}
	}
	for _, p := range m.snap.WorkerProfiles {
		provider := protocol.ProfileProvider(p.Harness, p.Model)
		if protocol.QuotaProviderForModelProvider(provider) == quota {
			return provider
		}
	}
	switch quota {
	case "claude":
		return "anthropic"
	case "codex":
		return "openai"
	}
	return quota
}
