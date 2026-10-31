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

type messageConversation struct {
	id               string
	items            []int
	at               time.Time
	cto              bool
	event            bool
	worker           bool
	workerSession    string
	workerOwner      string
	ownerInstruction bool
}

func (m Model) messageConversations() []messageConversation {
	groups := map[string]*messageConversation{}
	parentIDs := map[string]string{}
	for i, it := range m.feed.items {
		if !it.isMessage() {
			continue
		}
		workerSession, workerOwner, worker := m.workerPair(it)
		id := ""
		if worker {
			id = "worker:" + workerSession
		} else {
			id = it.ConversationID
			if id == "" {
				id = parentIDs[it.Parent]
			}
			if id == "" {
				id = messagePairID(it.From, it.To)
			}
			if id == "" {
				id = it.MsgID
			}
		}
		parentIDs[it.MsgID] = id
		g := groups[id]
		if g == nil {
			g = &messageConversation{id: id, worker: worker, workerSession: workerSession, workerOwner: workerOwner}
			groups[id] = g
		}
		g.items = append(g.items, i)
		g.ownerInstruction = g.ownerInstruction || it.OwnerInstruction != ""
		activity := it.At
		if it.Updated.After(activity) {
			activity = it.Updated
		}
		if activity.After(g.at) {
			g.at = activity
		}
		g.cto = g.cto || it.FromCTO
	}
	out := make([]messageConversation, 0, len(groups))
	for _, g := range groups {
		if g.worker && m.hideWorkerThreads {
			continue
		}
		if firstNonEmpty(m.uiFilter, m.filter) != "" {
			var b strings.Builder
			for _, i := range g.items {
				it := m.feed.items[i]
				fmt.Fprintf(&b, "%s %s %s %s %s ", m.conversationName(it.From), m.conversationName(it.To), it.Snippet, it.Type, it.Body)
			}
			if !strings.Contains(strings.ToLower(b.String()), strings.ToLower(firstNonEmpty(m.uiFilter, m.filter))) {
				continue
			}
		}
		out = append(out, *g)
	}
	if m.messageShowEvents {
		for i, it := range m.feed.items {
			if it.isMessage() {
				continue
			}
			if m.events == eventsNone && (strings.HasPrefix(it.Kind, "worker.") || strings.HasPrefix(it.Kind, "session.")) {
				continue
			}
			if firstNonEmpty(m.uiFilter, m.filter) != "" && !strings.Contains(strings.ToLower(m.infoText(it)), strings.ToLower(firstNonEmpty(m.uiFilter, m.filter))) {
				continue
			}
			out = append(out, messageConversation{id: fmt.Sprintf("event-%d", i), items: []int{i}, at: it.At, event: true})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		rank := func(c messageConversation) int {
			switch {
			case c.event:
				return 3
			case c.worker:
				return 2
			case c.ownerInstruction:
				return 0
			default:
				return 1
			}
		}
		if rank(out[i]) != rank(out[j]) {
			return rank(out[i]) < rank(out[j])
		}
		if out[i].at.Equal(out[j].at) {
			return out[i].items[len(out[i].items)-1] > out[j].items[len(out[j].items)-1]
		}
		return out[i].at.After(out[j].at)
	})
	return out
}

func (m Model) workerPair(it *feedItem) (workerSession, workerOwner string, ok bool) {
	for _, pair := range [][2]string{{it.From, it.To}, {it.To, it.From}} {
		a, exists := m.agentByID(pair[0])
		if exists && a.Worker != nil && a.Worker.Owner == pair[1] {
			return pair[0], pair[1], true
		}
	}
	return "", "", false
}

func (m Model) conversationIndex(rows []messageConversation) int {
	for n, r := range rows {
		for _, i := range r.items {
			if i == m.feedCur {
				return n
			}
		}
	}
	return 0
}

func messagePairID(from, to string) string {
	if from == "" || to == "" {
		return ""
	}
	if from > to {
		from, to = to, from
	}
	return "pair:" + from + "\x00" + to
}

