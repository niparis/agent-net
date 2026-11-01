package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

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
			out = append(out, mapWhyLines(sanitize(v.Evidence[0].Excerpt), width)...)
		}
	}
	return out
}

// mapWhyLines wraps the evidence of a learned change over at most two lines,
// cut at a word with an ellipsis, so it never runs off the card.
func mapWhyLines(why string, w int) []string {
	const indent = "    "
	const label = "why: "
	why = strings.TrimSpace(why)
	if why == "" {
		return nil
	}
	room := max(w-ansi.StringWidth(indent+label), 8)
	lines := wrap(why, room)
	if len(lines) > 2 {
		lines = lines[:2]
		words := strings.Fields(lines[1])
		for len(words) > 1 && ansi.StringWidth(strings.Join(words, " "))+2 > room {
			words = words[:len(words)-1]
		}
		last := strings.Join(words, " ")
		if ansi.StringWidth(last)+2 > room {
			last = ansi.Truncate(last, max(room-2, 1), "")
		}
		lines[1] = last + " …"
	}
	out := []string{indent + sDim.Render(label) + lines[0]}
	for _, l := range lines[1:] {
		out = append(out, strings.Repeat(" ", ansi.StringWidth(indent+label))+l)
	}
	return out
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
