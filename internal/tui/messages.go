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
	task             string
	ownerInstruction bool
	// The stored side of a conversation from the gateway. A row may have
	// both: convID names the gateway conversation and stored says the count,
	// last line and state come from the store rather than only the feed.
	convID    string
	from      string
	to        string
	fromName  string
	toName    string
	count     int
	lastLine  string
	state     string
	typ       string
	lastMsgID string
	stored    bool
}

// feedConversations groups the live feed into conversations. The Messages tab
// uses it as the fallback when the gateway cannot list stored conversations,
// and rebuildConversations merges it into the stored list.
func (m Model) feedConversations() []messageConversation {
	groups := map[string]*messageConversation{}
	parentIDs := map[string]string{}
	workers := m.feedWorkers()
	for i, it := range m.feed.items {
		if !it.isMessage() {
			continue
		}
		workerSession, workerOwner, worker := m.workerPair(it, workers)
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
		if it.ConversationID != "" {
			g.convID = it.ConversationID
		}
		if g.from == "" {
			g.from, g.to = it.From, it.To
		}
		g.items = append(g.items, i)
		g.ownerInstruction = g.ownerInstruction || it.OwnerInstruction != ""
		if g.task == "" && strings.HasPrefix(strings.TrimSpace(it.Body), "[agent-net worker brief]") {
			g.task = sanitize(protocol.WorkerTaskSummary(it.Body))
		}
		if !workerNoticeLifecycle(it.Body) {
			g.count++
			activity := it.At
			if it.Updated.After(activity) {
				activity = it.Updated
			}
			if activity.After(g.at) {
				g.at = activity
			}
		}
		g.cto = g.cto || it.FromCTO
	}
	out := make([]messageConversation, 0, len(groups))
	for _, g := range groups {
		if g.count == 0 {
			// Only lifecycle notices: they do not make a row.
			continue
		}
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

// feedWorkers maps a worker session to its owner. The snapshot has the live
// workers. The gateway deletes a worker's session record a few hours after the
// start, but the task brief its caller sent still says which session is the
// worker, so the feed can group its later notices too.
func (m Model) feedWorkers() map[string]string {
	workers := map[string]string{}
	for _, a := range m.snap.Agents {
		if a.Worker != nil {
			workers[a.SessionID] = a.Worker.Owner
		}
	}
	for _, it := range m.feed.items {
		if !it.isMessage() {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(it.Body), "[agent-net worker brief]") && it.To != "" {
			workers[it.To] = it.From
		}
	}
	return workers
}

func (m Model) workerPair(it *feedItem, workers map[string]string) (workerSession, workerOwner string, ok bool) {
	for _, pair := range [][2]string{{it.From, it.To}, {it.To, it.From}} {
		if owner, exists := workers[pair[0]]; exists && owner == pair[1] {
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
	from, to := c.from, c.to
	fromName, toName := c.fromName, c.toName
	topic, state, typ := c.lastLine, c.state, c.typ
	cto, ownerInstruction := c.cto, c.ownerInstruction
	if len(c.items) > 0 {
		first := m.feed.items[c.items[0]]
		latest := m.feed.items[c.items[len(c.items)-1]]
		if from == "" {
			from = first.From
		}
		if to == "" {
			to = first.To
		}
		if topic == "" {
			topic = latest.Snippet
			if topic == "" {
				topic = m.missingMessageBody(latest)
			}
		}
		if state == "" {
			state = latest.State
		}
		if typ == "" {
			typ = latest.Type
		}
		cto = cto || latest.FromCTO
		ownerInstruction = ownerInstruction || latest.OwnerInstruction != ""
	}
	fromName = m.partyName(from, fromName)
	if cto {
		fromName = m.ctoTitle()
	}
	toName = m.partyName(to, toName)
	names := fromName + " ↔ " + toName
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
	count := c.count
	if count == 0 {
		count = len(c.items)
	}
	messageWord := "messages"
	if count == 1 {
		messageWord = "message"
	}
	suffix := fmt.Sprintf("%d %s · %s", count, messageWord, ageText)
	marker := ""
	if ownerInstruction {
		marker = lipgloss.NewStyle().Foreground(roleAccent).Render("you→ ")
	}
	style := lipgloss.NewStyle().Foreground(roleText)
	if cto {
		style = lipgloss.NewStyle().Foreground(roleCTO)
	} else if state == protocol.StateFailed || state == protocol.StateExpired {
		style = lipgloss.NewStyle().Foreground(roleError)
	} else if typ == protocol.TypeRequest && state != protocol.StateReplied {
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
	} else if len(c.items) > 0 && c.typ == protocol.TypeRequest && c.state != protocol.StateReplied && c.state != protocol.StateFailed && c.state != protocol.StateExpired {
		statusStyle = lipgloss.NewStyle().Foreground(roleWarning)
	}
	caller := m.partyName(c.workerOwner, c.fromName)
	content := caller + " → worker"
	if task := firstNonEmpty(c.task, m.workerTaskLine(c.workerSession)); task != "" {
		content += ": " + task
	} else if name := m.conversationName(c.workerSession); name != "ended session" {
		content = caller + " → " + name
	}
	count := c.count
	if count == 0 {
		count = len(c.items)
	}
	messageWord := "messages"
	if count == 1 {
		messageWord = "message"
	}
	room := max(w-ansi.StringWidth(lead)-ansi.StringWidth(fmt.Sprintf("%d %s · ", count, messageWord))-ansi.StringWidth(status)-1, 0)
	line := lead + padStyled(clip(content, room), room) + " " + sDim.Render(fmt.Sprintf("%d %s · ", count, messageWord)) + statusStyle.Render(status)
	if selected {
		line = selectedRow(line)
	}
	return clip(line, w)
}

// workerTaskLine is the first line of the task the worker was started with,
// cleaned and cut for the list. Empty when the machine did not store one.
func (m Model) workerTaskLine(session string) string {
	a, ok := m.agentByID(session)
	if !ok || a.Worker == nil {
		return ""
	}
	line := sanitize(a.Worker.Task)
	if i := strings.Index(line, "\n"); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	if r := []rune(line); len(r) > 80 {
		line = string(r[:79]) + "…"
	}
	return line
}

func (m Model) workerThreadStatus(c messageConversation) string {
	for i := len(c.items) - 1; i >= 0; i-- {
		body := m.feed.items[c.items[i]].Body
		if workerNoticeLifecycle(body) {
			continue
		}
		if status, ok := workerNoticeStatus(body); ok {
			return status
		}
	}
	switch c.state {
	case protocol.StateFailed:
		return "failed"
	case protocol.StateExpired:
		return "timed out"
	}
	if a, ok := m.agentByID(c.workerSession); ok {
		if a.Online {
			return "running"
		}
		return "ended"
	}
	if c.stored {
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

// workerNoticeLifecycle tells whether a worker notice is pure lifecycle: a
// start that is queued or has begun, or a start that never became a worker.
// The stored list hides these, and the thread draws them dim.
func workerNoticeLifecycle(body string) bool {
	status, ok := workerNoticeStatus(body)
	if !ok {
		return false
	}
	switch status {
	case "started", "queued", "not started":
		return true
	}
	return false
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
	if c.event {
		return []string{sDim.Render("System event")}
	}
	return m.conversationLines(c, w)
}

func (m Model) selectedConversationItem() *feedItem {
	rows := m.messageConversations()
	if len(rows) == 0 {
		return nil
	}
	row := rows[m.selectedConversationIndex(rows)]
	for _, i := range row.items {
		if i == m.feedCur {
			return m.feedItemAt(i)
		}
	}
	if len(row.items) == 0 {
		return nil
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
	if id == "" {
		return "ended session"
	}
	if m.snap.OwnerSession != "" && id == m.snap.OwnerSession {
		return "you"
	}
	a, ok := m.agentByID(id)
	if !ok {
		return "ended session"
	}
	if protocol.IsOwner(a.Harness) {
		return "you"
	}
	if strings.TrimSpace(a.Profile.Name) == "" {
		return "ended session"
	}
	name := m.feedName(id)
	if a.Worker != nil {
		if i := strings.LastIndex(name, "worker "); i >= 0 {
			return name[i:]
		}
	}
	return name
}
