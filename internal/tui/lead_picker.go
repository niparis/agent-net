package tui

import (
	"context"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/niparis/agent-net/internal/protocol"
)

type leadChoice struct {
	name string
	ref  *protocol.GraphRef
}
type leadPicker struct {
	target  protocol.GraphRef
	name    string
	choices []leadChoice
	index   int
	back    inputMode
}

func (m Model) startLeadPicker(target protocol.GraphRef, name string) Model {
	p := leadPicker{target: target, name: name, back: m.mode, choices: []leadChoice{{name: "Clear lead"}}}
	eligible := map[string]bool{}
	if target.Kind == "team" {
		for _, e := range m.mp.full.Edges {
			if e.Kind == "member_of" && e.FromRef != nil && e.FromRef.Kind == "agent" && e.ToRef != nil && *e.ToRef == target {
				eligible[e.FromRef.ID] = true
			}
		}
	} else {
		for _, n := range m.mp.full.Nodes {
			if n.Ref == nil || *n.Ref != target {
				continue
			}
			if n.Owner != nil {
				eligible[n.Owner.ID] = true
			}
			for _, session := range n.Sessions {
				for _, a := range m.snap.Agents {
					if a.SessionID == session.ID && a.AgentID != "" {
						eligible[a.AgentID] = true
					}
				}
			}
		}
		for _, a := range m.snap.Agents {
			if a.AgentID != "" && (a.RepoContext == target.ID || a.Repo.Project == target.ID) {
				eligible[a.AgentID] = true
			}
		}
	}
	for _, n := range m.mp.full.Nodes {
		if n.Ref != nil && n.Ref.Kind == "agent" && eligible[n.Ref.ID] && (n.Active == nil || *n.Active) {
			ref := *n.Ref
			name := n.Name
			if label := m.mp.labels[graphRefKey(n.Ref)]; label != "" {
				name = strings.TrimPrefix(label, "agent ")
			}
			p.choices = append(p.choices, leadChoice{name: name, ref: &ref})
		}
	}
	sort.Slice(p.choices[1:], func(i, j int) bool { return p.choices[i+1].name < p.choices[j+1].name })
	for _, n := range m.mp.full.Nodes {
		if n.Ref != nil && *n.Ref == target && n.Lead != nil {
			for i, choice := range p.choices {
				if choice.ref != nil && *choice.ref == *n.Lead {
					p.index = i
				}
			}
		}
	}
	m.leadPicker = p
	m.mode = modeLeadPicker
	m.notice = notice{}
	return m
}
func (m Model) leadPickerKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := &m.leadPicker
	switch k.String() {
	case "esc":
		m.mode = p.back
		return m, nil
	case "up", "k":
		p.index = max(0, p.index-1)
	case "down", "j":
		p.index = min(len(p.choices)-1, p.index+1)
	case "enter":
		q := protocol.LeadSetRequest{Ref: p.target, Lead: p.choices[p.index].ref}
		m.mode = p.back
		src, ok := m.src.(inboxCaller)
		if !ok {
			m.fail("Lead settings unavailable.")
			return m, nil
		}
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), Timeout)
			defer cancel()
			_, err := src.Call(ctx, protocol.OpLeadSet, q)
			return teamWriteMsg{done: "Lead saved.", err: err}
		}
	}
	return m, nil
}
func (m Model) leadPickerView(w, rows int) []string {
	p := m.leadPicker
	out := []string{section("Lead for "+sanitize(p.name), w)}
	visible := max(rows-4, 1)
	start := max(0, p.index-visible+1)
	for i := start; i < len(p.choices) && i < start+visible; i++ {
		c := p.choices[i]
		mark := "  "
		if i == p.index {
			mark = "> "
		}
		out = append(out, clip(mark+sanitize(c.name), w))
	}
	out = append(out, "", sDim.Render(clip("  up/down select · enter save · esc cancel", w)))
	for len(out) < rows {
		out = append(out, "")
	}
	if len(out) > rows {
		out = out[:rows]
	}
	return out
}
