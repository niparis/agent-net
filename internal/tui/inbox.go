package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// The Inbox tab (tab 7) is one place for everything that waits for the owner.
// It holds every kind the Needs you rows and the old inbox overlay held, with
// the same keys and the same operations.

type inboxGroup int

const (
	inboxHeld inboxGroup = iota
	inboxOutcome
	inboxLearned
	inboxProposal
	inboxApproval
	inboxNotice
)

var inboxGroupOrder = []inboxGroup{inboxHeld, inboxOutcome, inboxLearned, inboxProposal, inboxApproval, inboxNotice}

func (g inboxGroup) title() string {
	switch g {
	case inboxHeld:
		return "Held requests"
	case inboxOutcome:
		return "Outcomes"
	case inboxLearned:
		return "Learned suggestions"
	case inboxProposal:
		return "CTO proposals"
	case inboxApproval:
		return "Approvals and questions"
	case inboxNotice:
		return "Notices"
	}
	return ""
}

// inboxRow is one waiting item. kind names the row's actions, not its group:
// held, outcome, learned, proposal, approval, question, notice or waiting (a
// session that reports a wait).
type inboxRow struct {
	group    inboxGroup
	key      string
	kind     string
	name     string
	text     string
	since    int64
	session  string
	held     protocol.Message
	outcome  protocol.OutcomeInboxEntry
	learned  protocol.LearnedSuggestion
	proposal protocol.CTOProposal
	item     protocol.InboxItem
}

// inboxRows builds every waiting row, grouped and newest first inside a group.
func (m Model) inboxRows() []inboxRow {
	var rows []inboxRow
	for _, held := range m.snap.Held {
		recipient := held.To
		if name := m.inboxSessionName(held.To); name != held.To {
			recipient = name
		}
		label := held.FromName
		if label == "" {
			label = held.From
		}
		first, _, _ := strings.Cut(held.Body, "\n")
		rows = append(rows, inboxRow{
			group: inboxHeld, key: "held:" + held.ID, kind: "held",
			name: label + " -> " + recipient, text: first,
			since: held.CreatedAt.Unix(), session: held.To, held: held,
		})
	}
	for _, e := range m.snap.Outcomes {
		text := e.Request + " | " + e.Outcome + ": " + e.Reason
		if e.Recommendation != "" {
			text += " | " + e.Recommendation
		}
		rows = append(rows, inboxRow{
			group: inboxOutcome, key: "outcome:" + e.ReplyID, kind: "outcome",
			name: e.From + " -> " + e.To, text: text,
			since: e.At / 1e9, outcome: e,
		})
	}
	for _, v := range m.snap.Learned {
		if v.Status != "pending" || v.Severity != "major" {
			continue
		}
		rows = append(rows, inboxRow{
			group: inboxLearned, key: fmt.Sprintf("learned:%d", v.ID), kind: "learned",
			name: v.Node, text: learnedQuestion(v),
			since: v.CreatedAt / 1e9, learned: v,
		})
	}
	for _, p := range m.proposals {
		if p.Status != "open" {
			continue
		}
		rows = append(rows, inboxRow{
			group: inboxProposal, key: proposalKey(p), kind: "proposal",
			name: "CTO proposal", text: proposalSummary(p),
			since: p.CreatedAt.Unix(), proposal: p,
		})
	}
	for _, item := range m.inboxItems {
		if item.Status != "open" {
			continue
		}
		switch item.Kind {
		case "check":
			name := "Notice"
			if item.Node == "apps" {
				name = "Connected app"
			}
			rows = append(rows, inboxRow{
				group: inboxNotice, key: "notice:" + item.ID, kind: "notice",
				name: name, text: appApprovalNoticeText(item.Text),
				since: item.CreatedAt.Unix(), item: item,
			})
		case "approval", "question":
			rows = append(rows, inboxRow{
				group: inboxApproval, key: "inbox:" + item.ID, kind: item.Kind,
				name: m.inboxSessionName(item.SessionID), text: item.Text,
				since: item.CreatedAt.Unix(), session: item.SessionID, item: item,
			})
		}
	}
	for _, a := range m.snap.Agents {
		if protocol.IsOwner(a.Harness) || a.Worker != nil || a.Waiting == nil {
			continue
		}
		if m.hiddenSessions[a.SessionID] && !m.showHiddenSessions {
			continue
		}
		if (!a.Online || a.Status == protocol.StatusDormant) && !m.showOffline {
			continue
		}
		if a.Worker != nil && !a.Online {
			continue
		}
		if m.activeOnly && m.idleLong(a) {
			continue
		}
		if !matches(a, m.filter) {
			continue
		}
		rows = append(rows, inboxRow{
			group: inboxApproval, key: "waiting:" + a.SessionID, kind: "waiting",
			name: m.needsName(a), text: needsText(a.Waiting),
			since: a.Waiting.Since, session: a.SessionID,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].group != rows[j].group {
			return rows[i].group < rows[j].group
		}
		if rows[i].since != rows[j].since {
			return rows[i].since > rows[j].since
		}
		return rows[i].key < rows[j].key
	})
	return rows
}

