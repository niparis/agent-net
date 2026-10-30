package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/niparis/agent-net/internal/protocol"
)

type inboxCaller interface {
	Call(context.Context, string, any) (json.RawMessage, error)
}
type inboxAnswerMsg struct {
	id  string
	err error
}

func (m Model) inboxCount() int {
	n := 0
	for _, item := range m.inboxItems {
		if item.Status == "open" {
			n++
		}
	}
	return n
}
func (m Model) orderedInbox() []protocol.InboxItem {
	items := append([]protocol.InboxItem(nil), m.inboxItems...)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items
}
func (m Model) inboxView(w int) []string {
	out := []string{section("Inbox", w)}
	items := m.orderedInbox()
	if len(items) == 0 {
		return append(out, "  No inbox items")
	}
	if m.inboxIndex >= len(items) {
		return out
	}
	item := items[m.inboxIndex]
	if m.inboxDetail {
		out = append(out, "Kind: "+sanitize(item.Kind), "Status: "+sanitize(item.Status), "Session: "+sanitize(item.SessionID), "Node: "+sanitize(item.Node), "Text: "+sanitize(item.Text), "Recommendation: "+sanitize(item.Recommendation), "Message: "+sanitize(item.MessageID), "Conversation: "+sanitize(item.ConversationID), "Answer: "+sanitize(item.Answer))
	} else {
		kind := ""
		for i, it := range items {
			if it.Kind != kind {
				kind = it.Kind
				out = append(out, kind)
			}
			mark := "  "
			if i == m.inboxIndex {
				mark = "> "
			}
			out = append(out, clip(mark+sanitize(it.Text)+" ("+sanitize(it.Status)+")", w))
		}
	}
	if m.inboxError != "" {
		out = append(out, "Error: "+m.inboxError)
	}
	return out
}
func (m Model) inboxKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := m.orderedInbox()
	if m.inboxEditing {
		switch k.Type {
		case tea.KeyEsc:
			m.inboxEditing = false
			return m, nil
		case tea.KeyEnter:
			answer := strings.TrimSpace(m.inboxInput.Value())
			if answer != "" {
				m.inboxEditing = false
				return m, m.inboxAnswer(items[m.inboxIndex].ID, answer)
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.inboxInput, cmd = m.inboxInput.Update(k)
		return m, cmd
	}
	switch k.String() {
	case "esc", "backspace":
		if m.inboxDetail {
			m.inboxDetail = false
		} else {
			m.inboxOn = false
		}
	case "up", "k":
		m.inboxIndex = max(0, m.inboxIndex-1)
	case "down", "j":
		m.inboxIndex = min(max(0, len(items)-1), m.inboxIndex+1)
	case "enter":
		if len(items) > 0 {
			m.inboxDetail = true
		}
	case "a", "r":
		if len(items) > 0 && items[m.inboxIndex].Status == "open" {
			answer := "approve"
			if k.String() == "r" {
				answer = "refuse"
			}
			return m, m.inboxAnswer(items[m.inboxIndex].ID, answer)
		}
	case "t":
		if len(items) > 0 && items[m.inboxIndex].Status == "open" {
			m.inboxInput = textinput.New()
			m.inboxInput.CharLimit = protocol.MaxBodyBytes
			m.inboxInput.Focus()
			m.inboxEditing = true
		}
	}
	return m, nil
}
func (m Model) inboxAnswer(id, answer string) tea.Cmd {
	c, ok := m.src.(inboxCaller)
	return func() tea.Msg {
		if !ok {
			return inboxAnswerMsg{id, fmt.Errorf("inbox unavailable")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		_, err := c.Call(ctx, "inbox_answer", map[string]string{"id": id, "answer": answer})
		return inboxAnswerMsg{id, err}
	}
}
