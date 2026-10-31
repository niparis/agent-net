package tui

import (
	"context"
	"sort"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/niparis/agent-net/internal/protocol"
)

func (m Model) hiddenItems() protocol.HiddenItems {
	items := protocol.HiddenItems{}
	for id := range m.hiddenSessions {
		items.Sessions = append(items.Sessions, id)
	}
	for name := range m.hiddenNodes {
		items.Nodes = append(items.Nodes, name)
	}
	sort.Strings(items.Sessions)
	sort.Strings(items.Nodes)
	return items
}

func (m *Model) setHiddenItems(items protocol.HiddenItems) {
	m.hiddenSessions = make(map[string]bool, len(items.Sessions))
	for _, id := range items.Sessions {
		m.hiddenSessions[id] = true
	}
	m.hiddenNodes = make(map[string]bool, len(items.Nodes))
	for _, name := range items.Nodes {
		m.hiddenNodes[name] = true
	}
}

func (m Model) hiddenItemsCmd(items protocol.HiddenItems) tea.Cmd {
	src := m.src
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		updated, err := src.SetHiddenItems(ctx, items)
		return hiddenItemsMsg{items: updated, err: err}
	}
}

func (m Model) toggleHiddenSession() (tea.Model, tea.Cmd) {
	if m.selected == "" {
		return m, nil
	}
	a, ok := m.agentByID(m.selected)
	if !ok || protocol.IsOwner(a.Harness) {
		return m, nil
	}
	items := m.hiddenItems()
	if m.hiddenSessions[a.SessionID] {
		items.Sessions = removeString(items.Sessions, a.SessionID)
	} else {
		items.Sessions = append(items.Sessions, a.SessionID)
	}
	return m, m.hiddenItemsCmd(items)
}

func (m Model) toggleHiddenNode() (tea.Model, tea.Cmd) {
	if m.mp.selected == "" {
		return m, nil
	}
	if _, ok := m.mapNode(m.mp.selected); !ok {
		return m, nil
	}
	items := m.hiddenItems()
	if m.hiddenNodes[m.mp.selected] {
		items.Nodes = removeString(items.Nodes, m.mp.selected)
	} else {
		items.Nodes = append(items.Nodes, m.mp.selected)
	}
	return m, m.hiddenItemsCmd(items)
}

func removeString(items []string, value string) []string {
	out := items[:0]
	for _, item := range items {
		if item != value {
			out = append(out, item)
		}
	}
	return out
}

func (m Model) mapVisibleNodes() []protocol.Node {
	var out []protocol.Node
	for _, node := range m.mp.nodes {
		if m.showHiddenNodes || !m.hiddenNodes[node.Name] {
			out = append(out, node)
		}
	}
	return out
}

func (m Model) mapVisibleEdges() []protocol.Edge {
	var out []protocol.Edge
	for _, edge := range m.mp.edges {
		if m.showHiddenNodes || !m.hiddenNodes[edge.From] && !m.hiddenNodes[edge.To] {
			out = append(out, edge)
		}
	}
	return out
}