// inboxSessionName is the name the Inbox shows for a session.
func (m Model) inboxSessionName(id string) string {
	if a, ok := m.agentByID(id); ok {
		if name := m.needsName(a); name != "" {
			return name
		}
	}
	return id
}

// waitingCount is the number of items waiting for the owner.
func (m Model) waitingCount() int { return len(m.inboxRows()) }

// anySessionWaiting tells whether a table row reports a wait.
func (m Model) anySessionWaiting() bool {
	for _, a := range m.snap.Agents {
		if a.Waiting != nil && a.Worker == nil && !protocol.IsOwner(a.Harness) {
			return true
		}
	}
	return false
}

func (m Model) inboxSelected(rows []inboxRow) (inboxRow, bool) {
	if len(rows) == 0 {
		return inboxRow{}, false
	}
	return rows[min(max(m.inboxIndex, 0), len(rows)-1)], true
}

// inboxTabView draws the tab: a title, the groups and their rows, or the
// detail of the selected row, or the empty state.
func (m Model) inboxTabView(w, h int) []string {
	out := []string{m.titleLine(w)}
	if m.inboxError != "" {
		out = append(out, sBad.Render(clip("  "+m.inboxError, w)))
	}
	rows := m.inboxRows()
	if len(rows) == 0 {
		for len(out) < h {
			out = append(out, "")
		}
		if h > 1 {
			out[1+max((h-2)/2, 0)] = sDim.Render(centerText("Nothing is waiting for you.", w))
		}
		return out[:max(h, 1)]
	}
	if m.inboxDetail {
		out = append(out, section("Inbox detail", w))
		row, _ := m.inboxSelected(rows)
		for _, line := range m.inboxDetailLines(row) {
			out = append(out, clip("  "+line, w))
		}
		for len(out) < h {
			out = append(out, "")
		}
		return out[:max(h, 1)]
	}

	type displayLine struct {
		text string
		row  int
	}
	var display []displayLine
	for _, group := range inboxGroupOrder {
		heading := false
		for i, row := range rows {
			if row.group != group {
				continue
			}
			if !heading {
				display = append(display, displayLine{text: sDim.Render(group.title()), row: -1})
				heading = true
			}
			display = append(display, displayLine{row: i})
		}
	}
	nameW := 10
	for _, row := range rows {
		nameW = max(nameW, ansi.StringWidth(sanitize(row.name)))
	}
	nameW = min(nameW, 26)
	selectedLine := 0
	for i, line := range display {
		if line.row < 0 {
			continue
		}
		if line.row == m.inboxIndex {
			selectedLine = i
		}
		display[i].text = m.inboxRowLine(rows[line.row], nameW, w, line.row == m.inboxIndex)
	}
	room := max(h-1, 1)
	top := 0
	if selectedLine >= room {
		top = selectedLine - room + 1
	}
	if top > 0 && top < len(display) && display[top].row >= 0 && display[top-1].row < 0 {
		top--
	}
	for i := top; i < len(display) && len(out) < h; i++ {
		out = append(out, clip(display[i].text, w))
	}
	for len(out) < h {
		out = append(out, "")
	}
	return out[:max(h, 1)]
}

func (m Model) inboxRowLine(row inboxRow, nameW, w int, selected bool) string {
	const ageW = 4
	marker := "  "
	if selected {
		marker = sAccent.Render("▸") + " "
	}
	name := cell(sanitize(row.name), nameW)
	age := sDim.Render(cellR(needsAge(m.now(), row.since), ageW))
	room := max(w-ansi.StringWidth(marker)-nameW-1-ageW-1, 4)
	text := clip(sanitize(row.text), room)
	line := marker + name + " " + padStyled(text, room) + " " + age
	if selected {
		return selectedRow(clip(line, w))
	}
	return clip(line, w)
}

