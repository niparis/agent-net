package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
)

// A hand-over is the owner's choice to share one private item with the CTO.
// The CTO reads only public data, so the owner sends the item on purpose: the
// preview shows the exact text, and Enter sends that same string through the
// owner-gated cto_send path.
const maxHandoverRunes = 4000

const handoverCutMarker = "\n\n[hand-over cut at 4000 characters]"

// ctoSender is the owner path to the CTO. The daemon's cto_send method is
// behind the owner gate.
type ctoSender interface {
	CTOSend(context.Context, string) error
}

type handover struct {
	// text is exactly what Enter sends, after the cap.
	text string
	// msgID is set while a message body is fetched.
	msgID   string
	loading bool
	err     string
	scroll  int
}

type handoverMsg struct {
	id   string
	text string
	err  error
}

// capHandover cuts long text at the stated limit, keeping valid runes.
func capHandover(s string) string {
	r := []rune(s)
	if len(r) <= maxHandoverRunes {
		return s
	}
	suffix := []rune(handoverCutMarker)
	keep := maxHandoverRunes - len(suffix)
	if keep < 0 {
		keep = 0
	}
	return string(r[:keep]) + handoverCutMarker
}

func handoverHeader(origin string) string {
	return "Hand-over from the owner: " + origin + "\n\n"
}

// startHandover opens the preview for the selected row or message.
func (m Model) startHandover() (tea.Model, tea.Cmd) {
	if m.tab == messagesTab || (m.tab == sessionsTab && m.focus == focusFeed) {
		return m.startMessageHandover()
	}
	if m.tab != sessionsTab {
		m.fail("Hand-over works on Sessions and Messages rows.")
		return m, nil
	}
	return m.startRowHandover()
}

func (m Model) startRowHandover() (tea.Model, tea.Cmd) {
	if p, ok := m.selectedProposal(); ok {
		m.handover = &handover{text: capHandover(m.handoverProposal(p))}
		return m, nil
	}
	if raw, ok := strings.CutPrefix(m.selected, "learned:"); ok {
		id, _ := strconv.ParseInt(raw, 10, 64)
		for _, v := range m.snap.Learned {
			if v.ID == id {
				m.handover = &handover{text: capHandover(m.handoverLearned(v))}
				return m, nil
			}
		}
	}
	if raw, ok := strings.CutPrefix(m.selected, "outcome:"); ok {
		for _, e := range m.snap.Outcomes {
			if e.ReplyID == raw {
				m.handover = &handover{text: capHandover(m.handoverOutcome(e))}
				return m, nil
			}
		}
	}
	for _, held := range m.snap.Held {
		if held.ID == m.selected {
			m.handover = &handover{text: capHandover(m.handoverHeld(held))}
			return m, nil
		}
	}
	for _, item := range m.inboxItems {
		if item.Kind == "approval" && item.Status == "open" && item.SessionID == m.selected {
			m.handover = &handover{text: capHandover(m.handoverInbox(item))}
			return m, nil
		}
	}
	if a, ok := m.selectedAgent(); ok {
		origin := "a session row in Sessions"
		if m.selNeeds {
			origin = "a session that waits in Needs you"
		}
		m.handover = &handover{text: capHandover(m.handoverSession(a, origin))}
		return m, nil
	}
	m.fail("Select a Needs you or Sessions row to hand over.")
	return m, nil
}

func (m Model) startMessageHandover() (tea.Model, tea.Cmd) {
	it := m.selectedConversationItem()
	if it == nil || !it.isMessage() {
		m.fail("Select a message to hand over.")
		return m, nil
	}
	item := *it
	if item.Body != "" || item.Snip == snipDone {
		msg := protocol.Message{
			ID: item.MsgID, ConversationID: item.ConversationID, From: item.From, To: item.To,
			Type: item.Type, State: item.State, Body: item.Body, CreatedAt: item.At,
		}
		m.handover = &handover{msgID: item.MsgID, text: capHandover(m.handoverMessage(&item, msg))}
		return m, nil
	}
	m.handover = &handover{msgID: item.MsgID, loading: true}
	return m, m.handoverBodyCmd(&item)
}

func (m Model) handoverBodyCmd(it *feedItem) tea.Cmd {
	src := m.src
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		msg, err := src.Message(ctx, it.MsgID)
		if err != nil {
			return handoverMsg{id: it.MsgID, err: err}
		}
		return handoverMsg{id: it.MsgID, text: capHandover(m.handoverMessage(it, msg))}
	}
}

func (m Model) handoverKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	h := m.handover
	switch k.String() {
	case "esc":
		m.handover = nil
	case "q":
		m.quitting = true
		return m, tea.Quit
	case "enter":
		if h.loading || h.err != "" {
			return m, nil
		}
		text, title := h.text, m.ctoTitle()
		m.handover = nil
		src, ok := m.src.(ctoSender)
		if !ok {
			m.fail("The " + title + " is unavailable.")
			return m, nil
		}
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), Timeout)
			defer cancel()
			if err := src.CTOSend(ctx, text); err != nil {
				return actionMsg{err: err}
			}
			return actionMsg{text: "Sent to " + title + "."}
		}
	case "up", "k":
		h.scroll = max(h.scroll-1, 0)
	case "down", "j":
		h.scroll++
	case "pgup":
		h.scroll = max(h.scroll-10, 0)
	case "pgdown":
		h.scroll += 10
	case "home", "g":
		h.scroll = 0
	case "end", "G":
		h.scroll = 1 << 20
	}
	return m, nil
}

