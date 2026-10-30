package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// "Needs you" (decision 0016): the sessions that wait for the owner, above the
// machines. A row is the same session as in the main table, so enter, e and m
// work on it. Oldest wait first, and hidden when there is nobody to answer.

// maxNeedsRows keeps the section from taking the whole screen. Further waits
// are counted in one line.
const maxNeedsRows = 5

// sNeeds is the warning colour of a wait.
var sNeeds = lipgloss.NewStyle().Foreground(roleWarning)

// needsRows is the sessions that wait for the owner, oldest wait first. It
// follows the same filter and offline switch as the table.
func (m Model) needsRows() []protocol.AgentView {
	var out []protocol.AgentView
	for _, e := range m.snap.Outcomes {
		text := e.Request + " | " + e.Outcome + ": " + e.Reason
		if e.Recommendation != "" {
			text += " | " + e.Recommendation
		}
		out = append(out, protocol.AgentView{SessionID: "outcome:" + e.ReplyID, Harness: "outcome", Online: true, Profile: protocol.Profile{Name: e.From + " -> " + e.To}, Waiting: &protocol.Waiting{Kind: protocol.WaitingInput, Text: text, Since: e.At / 1e9}})
	}
	for _, a := range m.snap.Agents {
		if protocol.IsOwner(a.Harness) || a.Waiting == nil {
			continue
		}
		if (!a.Online || a.Status == protocol.StatusDormant) && !m.showOffline {
			continue
		}
		// An ended worker is not a row, even when offline sessions are shown.
		if a.Worker != nil && !a.Online {
			continue
		}
		if m.activeOnly && m.idleLong(a) {
			continue
		}
		if !matches(a, m.filter) {
			continue
		}
		out = append(out, a)
	}
	for _, item := range m.inboxItems {
		if item.Kind != "approval" || item.Status != "open" {
			continue
		}
		var a protocol.AgentView
		for _, candidate := range m.snap.Agents {
			if candidate.SessionID == item.SessionID {
				a = candidate
				break
			}
		}
		if a.SessionID == "" {
			a.SessionID = item.SessionID
			a.Profile.Name = item.SessionID
			a.Harness = "agent"
		}
		a.Waiting = &protocol.Waiting{Kind: protocol.WaitingPermission, Text: item.Text, Since: item.CreatedAt.Unix()}
		out = append(out, a)
	}
	for _, held := range m.snap.Held {
		recipient := held.To
		for _, agent := range m.snap.Agents {
			if agent.SessionID == held.To && agent.Profile.Name != "" {
				recipient = agent.Profile.Name
				break
			}
		}
		first, _, _ := strings.Cut(held.Body, "\n")
		label := held.FromName
		if label == "" {
			label = held.From
		}
		text := label + " -> " + recipient + ": " + first
		out = append(out, protocol.AgentView{SessionID: held.ID, NodeID: m.snap.NodeID, Online: true, Harness: "request", Profile: protocol.Profile{Name: text}, Waiting: &protocol.Waiting{Kind: "held", Text: text, Since: held.CreatedAt.Unix()}})
	}
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := out[i].Waiting.Since, out[j].Waiting.Since
		if si != sj {
			if si == 0 {
				return false
			}
			if sj == 0 {
				return true
			}
			return si < sj
		}
		return out[i].SessionID < out[j].SessionID
	})
	return out
}

// needsHeight is the rows the section takes, heading and overflow included.
func (m Model) needsHeight() int {
	n := len(m.needsRows())
	if n == 0 {
		return 0
	}
	h := 1 + min(n, maxNeedsRows)
	if n > maxNeedsRows {
		h++
	}
	return h
}

// needsText is what the session asks, on one line.
func needsText(w *protocol.Waiting) string {
	text := sanitize(w.Text)
	switch w.Kind {
	case "held":
		return "a approve  A always  r reject  R never"
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

// needsLines draws the section. It is empty when nobody waits.
func (m Model) needsLines(w int) []string {
	rows := m.needsRows()
	if len(rows) == 0 {
		return nil
	}
	shown := rows
	more := 0
	if len(shown) > maxNeedsRows {
		more = len(shown) - maxNeedsRows
		shown = shown[:maxNeedsRows]
	}
	nameW, harnessW, hostW := 0, len("AGENT"), 0
	for _, a := range shown {
		nameW = max(nameW, ansi.StringWidth(m.needsName(a)))
		harnessW = max(harnessW, ansi.StringWidth(sanitize(a.Harness)))
		if a.NodeID != m.snap.NodeID {
			hostW = max(hostW, ansi.StringWidth(m.nodeName(a.NodeID, a.Hostname)))
		}
	}
	nameW = min(max(nameW, len("NAME")), 30)
	harnessW = min(harnessW, 8)
	hostW = min(hostW, 14)
	out := []string{section(fmt.Sprintf("Needs you (%d)", len(rows)), w)}
	for i, a := range shown {
		marker := "  ⚠ "
		if a.Waiting.Kind == "held" {
			if m.focus == focusSessions && a.SessionID == m.selected {
				marker = "> ⚠ "
			}
			line := marker + sanitize(a.Waiting.Text) + "  [a/A approve, r/R reject]"
			if marker[0] == '>' {
				out = append(out, sSel.Render(clip(line, w)))
			} else {
				out = append(out, sNeeds.Render(clip(line, w)))
			}
			continue
		}
		if m.focus == focusSessions && i < len(rows) && rows[i].SessionID == m.selected {
			marker = "> ⚠ "
		}
		line := marker + cell(m.needsName(a), nameW) + " " + cell(sanitize(a.Harness), harnessW)
		if hostW > 0 {
			line += " " + cell(m.needsHost(a), hostW)
		}
		line += " " + cellR(needsAge(m.now(), a.Waiting.Since), 4) + " "
		text := needsText(a.Waiting)
		room := w - ansi.StringWidth(line)
		if room > 0 {
			line += clip(text, room)
		}
		if marker[0] == '>' {
			out = append(out, sSel.Render(clip(stripStyles(line), w)))
		} else {
			out = append(out, clip(sNeeds.Render(marker)+stripStyles(line)[len(marker):], w))
		}
	}
	if more > 0 {
		out = append(out, sDim.Render(clip(fmt.Sprintf("   +%d more waiting (o shows offline)", more), w)))
	}
	return out
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

// needsHost is the machine, shown only for a session on another machine.
func (m Model) needsHost(a protocol.AgentView) string {
	if a.NodeID == m.snap.NodeID {
		return ""
	}
	return sanitize(m.nodeName(a.NodeID, a.Hostname))
}

// waitingStatus is the STATUS of a session in the table: a wait outranks idle,
// busy and offline (the section above shows it too).
func waitingStatus(a protocol.AgentView) string {
	if a.Waiting == nil {
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
	return statusShort(a.Status)
}

// statusCellStyle colours a session's STATUS cell.
func (m Model) statusCellStyle(a protocol.AgentView) lipgloss.Style {
	if a.Online && a.Waiting != nil {
		return sNeeds
	}
	return statusStyle(a.Status)
}

// needsColumns widens STATUS so "needs you" fits when somebody waits.
func (m Model) needsColumns(cols []column) []column {
	if len(m.needsRows()) == 0 {
		return cols
	}
	for i := range cols {
		if cols[i].name == "STATUS" {
			cols[i].min, cols[i].max, cols[i].width = 9, 9, 9
		}
	}
	return cols
}