func centerText(s string, w int) string {
	s = sanitize(s)
	return strings.Repeat(" ", max((w-ansi.StringWidth(s))/2, 0)) + s
}

func (m Model) inboxDetailLines(row inboxRow) []string {
	var out []string
	switch row.kind {
	case "held":
		out = append(out, "From: "+sanitize(row.held.FromName), "To: "+sanitize(m.inboxSessionName(row.held.To)), "", sanitizeBody(row.held.Body))
	case "outcome":
		out = append(out, "From: "+sanitize(row.outcome.From), "To: "+sanitize(row.outcome.To),
			"Request: "+sanitize(row.outcome.Request), "Outcome: "+sanitize(row.outcome.Outcome),
			"Reason: "+sanitize(row.outcome.Reason))
		if row.outcome.Recommendation != "" {
			out = append(out, "Recommendation: "+sanitize(row.outcome.Recommendation))
		}
	case "learned":
		out = append(out, "Node: "+sanitize(row.learned.Node), sanitize(row.text))
		for _, e := range row.learned.Evidence {
			if e.Excerpt != "" {
				out = append(out, "Because: "+sanitize(e.Excerpt))
			}
		}
	case "proposal":
		out = append(out, sanitize(row.text))
	case "notice", "approval", "question":
		out = append(out, "Kind: "+sanitize(row.item.Kind), "Status: "+sanitize(row.item.Status),
			"Session: "+sanitize(row.item.SessionID), "Node: "+sanitize(row.item.Node),
			"Text: "+sanitize(row.item.Text))
		if row.item.Recommendation != "" {
			out = append(out, "Recommendation: "+sanitize(row.item.Recommendation))
		}
		if row.item.MessageID != "" {
			out = append(out, "Message: "+sanitize(row.item.MessageID))
		}
		if row.item.ConversationID != "" {
			out = append(out, "Conversation: "+sanitize(row.item.ConversationID))
		}
		if row.item.Answer != "" {
			out = append(out, "Answer: "+sanitize(row.item.Answer))
		}
	case "waiting":
		out = append(out, "Session: "+sanitize(m.inboxSessionName(row.session)), sanitize(row.text))
	}
	return out
}

type inboxAnswerMsg struct {
	id  string
	err error
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

// inboxTabKey handles the Inbox tab and its detail view.
func (m Model) inboxTabKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.inboxRows()
	if m.inboxEditing {
		switch k.Type {
		case tea.KeyEsc:
			m.inboxEditing = false
			return m, nil
		case tea.KeyEnter:
			answer := strings.TrimSpace(m.inboxInput.Value())
			if answer != "" {
				row, ok := m.inboxSelected(rows)
				if ok {
					m.inboxEditing = false
					return m, m.inboxAnswer(row.item.ID, answer)
				}
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.inboxInput, cmd = m.inboxInput.Update(k)
		return m, cmd
	}
	key := keyAction(contextInbox, k.String())
	switch key {
	case actQuit:
		m.quitting = true
		return m, tea.Quit
	case "help":
		m.openHelp()
		return m, nil
	}
	if m.inboxDetail {
		if key == "back" || key == actClose {
			m.inboxDetail = false
		}
		return m, nil
	}
	switch key {
	case actUp:
		m.inboxIndex = max(0, m.inboxIndex-1)
		return m, nil
	case actDown:
		m.inboxIndex = min(max(len(rows)-1, 0), m.inboxIndex+1)
		return m, nil
	case actPageUp:
		m.inboxIndex = max(0, m.inboxIndex-max(len(rows), 1))
		return m, nil
	case actPageDown:
		m.inboxIndex = min(max(len(rows)-1, 0), m.inboxIndex+max(len(rows), 1))
		return m, nil
	case actFirst:
		m.inboxIndex = 0
		return m, nil
	case actLast:
		m.inboxIndex = max(len(rows)-1, 0)
		return m, nil
	}
	row, ok := m.inboxSelected(rows)
	if !ok {
		return m, nil
	}
	switch key {
	case "details":
		switch row.kind {
		case "held", "waiting":
			m.selected = row.session
			return m.startEnterID(row.session)
		case "proposal":
			m.proposalDetail = &proposalDetail{proposal: row.proposal, loading: true}
			return m, m.proposalDetailCmd(row.proposal)
		default:
			m.inboxDetail = true
		}
	case "approve", "refuse":
		return m.inboxDecide(row, key == "approve")
	case "let stand", "overrule":
		action := "let_stand"
		if key == "overrule" {
			action = "overrule"
		}
		return m, m.inboxResolveOutcome(row, action)
	case "approve all":
		if row.kind == "proposal" {
			m.openProposalConfirm()
		}
	case "answer":
		if row.kind == "approval" || row.kind == "question" {
			m.inboxInput = textinput.New()
			m.inboxInput.CharLimit = protocol.MaxBodyBytes
			m.inboxInput.Focus()
			m.inboxEditing = true
		}
	case "dismiss":
		if row.kind == "notice" {
			return m, m.inboxAnswer(row.item.ID, "seen")
		}
	}
	return m, nil
}

func (m Model) inboxDecide(row inboxRow, approve bool) (tea.Model, tea.Cmd) {
	switch row.kind {
	case "held":
		answerer, ok := m.src.(interface {
			AnswerHeld(context.Context, protocol.HeldAnswer) error
		})
		if !ok {
			m.fail("Held request actions unavailable.")
			return m, nil
		}
		action := "a"
		if !approve {
			action = "r"
		}
		answer := protocol.HeldAnswer{MessageID: row.held.ID, Action: action}
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), Timeout)
			defer cancel()
			if err := answerer.AnswerHeld(ctx, answer); err != nil {
				return actionMsg{err: err}
			}
			return actionMsg{text: "Request answered."}
		}
	case "learned":
		c, ok := m.src.(inboxCaller)
		if !ok {
			m.fail("Learned suggestion actions unavailable.")
			return m, nil
		}
		action := "accept"
		if !approve {
			action = "reject"
		}
		id := row.learned.ID
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), Timeout)
			defer cancel()
			_, err := c.Call(ctx, "learned_answer", map[string]any{"suggestion_id": id, "action": action})
			if err != nil {
				return actionMsg{err: err}
			}
			return actionMsg{text: "Suggestion " + action + "ed."}
		}
	case "proposal":
		action := "a"
		if !approve {
			action = "x"
		}
		return m, m.proposalAnswerCmd(row.proposal, action)
	case "approval", "question":
		answer := "approve"
		if !approve {
			answer = "refuse"
		}
		return m, m.inboxAnswer(row.item.ID, answer)
	}
	return m, nil
}

