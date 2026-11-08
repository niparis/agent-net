package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/protocol"
)

// Charter drafts (decision 0088) are suggestions a session drafted from
// AGENTS.md. They sit in the owner's learned list with a negative ID. The card
// of the node they belong to shows them with accept and reject.

// draftBelongs tells whether a draft is written at this node.
func draftBelongs(v protocol.LearnedSuggestion, node protocol.Node) bool {
	if node.Ref != nil {
		return v.Level == node.Ref.Kind && v.Scope == node.Ref.ID
	}
	return (v.Level == "repo" || v.Level == "agent") && v.Scope == node.Name
}

func (m Model) mapDrafts(node protocol.Node) []protocol.LearnedSuggestion {
	var out []protocol.LearnedSuggestion
	for _, v := range m.snap.Learned {
		if v.Origin == "draft" && v.Status == "pending" && draftBelongs(v, node) {
			out = append(out, v)
		}
	}
	return out
}

func (m Model) mapDraftIndex(node protocol.Node, n int) int {
	if m.mp.draftFor != node.Name {
		return 0
	}
	return min(max(m.mp.draftIndex, 0), n-1)
}

func (m Model) mapDraftLines(node protocol.Node, w int) []string {
	drafts := m.mapDrafts(node)
	if len(drafts) == 0 {
		return nil
	}
	selected := m.mapDraftIndex(node, len(drafts))
	out := []string{section("Drafted from "+sanitize(drafts[0].By), w)}
	for i, v := range drafts {
		marker := "  "
		if i == selected {
			marker = "▸ "
		}
		out = append(out, clip(marker+sanitize(v.Field), w))
		for _, line := range wrap(sanitize(v.NewValue), max(w-4, 8)) {
			out = append(out, "    "+line)
		}
	}
	hint := "y accept  x reject"
	if len(drafts) > 1 {
		hint += "  , . choose"
	}
	return append(out, sDim.Render("  "+hint))
}

// mapDraftDecide answers the selected draft through the owner's learned answer.
func (m Model) mapDraftDecide(action string) tea.Cmd {
	c, ok := m.src.(inboxCaller)
	node, found := m.mapNode(m.mp.selected)
	if !ok || !found {
		return nil
	}
	drafts := m.mapDrafts(node)
	if len(drafts) == 0 {
		return nil
	}
	id := drafts[m.mapDraftIndex(node, len(drafts))].ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		if _, err := c.Call(ctx, "learned_answer", map[string]any{"suggestion_id": id, "action": action}); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{text: fmt.Sprintf("Draft %sed.", action)}
	}
}
