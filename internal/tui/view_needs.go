package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/niparis/agent-net/internal/protocol"
)

// Waiting sessions and their names, used by the sessions table.

func appApprovalNoticeText(text string) string {
	if strings.HasPrefix(text, "New app approved: ") {
		text = "Connected: " + strings.TrimPrefix(text, "New app approved: ") + ". If this was not you, revoke it in Settings."
	}
	return text
}

// sNeeds is the warning colour of a wait.
var sNeeds = lipgloss.NewStyle().Foreground(roleWarning)

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

// anySessionWaiting tells whether a table row reports a wait.
func (m Model) anySessionWaiting() bool {
	for _, a := range m.snap.Agents {
		if a.Waiting != nil && a.Worker == nil && !protocol.IsOwner(a.Harness) {
			return true
		}
	}
	return false
}

// waitingStatus is the STATUS of a session in the table: a wait outranks idle,
// busy and offline.
func waitingStatus(a protocol.AgentView) string {
	if a.Waiting == nil || a.Worker != nil {
		return ""
	}
	return "needs you"
}

// statusCell is the STATUS cell of the table; statusStyle has its colour.
func (m Model) statusCell(a protocol.AgentView) string {
	// A restarted conversation keeps one live record; the old one says only
	// that something replaced it.
	if a.ReplacedBy != "" {
		return "replaced"
	}
	if a.Worker == nil && a.StalledSince > 0 {
		return "stalled " + needsAge(m.now(), a.StalledSince)
	}
	if !a.Online {
		if a.LastSeen <= 0 {
			return "ended"
		}
		age := needsAge(m.now(), a.LastSeen)
		if age == "now" {
			return "ended just now"
		}
		return "ended " + age + " ago"
	}
	if a.Limited(m.now()) {
		return "limited"
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
	if a.Status == protocol.StatusDormant {
		return "dormant"
	}
	return statusShort(a.Status)
}