func (m Model) inboxResolveOutcome(row inboxRow, action string) tea.Cmd {
	src := m.src
	id := row.outcome.ReplyID
	return func() tea.Msg {
		resolver, ok := src.(outcomeResolver)
		if !ok {
			return actionMsg{err: fmt.Errorf("outcome decisions unavailable")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		if err := resolver.ResolveOutcome(ctx, id, action); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{text: "Outcome: " + action}
	}
}

// inboxHints lists only the keys that act on the selected row.
func (m Model) inboxHints() []hint {
	move := bindingPairHint(contextInbox, actUp, actDown)
	help := bindingHint(contextInbox, "help")
	quit := bindingHint(contextInbox, actQuit)
	if m.inboxDetail {
		return []hint{bindingHint(contextInbox, "back"), help, quit}
	}
	rows := m.inboxRows()
	row, ok := m.inboxSelected(rows)
	if !ok {
		return []hint{move, help, quit}
	}
	switch row.kind {
	case "held":
		open := bindingHint(contextInbox, "details")
		open.label = "open session"
		return []hint{open, bindingHint(contextInbox, "approve"), bindingHint(contextInbox, "refuse"), help, quit}
	case "waiting":
		open := bindingHint(contextInbox, "details")
		open.label = "open session"
		return []hint{open, help, quit}
	case "outcome":
		return []hint{bindingHint(contextInbox, "let stand"), bindingHint(contextInbox, "overrule"), help, quit}
	case "learned":
		return []hint{{key: "a", label: "accept", prio: 1}, {key: "r", label: "reject", prio: 1}, help, quit}
	case "proposal":
		return []hint{bindingHint(contextInbox, "details"), bindingHint(contextInbox, "approve"), {key: "r", label: "decline", prio: 1}, bindingHint(contextInbox, "approve all"), help, quit}
	case "notice":
		return []hint{bindingHint(contextInbox, "details"), bindingHint(contextInbox, "dismiss"), help, quit}
	case "approval", "question":
		return []hint{bindingHint(contextInbox, "details"), bindingHint(contextInbox, "approve"), bindingHint(contextInbox, "refuse"), bindingHint(contextInbox, "answer"), help, quit}
	}
	return []hint{move, help, quit}
}

// inboxCaller is the local call the Inbox and the CTO box need.
type inboxCaller interface {
	Call(context.Context, string, any) (json.RawMessage, error)
}