func (m Model) messageConversationLine(c messageConversation, w int, selected bool) string {
	if c.worker {
		return m.workerConversationLine(c, w, selected)
	}
	if c.event {
		it := m.feed.items[c.items[0]]
		line := sDim.Render(clip("  "+m.clock(it.At)+" "+m.infoText(it), w))
		if selected {
			line = selectedRow(line)
		}
		return line
	}
	first := m.feed.items[c.items[0]]
	latest := m.feed.items[c.items[len(c.items)-1]]
	from := m.conversationName(first.From)
	if first.FromCTO {
		from = "CTO"
	}
	to := m.conversationName(first.To)
	names := from + " ↔ " + to
	topic := latest.Snippet
	if topic == "" {
		topic = m.missingMessageBody(latest)
	}
	age := m.now().Sub(c.at)
	if age < 0 {
		age = 0
	}
	age = age.Truncate(time.Minute)
	ageText := strings.TrimSuffix(age.String(), "0s")
	if age < time.Minute {
		ageText = "now"
	} else {
		ageText += " ago"
	}
	lead := "  "
	if selected {
		lead = lipgloss.NewStyle().Foreground(roleAccent).Render("▸") + " "
	}
	content := names + " · " + topic
	messageWord := "messages"
	if len(c.items) == 1 {
		messageWord = "message"
	}
	suffix := fmt.Sprintf("%d %s · %s", len(c.items), messageWord, ageText)
	marker := ""
	if c.ownerInstruction {
		marker = lipgloss.NewStyle().Foreground(roleAccent).Render("you→ ")
	}
	style := lipgloss.NewStyle().Foreground(roleText)
	if c.cto {
		style = lipgloss.NewStyle().Foreground(roleCTO)
	} else if latest.State == protocol.StateFailed || latest.State == protocol.StateExpired {
		style = lipgloss.NewStyle().Foreground(roleError)
	} else if latest.Type == protocol.TypeRequest && latest.State != protocol.StateReplied {
		style = lipgloss.NewStyle().Foreground(roleWarning)
	}
	room := max(w-ansi.StringWidth(lead)-ansi.StringWidth(marker)-ansi.StringWidth(suffix)-1, 0)
	line := lead + marker + padStyled(style.Render(clip(content, room)), room) + " " + sDim.Render(suffix)
	if selected {
		line = selectedRow(line)
	}
	return clip(line, w)
}

func (m Model) workerConversationLine(c messageConversation, w int, selected bool) string {
	lead := "  "
	if selected {
		lead = lipgloss.NewStyle().Foreground(roleAccent).Render("▸") + " "
	}
	status := m.workerThreadStatus(c)
	statusStyle := lipgloss.NewStyle().Foreground(roleText)
	if status == "done" {
		statusStyle = sBusy
	} else if status == "failed" || status == "blocked" || status == "timed out" {
		statusStyle = sBad
	} else {
		latest := m.feed.items[c.items[len(c.items)-1]]
		if latest.Type == protocol.TypeRequest && latest.State != protocol.StateReplied && latest.State != protocol.StateFailed && latest.State != protocol.StateExpired {
			statusStyle = lipgloss.NewStyle().Foreground(roleWarning)
		}
	}
	content := m.conversationName(c.workerOwner) + " → " + m.conversationName(c.workerSession)
	messageWord := "messages"
	if len(c.items) == 1 {
		messageWord = "message"
	}
	suffix := fmt.Sprintf("%d %s · %s", len(c.items), messageWord, status)
	room := max(w-ansi.StringWidth(lead)-ansi.StringWidth(suffix)-1, 0)
	line := lead + padStyled(clip(content, room), room) + " " + sDim.Render(fmt.Sprintf("%d %s · ", len(c.items), messageWord)) + statusStyle.Render(status)
	if selected {
		line = selectedRow(line)
	}
	return clip(line, w)
}

func (m Model) workerThreadStatus(c messageConversation) string {
	for i := len(c.items) - 1; i >= 0; i-- {
		if status, ok := workerNoticeStatus(m.feed.items[c.items[i]].Body); ok {
			return status
		}
	}
	if a, ok := m.agentByID(c.workerSession); ok {
		if a.Online {
			return "running"
		}
		return "ended"
	}
	return "unknown"
}

