package tui

import (
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// Typed keys are private to the graph UI. Names remain presentation, so two
// records with the same display name remain independently selectable.
func graphRefKey(ref *protocol.GraphRef) string {
	if ref.Kind == "special" || ref.Kind == "repo" {
		return ref.ID
	}
	return ref.Kind + "\x00" + ref.ID
}

func (m *Model) prepareGraph(v protocol.GraphView) protocol.GraphView {
	v.Nodes = append([]protocol.Node(nil), v.Nodes...)
	v.Edges = append([]protocol.Edge(nil), v.Edges...)
	v.Changes = append([]protocol.CharterChange(nil), v.Changes...)
	m.mp.labels = make(map[string]string)
	for i := range v.Nodes {
		n := &v.Nodes[i]
		if n.Ref == nil {
			continue
		}
		name := n.Name
		n.Name = graphRefKey(n.Ref)
		m.mp.labels[n.Name] = sanitize(name)
		// Retain the renderer's established Repo and Agent styles.
		if n.Ref.Kind == "repo" {
			n.Kind = "component"
		} else {
			n.Kind = "architect"
		}
	}
	for i := range v.Edges {
		e := &v.Edges[i]
		if e.FromRef != nil {
			e.From = graphRefKey(e.FromRef)
		}
		if e.ToRef != nil {
			e.To = graphRefKey(e.ToRef)
		}
	}
	for i := range v.Changes {
		c := &v.Changes[i]
		if c.Ref != nil {
			c.Node = graphRefKey(c.Ref)
		}
	}
	return v
}

func (m Model) graphName(key string) string {
	if name, ok := m.mp.labels[key]; ok {
		return name
	}
	return m.projectName(key)
}

// agentDisplayName is the readable name the Sessions tab and the team card use
// for an agent: its project display name, then the agent name, then the
// session's title or project. It never falls back to an ID.
func (m Model) agentDisplayName(a protocol.AgentView) string {
	name := firstNonEmpty(a.ProjectDisplayName, firstNonEmpty(a.AgentName, firstNonEmpty(a.Title, a.Profile.Name)))
	if name == "" {
		name = firstNonEmpty(a.Repo.Project, a.Profile.Project)
	}
	return sanitize(name)
}

// graphLeadLabel names a lead for display. A lead may be pinned to one
// session; that session's internal key is not a name, so it resolves through
// the snapshot to the agent's display name. A ref without a readable name
// returns "", so callers can say "no lead" instead of showing a key.
func (m Model) graphLeadLabel(ref *protocol.GraphRef) string {
	if ref == nil {
		return ""
	}
	key := graphRefKey(ref)
	if alias, ok := m.mp.aliases[key]; ok {
		key = alias
	}
	if name, ok := m.mp.labels[key]; ok && strings.TrimSpace(name) != "" {
		return name
	}
	switch ref.Kind {
	case "session":
		if a, ok := m.agentByID(ref.ID); ok {
			if name := m.agentDisplayName(a); name != "" {
				return name
			}
		}
		for _, n := range m.mp.full.Nodes {
			for _, s := range n.Sessions {
				if s.ID == ref.ID && strings.TrimSpace(s.Name) != "" {
					return sanitize(s.Name)
				}
			}
		}
	case "agent":
		for _, a := range m.snap.Agents {
			if a.AgentID == ref.ID {
				return m.agentDisplayName(a)
			}
		}
	case "repo":
		return m.projectName(ref.ID)
	}
	return ""
}

func graphRecordID(n protocol.Node) string {
	if n.Ref != nil {
		return n.Ref.ID
	}
	return n.Name
}
