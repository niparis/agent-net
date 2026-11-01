package tui

import (
	"context"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/niparis/agent-net/internal/protocol"
)

// conversationPageSize is how many stored conversations one page holds.
const conversationPageSize = 50

// convDetail is the stored messages of one conversation, fetched when the
// owner selects it.
type convDetail struct {
	msgs []protocol.Message
	err  string
}

type conversationPageMsg struct {
	page  protocol.ConversationPage
	err   error
	first bool
}

type conversationMessagesMsg struct {
	id   string
	msgs []protocol.Message
	err  error
}

// messageConversations returns the rows the Messages tab draws. When the
// gateway has answered, they are the stored rows merged with the live feed;
// before that (or on an older daemon) they are the feed groups.
func (m Model) messageConversations() []messageConversation {
	if m.convLoaded {
		return m.convs
	}
	return m.feedConversations()
}

// selectedConversationIndex is the selected row. The stored list keeps its own
// cursor; the feed fallback follows the feed cursor.
func (m Model) selectedConversationIndex(rows []messageConversation) int {
	if len(rows) == 0 {
		return 0
	}
	if m.convLoaded {
		return min(max(m.convSel, 0), len(rows)-1)
	}
	return m.conversationIndex(rows)
}

// rebuildConversations merges the stored page with the live feed. A live row
// updates the matching stored row, and a conversation the owner has just
// exchanged appears at the top from the feed before the next page arrives.
func (m *Model) rebuildConversations() {
	selID := ""
	if m.convLoaded && m.convSel >= 0 && m.convSel < len(m.convs) {
		selID = m.convs[m.convSel].id
	}
	live := m.feedConversations()
	if !m.convLoaded {
		m.convs = live
		if m.convSel >= len(m.convs) {
			m.convSel = max(len(m.convs)-1, 0)
		}
		return
	}
	rows := make([]messageConversation, 0, len(live)+len(m.storedConvs))
	byID := map[string]int{}
	workerIndex := map[string]int{}
	for _, r := range live {
		byID[r.id] = len(rows)
		if r.worker && r.workerSession != "" {
			if _, ok := workerIndex[r.workerSession]; !ok {
				workerIndex[r.workerSession] = len(rows)
			}
		}
		rows = append(rows, r)
	}
	for _, v := range m.storedConvs {
		row := m.storedConversation(v)
		i, ok := byID[v.ID]
		if !ok && row.worker && row.workerSession != "" {
			i, ok = workerIndex[row.workerSession]
		}
		if ok {
			r := &rows[i]
			if row.at.After(r.at) {
				r.at, r.lastLine, r.state, r.lastMsgID = row.at, row.lastLine, row.state, row.lastMsgID
				r.typ = row.typ
			}
			if r.typ == "" {
				r.typ = row.typ
			}
			if row.worker {
				// The gateway counts a worker's content messages across all of
				// its conversations; the live feed sees only recent events.
				r.count = row.count
			} else {
				r.count = max(r.count, row.count)
			}
			r.stored, r.convID = true, row.convID
			if r.from == "" {
				r.from, r.to = row.from, row.to
			}
			if r.fromName == "" {
				r.fromName, r.toName = row.fromName, row.toName
			}
			if r.lastMsgID == "" {
				r.lastMsgID = row.lastMsgID
			}
			if r.workerSession == "" && row.worker {
				r.worker, r.workerSession, r.workerOwner = true, row.workerSession, row.workerOwner
			}
			if row.task != "" {
				r.task = row.task
			}
			continue
		}
		byID[v.ID] = len(rows)
		if row.worker && row.workerSession != "" {
			if _, exists := workerIndex[row.workerSession]; !exists {
				workerIndex[row.workerSession] = len(rows)
			}
		}
		rows = append(rows, row)
	}
	// The stored list is newest activity first. The feed fallback keeps its
	// rank order so an owner instruction stays at the top there.
	if m.hideWorkerThreads {
		kept := rows[:0]
		for _, r := range rows {
			if !r.worker {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	if needle := strings.ToLower(firstNonEmpty(m.uiFilter, m.filter)); needle != "" {
		kept := rows[:0]
		for _, r := range rows {
			text := strings.ToLower(strings.Join([]string{r.from, r.to, r.fromName, r.toName, r.lastLine, r.task,
				m.conversationName(r.from), m.conversationName(r.to)}, " "))
			if strings.Contains(text, needle) {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].at.Equal(rows[j].at) {
			return rows[i].id > rows[j].id
		}
		return rows[i].at.After(rows[j].at)
	})
	m.convs = rows
	if selID != "" {
		for i := range m.convs {
			if m.convs[i].id == selID {
				m.convSel = i
				break
			}
		}
	}
	if m.convSel >= len(m.convs) {
		m.convSel = max(len(m.convs)-1, 0)
	}
}

// storedConversation turns one gateway row into a row the screen draws.
func (m Model) storedConversation(v protocol.ConversationView) messageConversation {
	c := messageConversation{
		id: v.ID, convID: v.ID, from: v.From, to: v.To, fromName: v.FromName, toName: v.ToName,
		count: v.Count, lastLine: sanitize(v.LastLine), state: v.State, typ: v.Type, at: v.At,
		lastMsgID: v.LastMessageID, cto: v.FromCTO, ownerInstruction: v.OwnerInstruction,
		stored: true, task: sanitize(v.Task),
	}
	if v.WorkerSession != "" {
		c.worker, c.workerSession, c.workerOwner = true, v.WorkerSession, v.WorkerOwner
		if c.workerOwner == "" {
			c.workerOwner = v.From
		}
		return c
	}
	m.markWorker(&c)
	return c
}

// markWorker finds a worker at one end of a stored conversation and names the
// owner. The task itself comes from the worker's session in the snapshot.
func (m Model) markWorker(c *messageConversation) {
	for _, pair := range [][2]string{{c.from, c.to}, {c.to, c.from}} {
		a, ok := m.agentByID(pair[0])
		if ok && a.Worker != nil && a.Worker.Owner == pair[1] {
			c.worker, c.workerSession, c.workerOwner = true, pair[0], pair[1]
			return
		}
	}
}

// partyName prefers the session name the screen already knows and falls back
// to the name the gateway stored, so an old conversation still has a label.
// A session whose record is gone is an ended session, never an unknown one.
func (m Model) partyName(id, fallback string) string {
	if name := m.conversationName(id); name != "ended session" {
		return name
	}
	if fallback != "" {
		return sanitize(fallback)
	}
	return "ended session"
}

// conversationCmd asks for one page. first replaces the first page and keeps
// the older pages already loaded; a later page is appended.
func (m Model) conversationCmd(cursor string, first bool) tea.Cmd {
	src := m.src
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		page, err := src.Conversations(ctx, protocol.QueryConversations{Cursor: cursor, Limit: conversationPageSize})
		return conversationPageMsg{page: page, err: err, first: first}
	}
}

// conversationMessagesCmd reads one conversation's stored messages.
func (m Model) conversationMessagesCmd(id string) tea.Cmd {
	src := m.src
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		msgs, err := src.ConversationMessages(ctx, id)
		return conversationMessagesMsg{id: id, msgs: msgs, err: err}
	}
}

// wantConversations starts the next page when the cursor is near the end.
func (m *Model) wantConversations() tea.Cmd {
	if m.tab != messagesTab || m.help || !m.convLoaded || m.convNext == "" || m.convLoading {
		return nil
	}
	rows := m.messageConversations()
	if len(rows) == 0 || m.selectedConversationIndex(rows) < len(rows)-3 {
		return nil
	}
	m.convLoading = true
	return m.conversationCmd(m.convNext, false)
}

// wantConversationDetails fetches the stored messages of the selected row.
// It runs from Update, never from View.
func (m *Model) wantConversationDetails() tea.Cmd {
	if m.tab != messagesTab || m.help {
		return nil
	}
	rows := m.messageConversations()
	if len(rows) == 0 {
		return nil
	}
	row := rows[m.selectedConversationIndex(rows)]
	if row.event || row.convID == "" {
		return nil
	}
	// A live feed row already has its messages; the gateway read is for a
	// stored row, including one the feed also has a few recent lines of.
	if !row.stored && len(row.items) > 0 {
		return nil
	}
	if _, ok := m.convDetail[row.convID]; ok {
		return nil
	}
	if m.convDetailLoading[row.convID] {
		return nil
	}
	m.convDetailLoading[row.convID] = true
	return m.conversationMessagesCmd(row.convID)
}

// applyConversationPage keeps the stored rows and rebuilds the list the screen
// draws. A first page refreshes the newest rows and leaves older loaded pages
// in place; a later page appends.
func (m *Model) applyConversationPage(msg conversationPageMsg) {
	m.convLoading = false
	if msg.err != nil {
		m.convErr = sanitize(msg.err.Error())
		// An older daemon or gateway has no stored list: keep the feed view.
		if isUnavailable(msg.err) {
			m.convLoaded = false
		}
		return
	}
	m.convErr = ""
	m.convTotal = msg.page.Total
	if msg.first {
		merged := make(map[string]protocol.ConversationView, len(m.storedConvs)+len(msg.page.Conversations))
		for _, v := range m.storedConvs {
			merged[v.ID] = v
		}
		for _, v := range msg.page.Conversations {
			merged[v.ID] = v
		}
		m.storedConvs = m.storedConvs[:0]
		for _, v := range merged {
			m.storedConvs = append(m.storedConvs, v)
		}
		next := msg.page.Next
		if m.convNext != "" && len(m.storedConvs) > len(msg.page.Conversations) {
			// Older pages are already loaded. Keep the cursor for the next
			// page that is still missing instead of walking back to page two
			// on every refresh.
			next = m.convNext
		}
		m.convNext = next
		if msg.page.Total > 0 || len(msg.page.Conversations) > 0 {
			m.convLoaded = true
		}
	} else {
		seen := make(map[string]bool, len(m.storedConvs))
		for _, v := range m.storedConvs {
			seen[v.ID] = true
		}
		for _, v := range msg.page.Conversations {
			if !seen[v.ID] {
				m.storedConvs = append(m.storedConvs, v)
			}
		}
		m.convNext = msg.page.Next
		if len(m.storedConvs) > 0 {
			m.convLoaded = true
		}
	}
	sort.SliceStable(m.storedConvs, func(i, j int) bool {
		if m.storedConvs[i].At.Equal(m.storedConvs[j].At) {
			return m.storedConvs[i].ID > m.storedConvs[j].ID
		}
		return m.storedConvs[i].At.After(m.storedConvs[j].At)
	})
	m.rebuildConversations()
}

func (m *Model) applyConversationMessages(msg conversationMessagesMsg) {
	delete(m.convDetailLoading, msg.id)
	if msg.err != nil {
		m.convDetail[msg.id] = convDetail{err: bodyError(msg.err)}
		return
	}
	m.convDetail[msg.id] = convDetail{msgs: msg.msgs}
}

// conversationLines returns the chat lines of a stored conversation, oldest
// first. Feed lines that arrived after the fetched page go at the end.
func (m Model) conversationLines(c messageConversation, w int) []string {
	out := []string{}
	seen := map[string]bool{}
	selectedID := ""
	if m.feedCur >= 0 && m.feedCur < len(m.feed.items) {
		selectedID = m.feed.items[m.feedCur].MsgID
	}
	if d, ok := m.convDetail[c.convID]; ok && d.err == "" {
		for i := len(d.msgs) - 1; i >= 0; i-- {
			msg := d.msgs[i]
			if msg.ID == "" || seen[msg.ID] {
				continue
			}
			seen[msg.ID] = true
			out = append(out, m.messageLines(msg.From, c.workerSession, msg.FromCTO, msg.OwnerInstruction,
				m.clock(msg.CreatedAt), msg.Body, msg.ID == selectedID, w)...)
		}
	}
	for _, i := range c.items {
		it := m.feed.items[i]
		if it.MsgID != "" && seen[it.MsgID] {
			continue
		}
		if it.MsgID != "" {
			seen[it.MsgID] = true
		}
		body := it.Body
		if body == "" {
			body = m.missingMessageBody(it)
		}
		out = append(out, m.messageLines(it.From, c.workerSession, it.FromCTO, it.OwnerInstruction,
			m.clock(it.At), body, i == m.feedCur, w)...)
	}
	if len(out) == 0 {
		if d, ok := m.convDetail[c.convID]; ok && d.err != "" {
			return []string{sDim.Render(clip(d.err, w))}
		}
		if m.convDetailLoading[c.convID] || c.stored {
			return []string{sDim.Render("loading messages")}
		}
	}
	return out
}

// messageLines draws one message: its header and up to three body lines.
func (m Model) messageLines(senderID, workerSession string, fromCTO bool, ownerInstruction, clock, body string, selected bool, w int) []string {
	sender := m.conversationName(senderID)
	if sender == "ended session" && workerSession != "" && senderID == workerSession {
		sender = "ended worker"
	}
	if fromCTO {
		sender = m.ctoTitle()
	}
	marker := ""
	if ownerInstruction != "" {
		marker = lipglossAccent("you→ ")
	}
	header := sDim.Render(clock+" ") + marker + sDim.Render(sender)
	if selected {
		header = lipglossAccent("▸ ") + header
	} else {
		header = "  " + header
	}
	out := []string{header}
	if ownerInstruction != "" {
		for _, line := range wrap("Your instruction: "+ownerInstruction, w) {
			out = append(out, sDim.Render(line))
		}
	}
	if text, ok := workerNoticeLine(body); ok {
		switch {
		case workerNoticeLifecycle(body):
			out = append(out, sDim.Render(clip(text, w)))
		case strings.Contains(text, "✓ done"):
			out = append(out, sBusy.Render(clip(text, w)))
		default:
			out = append(out, sBad.Render(clip(text, w)))
		}
		return out
	}
	if body == "" {
		out = append(out, sDim.Render(clip("empty message", w)))
		return out
	}
	lines := strings.Split(body, "\n")
	for j := 0; j < len(lines) && j < 3; j++ {
		out = append(out, clip(lines[j], w))
	}
	if len(lines) > 3 {
		out = append(out, sDim.Render("…"))
	}
	return out
}

// lipglossAccent is the selected-row and owner-marker colour.
func lipglossAccent(s string) string {
	return lipgloss.NewStyle().Foreground(roleAccent).Render(s)
}
