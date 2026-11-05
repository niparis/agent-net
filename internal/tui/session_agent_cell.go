package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/niparis/agent-net/internal/protocol"
)

func canChangeSessionAgent(a protocol.AgentView) bool {
	return a.Online && a.Worker == nil && !protocol.IsOwner(a.Harness) && a.Harness != "cto" && a.Harness != "bot"
}

// cycleSessionAgent uses the gateway's active agent names and saves the next
// choice for this registered session. IDs never appear in the screen.
func (m Model) cycleSessionAgent() (Model, tea.Cmd) {
	a, ok := m.selectedAgent()
	if !ok || !canChangeSessionAgent(a) {
		m.fail("Pick a running agent session.")
		return m, nil
	}
	caller, ok := m.src.(inboxCaller)
	if !ok {
		m.fail("Changing the agent is unavailable.")
		return m, nil
	}
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		raw, err := caller.Call(ctx, protocol.OpSessionAgentList, struct{}{})
		if err != nil {
			return actionMsg{err: fmt.Errorf("Agent not changed: %s", explain(err))}
		}
		var options protocol.SessionAgentOptions
		if err = json.Unmarshal(raw, &options); err != nil {
			return actionMsg{err: fmt.Errorf("Agent not changed: invalid agent list")}
		}
		if len(options.Agents) == 0 {
			return actionMsg{err: fmt.Errorf("Agent not changed: no agents are available")}
		}
		index := -1
		for i, option := range options.Agents {
			if option.ID == a.AgentID {
				index = i
				break
			}
		}
		next := options.Agents[(index+1)%len(options.Agents)]
		if next.ID == a.AgentID {
			return actionMsg{text: "Only one agent is available."}
		}
		_, err = caller.Call(ctx, protocol.OpSessionAgentSet, protocol.SessionAgentSetRequest{SessionID: a.SessionID, Agent: next.ID})
		if err != nil {
			return actionMsg{err: fmt.Errorf("Agent not changed: %s", explain(err))}
		}
		return actionMsg{text: "Agent changed to " + sanitize(next.Name) + "."}
	}
}
