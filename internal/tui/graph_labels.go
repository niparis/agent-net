package tui

import "github.com/niparis/agent-net/internal/protocol"

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

func graphRecordID(n protocol.Node) string {
	if n.Ref != nil {
		return n.Ref.ID
	}
	return n.Name
}
