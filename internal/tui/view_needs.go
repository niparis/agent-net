package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/niparis/agent-net/internal/protocol"
)

// Waiting sessions and their names, shared by the Inbox tab and the sessions
// table. The Inbox replaced the old "Needs you" section and inbox overlay.

const appNoticePrefix = "app-notice:"

func isAppApprovalNotice(item protocol.InboxItem) bool {
	return item.Kind == "check" && item.Node == "apps" && (strings.HasPrefix(item.Text, "Connected: ") || strings.HasPrefix(item.Text, "New app approved: "))
}

func appApprovalNoticeText(text string) string {
	if strings.HasPrefix(text, "New app approved: ") {
		text = "Connected: " + strings.TrimPrefix(text, "New app approved: ") + ". If this was not you, revoke it in Settings."
	}
	return text
}

// sNeeds is the warning colour of a wait.
var sNeeds = lipgloss.NewStyle().Foreground(roleWarning)

// learnedQuestion describes the change in the words the owner can recognize.
func learnedQuestion(v protocol.LearnedSuggestion) string {
	phrase := v.Phrase
	if v.Origin == "owner" && v.RawPhrase != "" {
		phrase = v.RawPhrase
	}
	var question string
	if v.Field == "edge" {
		verb := "add"
		if v.Kind == "remove" {
			verb = "remove"
		}
		question = fmt.Sprintf("%s: %s a %s edge %s → %s?", v.Node, verb, v.EdgeKind, v.Node, v.Phrase)
	} else {
		switch v.Kind {
		case "remove":
			question = fmt.Sprintf("%s: remove «%s» from %s?", v.Node, phrase, v.Field)
		case "narrow":
			question = fmt.Sprintf("%s: change %s to «%s»?", v.Node, v.Field, phrase)
		default:
			question = fmt.Sprintf("%s: add «%s» to %s?", v.Node, phrase, v.Field)
		}
	}
	if len(v.Evidence) > 0 && v.Evidence[0].Excerpt != "" {
		question += " because " + v.Evidence[0].Excerpt
	}
	return sanitize(question)
}

// needsText is what the session asks, on one line.
func needsText(w *protocol.Waiting) string {
	text := sanitize(w.Text)
	switch w.Kind {
	case "held":
		return "a approve  A always  r reject  R never"
	case "learned":
		return text
	case protocol.WaitingPermission:
		if text == "" {
			return "permission"
		}
		return "permission: " + text
	case protocol.WaitingQuestion:
		if text == "" {
			return "question"
		}
		return `"` + text + `"`
	}
	if text == "" {
		return "input"
	}
	return `"` + text + `"`
}

// needsAge says how long the wait has been there, in one short cell.
func needsAge(now time.Time, since int64) string {
	if since <= 0 {
		return "now"
	}
	d := now.Sub(time.Unix(since, 0))
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// needsName is the name of the waiting session. A worker without a task shows
// its harness and short ID, as the main table does.
func (m Model) needsName(a protocol.AgentView) string {
	if name := sanitize(a.Profile.Name); name != "" {
		return name
	}
	if a.Worker != nil {
		return "worker " + sanitize(a.Harness) + " " + shortID(a.SessionID)
	}
	return shortID(a.SessionID)
}

// waitingStatus is the STATUS of a session in the table: a wait outranks idle,
// busy and offline (the Inbox shows it too).
func waitingStatus(a protocol.AgentView) string {
	if a.Waiting == nil || a.Worker != nil {
		return ""
	}
	return "needs you"
}

// statusCell is the STATUS cell of the table; statusStyle has its colour.
func (m Model) statusCell(a protocol.AgentView) string {
	if !a.Online {
		return "off"
	}
	if s := waitingStatus(a); s != "" {
		return s
	}
	if a.Status == "idle" && len(a.Pending) > 0 {
		for _, p := range a.Pending {
			if !p.Outgoing {
				return "owes"
			}
		}
		return "wait"
	}
	return statusShort(a.Status)
}

// statusCellStyle colours a session's STATUS cell.
func (m Model) statusCellStyle(a protocol.AgentView) lipgloss.Style {
	if a.Online && a.Waiting != nil {
		return sNeeds
	}
	return statusStyle(a.Status)
}
