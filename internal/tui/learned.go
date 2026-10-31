package tui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/niparis/agent-net/internal/protocol"
)

func (m Model) mapLearned(node string) []protocol.LearnedSuggestion {
	var out []protocol.LearnedSuggestion
	for _, v := range m.snap.Learned {
		if v.Node == node && v.Status == "applied" && v.By == "learned" && v.HistoryID != 0 {
			out = append(out, v)
		}
	}
	return out
}
func (m Model) mapUnseen(node string) int {
	n := 0
	for _, v := range m.mapLearned(node) {
		if v.Unseen {
			n++
		}
	}
	return n
}
func (m Model) mapLearnedLines(node string, width int) []string {
	changes := m.mapLearned(node)
	if len(changes) == 0 {
		return nil
	}
	selected := 0
	if m.mp.learnedFor == node {
		selected = m.mp.learnedIndex
	}
	selected = min(max(selected, 0), len(changes)-1)
	title := fmt.Sprintf("● %d learned", m.mapUnseen(node))
	if m.mapUnseen(node) == 0 {
		title = "Learned changes"
	}
	out := []string{section(title, width)}
	for i, v := range changes {
		marker := "  "
		if i == selected {
			marker = "▸ "
		}
		value := v.Phrase
		if v.Field == "edge" {
			value = v.EdgeKind + " -> " + v.Phrase
		} else if v.NewValue != "" {
			value = v.NewValue
		}
		out = append(out, clip(marker+sanitize(v.Field)+": "+sanitize(value)+"  "+m.mapAgo(v.DecidedAt), width))
		if len(v.Evidence) > 0 {
			for _, line := range wrap("why: "+sanitize(v.Evidence[0].Excerpt), max(width-4, 8)) {
				out = append(out, "    "+line)
			}
		}
	}
	return append(out, sDim.Render("  U next change  u undo"))
}
func (m Model) mapUndoLearned() tea.Cmd {
	c, ok := m.src.(inboxCaller)
	changes := m.mapLearned(m.mp.selected)
	if !ok || len(changes) == 0 {
		return nil
	}
	index := 0
	if m.mp.learnedFor == m.mp.selected {
		index = min(max(m.mp.learnedIndex, 0), len(changes)-1)
	}
	id := changes[index].HistoryID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		_, err := c.Call(ctx, "graph_undo", protocol.GraphUndoRequest{ID: id})
		if err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{text: "Learned change undone."}
	}
}
func (m Model) markMapViewed(node string) tea.Cmd {
	c, ok := m.src.(inboxCaller)
	if !ok || node == "" {
		return nil
	}
	var id, historyID int64
	for _, v := range m.mapLearned(node) {
		if v.Unseen && v.HistoryID > historyID {
			id, historyID = v.ID, v.HistoryID
		}
	}
	if id == 0 {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		_, err := c.Call(ctx, "learned_view", map[string]any{"suggestion_id": id, "node": node})
		if err != nil {
			return actionMsg{err: fmt.Errorf("mark learned changes viewed: %w", err)}
		}
		return nil
	}
}