// ---- the text that is sent ----

func (m Model) handoverName(id string) string {
	if id == "" {
		return "unknown"
	}
	if a, ok := m.agentByID(id); ok {
		if protocol.IsOwner(a.Harness) {
			return "you"
		}
		if name := strings.TrimSpace(a.Profile.Name); name != "" {
			return sanitize(name)
		}
	}
	return sanitize(shortID(id))
}

func (m Model) handoverSession(a protocol.AgentView, origin string) string {
	var b strings.Builder
	b.WriteString(handoverHeader(origin))
	m.writeHandoverSession(&b, a)
	return b.String()
}

func (m Model) writeHandoverSession(b *strings.Builder, a protocol.AgentView) {
	name := firstNonEmpty(a.Profile.Name, shortID(a.SessionID))
	fmt.Fprintf(b, "Session: %s (%s)\n", sanitize(name), sanitize(a.SessionID))
	fmt.Fprintf(b, "Machine: %s\n", sanitize(m.nodeName(a.NodeID, a.Hostname)))
	if project := firstNonEmpty(a.Repo.Project, a.Profile.Project); project != "" {
		fmt.Fprintf(b, "Project: %s\n", sanitize(m.projectName(project)))
	}
	if a.Harness != "" {
		fmt.Fprintf(b, "Harness: %s\n", sanitize(a.Harness))
	}
	status := a.Status
	if status == "" {
		status = "unknown"
	}
	active := "unknown"
	if a.LastActive > 0 {
		active = ago(m.now().Sub(time.Unix(a.LastActive, 0))) + " ago"
	}
	fmt.Fprintf(b, "Status: %s, last active %s\n", sanitize(status), active)
	if a.Title != "" {
		fmt.Fprintf(b, "Title: %s\n", sanitize(a.Title))
	}
	if a.Profile.Task != "" {
		fmt.Fprintf(b, "Announced task: %s\n", sanitize(a.Profile.Task))
	} else {
		b.WriteString("Announced task: none announced\n")
	}
	if a.Profile.Function != "" {
		fmt.Fprintf(b, "Function: %s\n", sanitize(a.Profile.Function))
	}
	if a.Profile.Expertise != "" {
		fmt.Fprintf(b, "Expertise: %s\n", sanitize(a.Profile.Expertise))
	}
	if a.Waiting != nil {
		kind := a.Waiting.Kind
		if kind == "" {
			kind = protocol.WaitingInput
		}
		text := sanitize(a.Waiting.Text)
		if text == "" {
			text = "(no text)"
		}
		fmt.Fprintf(b, "Waiting: %s - %s\n", sanitize(kind), text)
		if len(a.Waiting.Options) > 0 {
			fmt.Fprintf(b, "options: %s\n", sanitize(strings.Join(a.Waiting.Options, ", ")))
		}
		if a.Waiting.Urgency != "" {
			fmt.Fprintf(b, "Urgency: %s\n", sanitize(a.Waiting.Urgency))
		}
	}
}

func (m Model) handoverHeld(held protocol.Message) string {
	var b strings.Builder
	b.WriteString(handoverHeader("a held request in Needs you"))
	first, _, _ := strings.Cut(sanitizeBody(held.Body), "\n")
	fmt.Fprintf(&b, "Request: %s\n", first)
	from := firstNonEmpty(held.FromName, m.handoverName(held.From))
	fmt.Fprintf(&b, "From: %s (%s)\n", sanitize(from), sanitize(held.From))
	fmt.Fprintf(&b, "To: %s (%s)\n", m.handoverName(held.To), sanitize(held.To))
	fmt.Fprintf(&b, "Type: %s\n", dashIfEmpty(held.Type))
	if !held.CreatedAt.IsZero() {
		fmt.Fprintf(&b, "Created: %s\n", held.CreatedAt.In(m.loc).Format("2006-01-02 15:04:05"))
	}
	b.WriteString("Body:\n")
	b.WriteString(sanitizeBody(held.Body))
	b.WriteString("\n")
	return b.String()
}

func (m Model) handoverInbox(item protocol.InboxItem) string {
	var b strings.Builder
	b.WriteString(handoverHeader("an inbox item in Needs you"))
	fmt.Fprintf(&b, "Inbox item: %s (%s)\n", sanitize(item.Kind), sanitize(item.Status))
	if item.Node != "" {
		fmt.Fprintf(&b, "Machine: %s\n", sanitize(item.Node))
	}
	fmt.Fprintf(&b, "Question: %s\n", sanitize(item.Text))
	if item.Recommendation != "" {
		fmt.Fprintf(&b, "Recommendation: %s\n", sanitize(item.Recommendation))
	}
	if a, ok := m.agentByID(item.SessionID); ok {
		b.WriteString("\nSession context:\n")
		m.writeHandoverSession(&b, a)
	}
	return b.String()
}

