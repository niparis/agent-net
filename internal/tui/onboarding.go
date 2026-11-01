package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/niparis/agent-net/internal/protocol"
)

type onboardingStep struct{ id, label, text string }
type onboardingWriteRequest struct {
	method string
	params any
}

type onboardingMsg struct {
	value    protocol.Onboarding
	err      error
	revision int
	write    bool
}

func (m Model) onboardingWrite(method string, params any) (Model, tea.Cmd) {
	if !m.onboardingLoaded {
		return m, nil
	}
	if m.onboardingPending {
		m.onboardingQueue = append(m.onboardingQueue, onboardingWriteRequest{method: method, params: params})
		return m, nil
	}
	src, ok := m.src.(inboxCaller)
	if !ok {
		return m, nil
	}
	m.onboardingRevision++
	m.onboardingPending = true
	rev := m.onboardingRevision
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		raw, err := src.Call(ctx, method, params)
		var value protocol.Onboarding
		if err == nil {
			err = json.Unmarshal(raw, &value)
		}
		return onboardingMsg{value: value, err: err, revision: rev, write: true}
	}
}
func (m Model) onboardingDone(id string) (Model, tea.Cmd) {
	return m.onboardingWrite("onboarding_set", protocol.OnboardingUpdate{Step: id, State: "done"})
}
func (m Model) onboardingComplete(id string) (Model, tea.Cmd) {
	if m.onboardingStep().id != id {
		return m, nil
	}
	return m.onboardingDone(id)
}

// onboardingStep chooses the first applicable lesson. Inapplicable lessons are
// left untouched so they can appear when the network changes.
func (m Model) onboardingStep() onboardingStep {
	if !m.onboardingLoaded || m.onboarding.Disabled {
		return onboardingStep{}
	}
	done := func(id string) bool { return m.onboarding.Steps[id] != "" }
	var online []protocol.AgentView
	for _, a := range m.snap.Agents {
		if a.Online && !protocol.IsOwner(a.Harness) {
			online = append(online, a)
		}
	}
	name := ""
	if len(online) > 0 {
		name = sanitize(firstNonEmpty(online[0].Profile.Name, firstNonEmpty(online[0].Title, online[0].SessionID)))
	}
	if !done("see") {
		if name == "" {
			return onboardingStep{"see", "1/7 See", "No agents yet. Start an agent in a project: it appears here."}
		}
		for _, a := range online {
			if a.Waiting != nil {
				return onboardingStep{"see", "1/7 See", sanitize(firstNonEmpty(a.Profile.Name, firstNonEmpty(a.Title, a.SessionID))) + " waits for you. Enter opens the session."}
			}
		}
		return onboardingStep{"see", "1/7 See", "All your agents, live. Enter opens " + name + "."}
	}
	if !done("see-machine") && len(m.snap.Nodes) == 1 {
		return onboardingStep{"see-machine", "1/7 See", `Ask the CTO to add a machine: "add a machine".`}
	}
	if !done("talk") {
		return onboardingStep{"talk", "2/7 Talk", "Agents ask each other. Open Messages or send a message with m."}
	}
	if !done("cto") {
		return onboardingStep{"cto", "3/7 CTO", `Ask the CTO in plain words: "what is going on?"`}
	}
	if !done("map") {
		for _, a := range online {
			project := firstNonEmpty(a.ProjectDisplayName, firstNonEmpty(a.Repo.Project, a.Profile.Project))
			if project != "" && (a.Charter.Purpose == "" || a.Charter.PurposeFallback) && a.Charter.Maturity == "" && a.Charter.Accepts == "" && a.Charter.Refuses == "" && a.Charter.FeatureRequests == "" {
				return onboardingStep{"map", "4/7 Graph", "Ask the CTO to draft a charter for " + sanitize(project) + "."}
			}
		}
	}
	if !done("work-worker") {
		return onboardingStep{"work-worker", "5/7 Work", `Ask an agent to use a worker, or press s to start one.`}
	}
	if !done("work-protect") && !m.snap.ProtectedProviders.Configured {
		relevant := m.quotaRelevantProviders()
		for _, row := range m.quotaRows() {
			p := row.provider
			if relevant[p.Provider] && !quotaNotSetUp(p) && p.Error == "" && !p.NeedsAttention {
				return onboardingStep{"work-protect", "6/7 Work", "Protect " + sanitize(p.Provider) + " limits for your main sessions: 3, then p."}
			}
		}
	}
	if !done("tools") && len(m.snap.Nodes) > 1 && m.onboardingToolsKnown && m.onboardingToolsCount > 0 {
		return onboardingStep{"tools", "7/7 Tools", fmt.Sprintf("Your machines differ in %d tools. Press 5 to review them.", m.onboardingToolsCount)}
	}
	return onboardingStep{}
}

