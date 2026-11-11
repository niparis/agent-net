package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
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
	// The subtab and flags below are display facts. They come from the feed,
	// the stored row, or the one page the TUI already holds.
	system      bool
	systemKind  systemKind
	recipient   string
	today       int
	fromHarness string
	toHarness   string
	ownerNamed  bool
	blocked     bool
	burst       bool
	peakHour    int
	medianHour  int
	links       []string
	systemLines []string
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
		system := false
		if worker {
			id = "worker:" + workerSession
		} else {
			// The gateway groups a conversation that involves agent-net by
			// its conversation id, both its notices and the replies to them,
			// and every other conversation by its pair. The feed must use the
			// same key or the two rows never merge.
			system = it.From == "agent-net" || it.To == "agent-net"
			if system {
				id = it.ConversationID
				if id == "" {
					id = parentIDs[it.Parent]
				}
				if id == "" {
					id = it.MsgID
				}
			} else {
				// A peer thread is one row per unordered pair, even when the
				// messages were stored under several conversation ids.
				id = messagePairID(it.From, it.To)
				if id == "" {
					id = it.ConversationID
				}
				if id == "" {
					id = parentIDs[it.Parent]
				}
				if id == "" {
					id = it.MsgID
				}
			}
		}
		parentIDs[it.MsgID] = id
		g := groups[id]
		if g == nil {
			g = &messageConversation{id: id, worker: worker, workerSession: workerSession, workerOwner: workerOwner}
			groups[id] = g
		}
		if it.ConversationID != "" && !strings.HasPrefix(id, "pair:") {
			g.convID = it.ConversationID
		}
		if g.from == "" {
			g.from, g.to = it.From, it.To
		}
		g.items = append(g.items, i)
		g.ownerInstruction = g.ownerInstruction || it.OwnerInstruction != ""
		g.blocked = g.blocked || reportsBlocked(it.Body)
		if g.task == "" && protocol.IsWorkerTask(protocol.Message{WorkerTask: it.WorkerTask, Type: it.Type, ParentID: it.Parent, Body: it.Body}) {
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
	for id, g := range groups {
		if g.count == 0 {
			continue
		}
		g.fromHarness, g.toHarness = m.partyHarness(g.from), m.partyHarness(g.to)
		if strings.HasPrefix(g.id, "pair:") {
			g.convID = g.id
		}
		g.ownerNamed = g.ownerInstruction
		g.burst = m.threadBurst(*g)
		g.links = m.threadLinks(*g)
		g.systemKind, g.recipient, g.system = m.groupSystemKind(*g)
		if g.system {
			if g.recipient == "" {
				g.recipient = g.to
			}
			for _, i := range g.items {
				if it := m.feedItemAt(i); it != nil && it.isMessage() && m.sameDay(it.At, m.now()) {
					g.today++
				}
			}
		}
		groups[id] = g
	}
	out := make([]messageConversation, 0, len(groups))
	for _, g := range groups {
		if g.count == 0 {
			// Only lifecycle notices: they do not make a row.
			continue
		}
		out = append(out, *g)
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
		if protocol.IsWorkerTask(protocol.Message{WorkerTask: it.WorkerTask, Type: it.Type, ParentID: it.Parent, Body: it.Body}) && it.To != "" {
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

// selectedWorkerConversation tells whether the selection is a worker
// conversation, where the defect-found-later key applies.
func (m Model) selectedWorkerConversation() bool {
	rows := m.messageConversations()
	if len(rows) == 0 {
		return false
	}
	row := rows[m.selectedConversationIndex(rows)]
	return row.worker && row.workerSession != ""
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
	if c.system {
		return m.systemConversationLine(c, w, selected)
	}
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
	ownerNamed, blocked := c.ownerNamed, c.blocked
	burst := c.burst || m.threadBurst(c)
	fromHarness, toHarness := c.fromHarness, c.toHarness
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
		ownerNamed = ownerNamed || latest.OwnerInstruction != ""
		blocked = blocked || reportsBlocked(latest.Body)
	}
	fromName = m.partyName(from, fromName)
	fromTag := m.typeTag(from, fromHarness)
	if cto {
		fromName = m.ctoTitle()
	}
	toName = m.partyName(to, toName)
	toTag := m.typeTag(to, toHarness)
	if w <= 80 {
		// Two long peer names leave no room for the preview at 80 columns.
		fromName = ansi.Truncate(fromName, 14, "…")
		toName = ansi.Truncate(toName, 14, "…")
	}
	// The type tag stays after the cut, so "app" or "worker" is never lost.
	fromName += fromTag
	toName += toTag
	names := fromName + " ↔ " + toName
	age := m.now().Sub(c.at)
	if age < 0 {
		age = 0
	}
	age = age.Truncate(time.Minute)
	ageText := strings.TrimSuffix(age.String(), "0s")
	if age < time.Minute {
		ageText = "now"
	} else if m.width > 80 {
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
	if w <= 80 {
		messageWord = "msgs"
		if count == 1 {
			messageWord = "msg"
		}
		suffix = fmt.Sprintf("%s · %d %s", ageText, count, messageWord)
	}
	state = m.threadState(c)
	if w > 80 && state != "" {
		suffix += " · " + state
	}
	if burst {
		if w > 80 {
			suffix += fmt.Sprintf(" · burst %d/h", max(c.peakHour, 20))
		} else {
			suffix = "⚡" + suffix
		}
	}
	if blocked {
		if w > 80 {
			suffix += " · blocked"
		} else {
			suffix = "!" + suffix
		}
	}
	marker := ""
	if ownerInstruction || ownerNamed {
		marker = lipgloss.NewStyle().Foreground(roleAccent).Render("you→ ")
	}
	if w <= 80 {
		marker += stateMark(state)
	}
	style := lipgloss.NewStyle().Foreground(roleText)
	if cto {
		style = lipgloss.NewStyle().Foreground(roleCTO)
	} else if state == "failed" || state == "timed out" {
		style = lipgloss.NewStyle().Foreground(roleError)
	} else if state == "waiting" || strings.HasPrefix(state, "gw ") {
		style = lipgloss.NewStyle().Foreground(roleWarning)
	}
	room := max(w-ansi.StringWidth(lead)-ansi.StringWidth(marker)-ansi.StringWidth(suffix)-1, 0)
	line := lead + marker + padStyled(style.Render(clip(content, room)), room) + " " + sDim.Render(suffix)
	if selected {
		line = selectedRow(line)
	}
	return clip(line, w)
}

// stateMark is the compact state at 80 columns, where the full word would
// crowd out the last line. The legend explains the marks.
func stateMark(state string) string {
	switch {
	case state == "waiting":
		return lipgloss.NewStyle().Foreground(roleWarning).Render("~ ")
	case state == "answered":
		return lipgloss.NewStyle().Foreground(roleGood).Render("✓ ")
	case strings.HasPrefix(state, "gw "):
		return lipgloss.NewStyle().Foreground(roleWarning).Render("gw ")
	case state == "failed":
		return lipgloss.NewStyle().Foreground(roleError).Render("! ")
	case state == "timed out":
		return lipgloss.NewStyle().Foreground(roleError).Render("⌛ ")
	}
	return ""
}

func (m Model) workerConversationLine(c messageConversation, w int, selected bool) string {
	lead := "  "
	if selected {
		lead = lipgloss.NewStyle().Foreground(roleAccent).Render("▸") + " "
	}
	status := m.workerThreadStatus(c)
	statusStyle := workerStatusStyle(status)
	if status != "done" && status != "failed" && status != "blocked" && status != "timed out" && len(c.items) > 0 && c.typ == protocol.TypeRequest && c.state != protocol.StateReplied && c.state != protocol.StateAcknowledged && c.state != protocol.StateFailed && c.state != protocol.StateExpired {
		statusStyle = lipgloss.NewStyle().Foreground(roleWarning)
	}
	caller := m.partyName(c.workerOwner, c.fromName) + m.typeTag(c.workerOwner, c.fromHarness)
	workerName := m.conversationName(c.workerSession)
	if workerName == "ended session" {
		workerName = "worker"
	}
	content := caller + " → " + workerName
	task := firstNonEmpty(c.task, m.workerTaskLine(c.workerSession))
	if task != "" {
		content += ": " + task
	}
	count := c.count
	if count == 0 {
		count = len(c.items)
	}
	messageWord := "messages"
	if count == 1 {
		messageWord = "message"
	}
	if w <= 80 {
		messageWord = "msgs"
		if count == 1 {
			messageWord = "msg"
		}
	}
	parts := []string{fmt.Sprintf("%d %s", count, messageWord)}
	if w <= 80 {
		parts[0] = fmt.Sprintf("%d", count)
	}
	if harness := m.partyHarness(c.workerSession); harness != "" {
		parts = append(parts, harness)
	}
	if run := m.workerRunTime(c); run != "" {
		parts = append(parts, run)
	}
	if pr := m.workerPR(c); pr != "" {
		parts = append(parts, pr)
	}
	suffix := strings.Join(parts, " · ")
	var tail string
	if w <= 80 {
		// At 80 columns the status is the first thing after the name, so the
		// cut cannot take "done" away.
		tail = statusStyle.Render(status)
		if suffix != "" {
			tail += sDim.Render(" · " + suffix)
		}
	} else {
		tail = sDim.Render(suffix + " · " + status)
	}
	room := max(w-ansi.StringWidth(lead)-ansi.StringWidth(tail)-1, 0)
	line := lead + padStyled(lipgloss.NewStyle().Foreground(roleText).Render(clip(content, room)), room) + " " + tail
	if selected {
		line = selectedRow(line)
	}
	return clip(line, w)
}

// workerRunTime is the run time the latest worker notice reported, or the time
// since the worker started when the notice is not in the feed.
func (m Model) workerRunTime(c messageConversation) string {
	for i := len(c.items) - 1; i >= 0; i-- {
		it := m.feedItemAt(c.items[i])
		if it == nil {
			continue
		}
		for _, line := range strings.Split(it.Body, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "run time: ") {
				return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "run time: "))
			}
		}
	}
	if a, ok := m.agentByID(c.workerSession); ok && a.Worker != nil && a.Worker.StartedAt > 0 {
		d := m.now().Sub(time.Unix(a.Worker.StartedAt, 0))
		if d < 0 {
			d = 0
		}
		return shortDuration(d)
	}
	return ""
}