func workerNoticeStatus(body string) (string, bool) {
	if !strings.HasPrefix(strings.TrimSpace(body), "[agent-net worker notice]") {
		return "", false
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "status: ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "status: ")), true
		}
	}
	return "stopped", true
}

func (m Model) missingMessageBody(it *feedItem) string {
	if it.BodyError != "" {
		return "body unavailable: " + it.BodyError
	}
	switch it.Snip {
	case snipLoading:
		return "loading body"
	case snipFailed:
		return "body unavailable"
	case snipDone:
		return "empty message"
	default:
		if m.localEnd(it) || m.snap.OwnerSession != "" {
			return "loading body"
		}
		return "body unavailable here"
	}
}

func workerNoticeLine(body string) (string, bool) {
	if !strings.HasPrefix(strings.TrimSpace(body), "[agent-net worker notice]") {
		return "", false
	}
	status, run, files := "", "", ""
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "status: ") {
			status = strings.TrimPrefix(line, "status: ")
		}
		if strings.HasPrefix(line, "run time: ") {
			run = strings.TrimPrefix(line, "run time: ")
		}
		if strings.Contains(line, " files changed") || strings.Contains(line, " file changed") {
			files = strings.Fields(line)[0] + " files"
		}
	}
	if status == "" {
		status = "stopped"
	}
	mark := "✓"
	if status != "done" {
		mark = "✗"
	}
	out := mark + " " + status
	if run != "" {
		out += " · " + run
	}
	if files != "" {
		out += " · " + files
	}
	return out, true
}
func (m Model) conversationChat(c messageConversation, w int) []string {
	var out []string
	if c.event {
		return []string{sDim.Render("System event")}
	}
	for _, i := range c.items {
		it := m.feed.items[i]
		sender := m.conversationName(it.From)
		if it.FromCTO {
			sender = "CTO"
		}
		marker := ""
		if it.OwnerInstruction != "" {
			marker = lipgloss.NewStyle().Foreground(roleAccent).Render("you→ ")
		}
		header := sDim.Render(m.clock(it.At)+" ") + marker + sDim.Render(sender)
		if i == m.feedCur {
			header = lipgloss.NewStyle().Foreground(roleAccent).Render("▸ ") + header
		} else {
			header = "  " + header
		}
		out = append(out, header)
		if it.OwnerInstruction != "" {
			for _, line := range wrap("Your instruction: "+it.OwnerInstruction, w) {
				out = append(out, sDim.Render(line))
			}
		}
		if text, ok := workerNoticeLine(it.Body); ok {
			if strings.Contains(text, "✓ done") {
				out = append(out, sBusy.Render(clip(text, w)))
			} else {
				out = append(out, sBad.Render(clip(text, w)))
			}
			continue
		}
		if it.Body == "" {
			out = append(out, sDim.Render(clip(m.missingMessageBody(it), w)))
			continue
		}
		lines := strings.Split(it.Body, "\n")
		for j := 0; j < len(lines) && j < 3; j++ {
			out = append(out, clip(lines[j], w))
		}
		if len(lines) > 3 {
			out = append(out, sDim.Render("…"))
		}
	}
	return out
}

func (m Model) selectedConversationItem() *feedItem {
	rows := m.messageConversations()
	if len(rows) == 0 {
		return nil
	}
	row := rows[m.conversationIndex(rows)]
	for _, i := range row.items {
		if i == m.feedCur {
			return m.feedItemAt(i)
		}
	}
	return m.feedItemAt(row.items[0])
}
func (m Model) systemEventCount() int {
	n := 0
	for _, it := range m.feed.items {
		if !it.isMessage() {
			n++
		}
	}
	return n
}

func (m Model) conversationName(id string) string {
	name := m.feedName(id)
	if a, ok := m.agentByID(id); ok && a.Worker != nil {
		if i := strings.LastIndex(name, "worker "); i >= 0 {
			return name[i:]
		}
	}
	return name
}