func (m Model) onboardingLine(w int) string {
	step := m.onboardingStep()
	if step.id == "" {
		line := "enter: open the " + m.ctoTitle() + " · tab: back"
		if m.focus == focusCTO {
			line += " · w: drive 30m"
		}
		return sDim.Render(clip(line, w))
	}
	label := lipgloss.NewStyle().Foreground(roleAccent).Render(step.label)
	line := label + "  " + step.text
	if m.focus == focusCTO && lipgloss.Width(line)+len("  esc skip") <= w {
		line += sDim.Render("  esc skip")
	}
	return clip(line, w)
}

func (m Model) onboardingLastUserMessage(events protocol.CTOEvents) string {
	last := ""
	for _, raw := range events.Messages {
		var v struct {
			Type string `json:"type"`
			Role string `json:"role"`
		}
		if json.Unmarshal(raw, &v) == nil && (strings.EqualFold(v.Type, "user") || strings.EqualFold(v.Role, "user")) {
			last = string(raw)
		}
	}
	return last
}

func (m Model) onboardingHasPeerThread() bool {
	for _, item := range m.feed.items {
		if !item.isMessage() || item.FromCTO || item.From == "" || item.To == "" {
			continue
		}
		from, a := m.agentByID(item.From)
		to, b := m.agentByID(item.To)
		if a && b && !protocol.IsOwner(from.Harness) && !protocol.IsOwner(to.Harness) {
			return true
		}
	}
	return false
}

func (m Model) onboardingAfterFetch(before string, events []protocol.FeedEvent) (Model, tea.Cmd) {
	if !m.onboardingLoaded || m.onboardingPending || m.onboarding.Disabled {
		return m, nil
	}
	online := false
	charter := false
	worker := false
	for _, a := range m.snap.Agents {
		if a.Online && !protocol.IsOwner(a.Harness) {
			online = true
			if a.Charter.Purpose != "" && !a.Charter.PurposeFallback || a.Charter.Maturity != "" || a.Charter.Accepts != "" || a.Charter.Refuses != "" || a.Charter.FeatureRequests != "" {
				charter = true
			}
		}
		if a.Worker != nil {
			worker = true
		}
	}
	for _, item := range m.feed.items {
		if item.Kind == "worker.started" {
			worker = true
		}
	}
	for _, e := range events {
		if e.Kind == "worker.started" {
			worker = true
		}
	}
	if before == "see" && !online {
		m.onboardingSawNoAgents = true
	}
	switch {
	case before == "see" && online && m.onboardingSawNoAgents:
		m.onboardingSawNoAgents = false
		return m.onboardingDone("see")
	case before == "see-machine" && len(m.snap.Nodes) > 1:
		return m.onboardingDone("see-machine")
	case before == "map" && charter:
		return m.onboardingDone("map")
	case before == "work-worker" && worker:
		return m.onboardingDone("work-worker")
	case before == "tools" && m.onboardingToolsKnown && m.onboardingToolsCount == 0:
		return m.onboardingDone("tools")
	case before == "work-protect" && m.snap.ProtectedProviders.Configured:
		return m.onboardingDone("work-protect")
	}
	return m, nil
}
