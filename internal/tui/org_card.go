package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/niparis/agent-net/internal/authority"
	"github.com/niparis/agent-net/internal/protocol"
)

// orgLabelW is the label column of the org rows on an Agent card. It is the
// width the place block uses on a wide card.
const orgLabelW = 10

// orgCardLines is the lead session and the delegated authority of an Agent
// (decision 0160 and decision 0166). The authority is the text a session reads
// in its instructions, so the card cannot say something else.
func (m Model) orgCardLines(node protocol.Node, w int) []string {
	if node.Ref == nil || node.Ref.Kind != "agent" {
		return nil
	}
	lead, style := m.leadSessionText(node.LeadSession)
	out := orgRow("agent lead", lead, style, w)
	authorityText, authStyle := authorityLine(node.Scopes)
	return append(out, orgRow("authority", authorityText, authStyle, w)...)
}

// leadSessionText says whether the Agent's lead session is online, offline
// since a time, or gone. Gone means the snapshot does not list the session.
func (m Model) leadSessionText(lead *protocol.GraphSession) (string, lipgloss.Style) {
	if lead == nil {
		return "no lead session", sDim
	}
	name := firstNonEmpty(lead.Name, lead.ID)
	a, ok := m.sessionByID(lead.ID)
	switch {
	case !ok:
		return name + " · gone", sBad
	case !a.Online || sessionEnded(a):
		if a.LastSeen == 0 {
			return name + " · offline", sDim
		}
		return name + " · offline since " + m.displayTime(a.LastSeen).Format("15:04"), sDim
	}
	return name + " · online", lipgloss.NewStyle()
}

// authorityWords is the Delegated authority sentence without its prefix and
// final stop, so a screen reads the same words as the session. Empty when the
// Agent holds no scope.
func authorityWords(scopes []protocol.Scope) string {
	text := authority.Text(scopes)
	if text == "" {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(text, "Delegated authority: "), ".")
}

// authorityLine is the authority row of the Agent card.
func authorityLine(scopes []protocol.Scope) (string, lipgloss.Style) {
	if text := authorityWords(scopes); text != "" {
		return text, lipgloss.NewStyle()
	}
	return "no delegated authority", sDim
}

// orgRow is one labelled row, wrapped under the label. The style applies to
// every wrapped line.
func orgRow(label, text string, style lipgloss.Style, w int) []string {
	body := wrap(text, max(w-orgLabelW-1, 8))
	if len(body) == 0 {
		body = []string{""}
	}
	out := []string{sDim.Render(cell(label, orgLabelW)) + " " + style.Render(body[0])}
	for _, line := range body[1:] {
		out = append(out, strings.Repeat(" ", orgLabelW+1)+style.Render(line))
	}
	return out
}

// sessionByID finds a session in the snapshot, ended ones included.
func (m Model) sessionByID(id string) (protocol.AgentView, bool) {
	for _, a := range m.snap.Agents {
		if a.SessionID == id {
			return a, true
		}
	}
	return protocol.AgentView{}, false
}