func (m Model) handoverProposal(p protocol.CTOProposal) string {
	var b strings.Builder
	b.WriteString(handoverHeader("a CTO proposal in Needs you"))
	fmt.Fprintf(&b, "Proposal: %s (%s)\n", sanitize(p.ID), sanitize(p.Status))
	fmt.Fprintf(&b, "Summary: %s\n", sanitize(proposalSummary(p)))
	if p.Reason != "" {
		fmt.Fprintf(&b, "CTO's words: %s\n", sanitize(p.Reason))
	}
	if len(p.Args) > 0 && string(p.Args) != "null" {
		fmt.Fprintf(&b, "Arguments: %s\n", sanitize(string(p.Args)))
	}
	return b.String()
}

func (m Model) handoverLearned(v protocol.LearnedSuggestion) string {
	var b strings.Builder
	b.WriteString(handoverHeader("a learned suggestion in Needs you"))
	fmt.Fprintf(&b, "Suggestion: %s\n", sanitize(learnedQuestion(v)))
	fmt.Fprintf(&b, "Node: %s\n", sanitize(v.Node))
	if v.Field != "" {
		fmt.Fprintf(&b, "Field: %s\n", sanitize(v.Field))
	}
	if v.Kind != "" {
		fmt.Fprintf(&b, "Kind: %s\n", sanitize(v.Kind))
	}
	if v.Phrase != "" {
		fmt.Fprintf(&b, "Phrase: %s\n", sanitize(v.Phrase))
	}
	for _, e := range v.Evidence {
		if e.Excerpt != "" {
			fmt.Fprintf(&b, "Evidence: %s\n", sanitize(e.Excerpt))
		}
	}
	return b.String()
}

func (m Model) handoverOutcome(e protocol.OutcomeInboxEntry) string {
	var b strings.Builder
	b.WriteString(handoverHeader("an outcome row in Needs you"))
	fmt.Fprintf(&b, "Edge: %s -> %s\n", sanitize(e.From), sanitize(e.To))
	if e.Request != "" {
		fmt.Fprintf(&b, "Request: %s\n", sanitize(e.Request))
	}
	if e.Outcome != "" {
		fmt.Fprintf(&b, "Outcome: %s\n", sanitize(e.Outcome))
	}
	if e.Reason != "" {
		fmt.Fprintf(&b, "Reason: %s\n", sanitize(e.Reason))
	}
	if e.Recommendation != "" {
		fmt.Fprintf(&b, "Recommendation: %s\n", sanitize(e.Recommendation))
	}
	return b.String()
}

func (m Model) handoverMessage(it *feedItem, msg protocol.Message) string {
	var b strings.Builder
	b.WriteString(handoverHeader("a message in Messages"))
	from := m.handoverName(it.From)
	if it.FromCTO {
		from = m.ctoTitle()
	}
	fmt.Fprintf(&b, "From: %s (%s)\n", sanitize(from), sanitize(it.From))
	fmt.Fprintf(&b, "To: %s (%s)\n", m.handoverName(it.To), sanitize(it.To))
	fmt.Fprintf(&b, "Type: %s, state %s\n", dashIfEmpty(it.Type), dashIfEmpty(it.State))
	if !it.At.IsZero() {
		fmt.Fprintf(&b, "Time: %s\n", it.At.In(m.loc).Format("2006-01-02 15:04:05"))
	}
	if it.ConversationID != "" {
		fmt.Fprintf(&b, "Conversation: %s\n", sanitize(it.ConversationID))
	}
	b.WriteString("Body:\n")
	body := sanitizeBody(msg.Body)
	if strings.TrimSpace(body) == "" {
		body = "(empty message)"
	}
	b.WriteString(body)
	b.WriteString("\n")
	return b.String()
}

// ---- the preview ----

func (m Model) handoverView(w, h int) []string {
	hd := m.handover
	out := []string{section("Hand over to "+m.ctoTitle(), w)}
	out = append(out, sDim.Render(clip("  The text below is sent as you. Enter sends it, Esc cancels.", w)))
	out = append(out, "")
	switch {
	case hd.loading:
		out = append(out, sDim.Render("  fetching the message body..."))
	case hd.err != "":
		for _, line := range wrap(hd.err, w-4) {
			out = append(out, "  "+sBad.Render(line))
		}
	default:
		var body []string
		for _, line := range strings.Split(hd.text, "\n") {
			if line == "" {
				body = append(body, "")
				continue
			}
			body = append(body, wrap(line, w-4)...)
		}
		room := max(h-len(out), 1)
		start := min(hd.scroll, max(len(body)-room, 0))
		for i := start; i < len(body) && i < start+room; i++ {
			out = append(out, clip("  "+body[i], w))
		}
	}
	for len(out) < h {
		out = append(out, "")
	}
	return out[:h]
}
