package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/niparis/agent-net/internal/protocol"
)

func (m Model) quotaConfirmKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	yes, no := confirmAnswer(k)
	switch {
	case no:
		m.mode = modeNormal
		m.say("Protection unchanged.")
	case yes:
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
		src, name, protecting := m.src, m.quotaConfirm, !m.quotaUnprotect
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
				return actionMsg{err: actionErr(err, "Cannot change protection: %s", explain(err))}
			}
			step := ""
			if protecting {
				step = "work-protect"
			}
			return actionMsg{onboarding: step, text: "Protection updated for " + name + "."}
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

func (m Model) quotaFloorKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch keyAction(contextForm, k.String()) {
	case "cancel":
		m.mode = modeNormal
		m.say("Worker floor unchanged.")
		return m, nil
	case "submit":
		valStr := strings.TrimSpace(m.rename.Value())
		if valStr == "" {
			valStr = "0"
		}
		floor, err := strconv.Atoi(valStr)
		if err != nil || floor < 0 || floor > 95 {
			m.fail("Floor must be 0 to 95 percent (0 = off).")
			return m, nil
		}
		m.mode = modeNormal
		provider := m.quotaConfirm
		wf := m.snap.WorkerFloors
		newFloors := make(map[string]int, len(wf.Floors)+1)
		for k, v := range wf.Floors {
			newFloors[k] = v
		}
		if floor == 0 {
			delete(newFloors, provider)
			delete(newFloors, strings.ToLower(m.quotaConfirmDisplay))
		} else {
			newFloors[provider] = floor
		}
		wf.Floors = newFloors
		wf.Configured = true
		m.snap.WorkerFloors = wf

		statusText := fmt.Sprintf("Worker floor set to %d%% for %s.", floor, provider)
		if floor == 0 {
			statusText = fmt.Sprintf("Worker floor off for %s.", provider)
		}
		m.say(statusText)

		src := m.src
		return m, func() tea.Msg {
			caller, ok := src.(interface {
				Call(context.Context, string, any) (json.RawMessage, error)
			})
			if !ok {
				return actionMsg{err: fmt.Errorf("Cannot change worker floor: operator does not support it")}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := caller.Call(ctx, protocol.OpWorkerFloorsSet, wf); err != nil {
				return actionMsg{err: actionErr(err, "Cannot change worker floor: %s", explain(err))}
			}
			return actionMsg{text: statusText}
		}
	}
	var cmd tea.Cmd
	m.rename, cmd = m.rename.Update(k)
	return m, cmd
}