// shortDuration writes a run time in the list's small form.
func shortDuration(d time.Duration) string {
	if d < time.Minute {
		return "now"
	}
	d = d.Truncate(time.Minute)
	if d < time.Hour {
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	h, min := int(d.Hours()), int(d.Minutes())%60
	if min == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%02d", h, min)
}

// workerPR is the first PR named in the worker's task or notices.
func (m Model) workerPR(c messageConversation) string {
	texts := []string{c.task, c.lastLine}
	for _, i := range c.items {
		if it := m.feedItemAt(i); it != nil {
			texts = append(texts, it.Snippet, it.Body)
		}
	}
	for _, text := range texts {
		if u := prURLRE.FindString(text); u != "" {
			return "PR " + u[strings.LastIndex(u, "/")+1:]
		}
		if mm := prNumberRE.FindStringSubmatch(text); len(mm) > 1 {
			return "PR #" + mm[1]
		}
	}
	return ""
}

// systemConversationLine is one grouped system row: kind, recipient, and how
// many arrived today.
func (m Model) systemConversationLine(c messageConversation, w int, selected bool) string {
	lead := "  "
	if selected {
		lead = lipgloss.NewStyle().Foreground(roleAccent).Render("▸") + " "
	}
	label := c.systemKind.label(true)
	if label == "" {
		label = "notices"
	}
	recipient := m.partyName(c.to, c.toName) + m.typeTag(c.to, c.toHarness)
	content := label + " → " + recipient
	count := c.today
	word := "today"
	if count == 0 {
		count = max(c.count, 1)
		word = "total"
	}
	suffix := fmt.Sprintf("%d %s", count, word)
	room := max(w-ansi.StringWidth(lead)-ansi.StringWidth(suffix)-1, 0)
	line := lead + padStyled(lipgloss.NewStyle().Foreground(roleText).Render(clip(content, room)), room) + " " + sDim.Render(suffix)
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
	line := sanitize(a.Worker.Title)
	if line == "" {
		line = sanitize(a.Worker.Task)
	}
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
	if c.system {
		return m.systemConversationLines(c, w)
	}
	return m.conversationLines(c, w)
}

// systemConversationLines shows the messages of one system group. The newest
// conversation's full messages come from the normal detail read; the older
// rows keep their stored last line, which is what the page holds.
func (m Model) systemConversationLines(c messageConversation, w int) []string {
	out := []string{}
	seen := map[string]bool{}
	if d, ok := m.convDetail[c.convID]; ok && d.err == "" {
		for i := len(d.msgs) - 1; i >= 0; i-- {
			msg := d.msgs[i]
			if msg.ID == "" || seen[msg.ID] {
				continue
			}
			seen[msg.ID] = true
			out = append(out, m.messageLines(msg.From, "", msg.FromCTO, msg.OwnerInstruction,
				m.clock(msg.CreatedAt), msg.Body, false, w)...)
		}
	}
	for _, line := range c.systemLines {
		for _, wrapped := range wrap(line, w) {
			out = append(out, sDim.Render(wrapped))
		}
	}
	if len(out) == 0 {
		out = append(out, sDim.Render("loading messages"))
	}
	return out
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
