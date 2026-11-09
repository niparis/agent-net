package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// The chief of staff on the Desk (decision 0114, part 2): the header's chief,
// the list of what the chief handled, the digest after an hour away, and the
// owner's undo and two marks.

// deskAwayAfter is how long the owner must be away before the Desk shows the
// digest of what happened meanwhile.
const deskAwayAfter = time.Hour

// deskDigestLines is how many log lines the digest counts. The gateway keeps
// at most this many in one answer.
const deskDigestLines = 500

// deskDigest is the line "While you were away". It is counted once, when the
// owner comes back, so that later log lines do not change it.
type deskDigest struct {
	since  time.Time
	away   time.Duration
	counts deskCounts
	loaded bool
	failed bool
	hidden bool
}

// deskCounts are the four numbers of the digest.
type deskCounts struct {
	closed, sentOn, answered, forYou int
}

// deskMark is the mark whose optional line of why the owner is typing.
type deskMark struct {
	ask, mark string
}

// deskAskMsg is the answer of an undo or a mark.
type deskAskMsg struct {
	ask  string
	undo bool
	err  error
}

// deskDigestMsg is the log that the digest counts.
type deskDigestMsg struct {
	since   time.Time
	entries []protocol.DeskLogEntry
	err     error
}

// deskDigestStart runs on every key. When the owner was away an hour or more,
// it opens the digest of that time and returns the command that counts it.
// Every key sets the last-input time.
func (m *Model) deskDigestStart() tea.Cmd {
	now := m.now()
	if m.lastInput.IsZero() || now.Sub(m.lastInput) < deskAwayAfter {
		m.lastInput = now
		return nil
	}
	since := m.lastInput
	m.digest = &deskDigest{since: since, away: now.Sub(since)}
	m.lastInput = now
	return m.deskDigestCmd(since)
}

// deskDigestCmd reads the newest log lines for the digest.
func (m Model) deskDigestCmd(since time.Time) tea.Cmd {
	c, ok := m.src.(inboxCaller)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		raw, err := c.Call(ctx, protocol.OpDeskLog, protocol.DeskLogQuery{Limit: deskDigestLines})
		if err != nil {
			return deskDigestMsg{since: since, err: err}
		}
		var res protocol.DeskLogResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return deskDigestMsg{since: since, err: err}
		}
		return deskDigestMsg{since: since, entries: res.Entries}
	}
}

// onDeskDigest counts the digest's lines. A late answer for an older away time
// is dropped.
func (m *Model) onDeskDigest(msg deskDigestMsg) {
	d := m.digest
	if d == nil || !d.since.Equal(msg.since) {
		return
	}
	d.loaded = true
	if msg.err != nil {
		d.failed = true
		return
	}
	d.counts = countDigest(msg.entries, msg.since)
}

// countDigest counts the log lines written since a time, in four groups.
func countDigest(entries []protocol.DeskLogEntry, since time.Time) deskCounts {
	var c deskCounts
	for _, e := range entries {
		if e.At.Before(since) {
			continue
		}
		switch digestGroup(e) {
		case "closed":
			c.closed++
		case "sent on":
			c.sentOn++
		case "answered":
			c.answered++
		case "for you":
			c.forYou++
		}
	}
	return c
}

// digestGroup says which digest count a log line adds to, or "" for none.
// The gateway writes these lines (decision 0114): the chief's sorts, the
// gateway's fallbacks to the owner, and the automatic closes of asks.
func digestGroup(e protocol.DeskLogEntry) string {
	text := e.Text
	switch {
	case e.Source == "chief" && (strings.HasPrefix(text, "Chief closed ask:") || strings.HasPrefix(text, "Chief merged ask into ")):
		return "closed"
	case e.Source == "chief" && strings.HasPrefix(text, "Chief sent ask to you"):
		return "for you"
	case e.Source == "chief" && strings.HasPrefix(text, "Chief sent ask to "):
		return "sent on"
	case e.Source == "gateway" && strings.Contains(text, "so it is with you"):
		return "for you"
	case e.Source == "desk" && strings.HasPrefix(text, "Closed ") && (strings.Contains(text, " (answered): ") || strings.Contains(text, " (owner replied): ")):
		return "answered"
	case e.Source == "desk" && strings.HasPrefix(text, "Closed "):
		return "closed"
	}
	return ""
}

// deskDigestLines is the digest at the top of the Desk, or none when there is
// none. On one line, "x hide" sits at the right. A narrow screen gives the
// counts their own line, so "x hide" never touches them.
func (m Model) deskDigestLines(w int) []string {
	d := m.digest
	if d == nil || d.hidden {
		return nil
	}
	head := sDim.Render(fmt.Sprintf("While you were away (%d h)", int(d.away/time.Hour)))
	var counts string
	switch {
	case d.failed:
		counts = sDim.Render("could not count what happened")
	case !d.loaded:
		counts = sDim.Render("counting...")
	default:
		c := d.counts
		counts = fmt.Sprintf("closed %d · sent on %d · answered %d · ", c.closed, c.sentOn, c.answered) + sNeeds.Render(fmt.Sprintf("%d for you", c.forYou))
	}
	hide := sDim.Render("x hide")
	if room := w - ansi.StringWidth(head) - 2 - ansi.StringWidth(counts) - ansi.StringWidth(hide); room >= 4 {
		return []string{head + "  " + counts + strings.Repeat(" ", room) + hide}
	}
	room := w - ansi.StringWidth(head) - ansi.StringWidth(hide)
	return []string{head + strings.Repeat(" ", max(room, 1)) + hide, clip(counts, w)}
}

// deskSectionCount is how many boxes the Desk draws: Needs you, In flight,
// Handled for you, and the Log when it is open.
func (m Model) deskSectionCount() int {
	if m.deskLogOpen {
		return 4
	}
	return 3
}

// deskChiefText names the chief and says whether it is online.
func (m Model) deskChiefText() string {
	switch {
	case !m.chiefLoaded:
		return sDim.Render("chief of staff")
	case m.chief.SessionID == "":
		return sDim.Render("no chief of staff")
	}
	text := "chief of staff"
	if name := sanitize(m.chief.Name); name != "" {
		text += " " + name
	}
	if m.chief.Online {
		return sDim.Render(text + " on")
	}
	return sBad.Render(text + " off")
}

// deskSelectedHandled is the handled sort under the cursor.
func (m Model) deskSelectedHandled() (protocol.ChiefSort, bool) {
	sec, i := m.deskPosition()
	if sec != deskSecHandled || i >= len(m.handled) {
		return protocol.ChiefSort{}, false
	}
	return m.handled[i], true
}

// handledLabel says what the chief did with an ask, in the owner's words.
func handledLabel(h protocol.ChiefSort) string {
	if h.Undone {
		return "undone"
	}
	switch h.Result {
	case protocol.AskSortClose:
		return "closed"
	case protocol.AskSortMerge:
		return "merged"
	case protocol.AskSortSend:
		return "sent"
	case protocol.AskSortOwner:
		return "to you"
	}
	return h.Result
}

// deskHandledLine is one row of Handled for you: what the chief did, the ask,
// then its reason and the session a send went to. The reason gives way first
// when the row is narrow, and the question keeps at least a readable part of
// its words. The panel shows all of it.
func (m Model) deskHandledLine(h protocol.ChiefSort, w int, selected bool) string {
	marker := "  "
	if selected {
		marker = sAccent.Render("▸") + " "
	}
	avail := max(w-ansi.StringWidth(marker)-9, 4)
	session := ""
	if h.Result == protocol.AskSortSend {
		session = "  → " + m.inboxSessionName(h.To)
	}
	reason := ""
	if h.Reason != "" {
		reason = "  " + sanitize(h.Reason)
	}
	const minQuestion = 24
	if avail-ansi.StringWidth(session)-ansi.StringWidth(reason) < minQuestion {
		reason = ""
	}
	question := cutWords(sanitize(h.Question), max(avail-ansi.StringWidth(session)-ansi.StringWidth(reason), 4))
	line := marker + cell(handledLabel(h), 8) + " " + question
	if h.Undone {
		line = sDim.Render(line + reason + session)
	} else {
		line += sDim.Render(reason + session)
	}
	if selected {
		return selectedRow(padStyled(line, w))
	}
	return clip(line, w)
}

// deskHandledHints is the key line of a handled sort.
func (m Model) deskHandledHints(h protocol.ChiefSort) []hint {
	var out []hint
	if !h.Undone {
		undo := bindingHint(contextInbox, "undo")
		undo.label = "undo the sort"
		out = append(out, undo)
	}
	out = append(out, bindingHint(contextInbox, "should_have_asked"))
	return keepChoices(out)
}

// deskHandledPanel draws the handled sort under the cursor, in full.
func (m Model) deskHandledPanel(w, h int) []string {
	s, ok := m.deskSelectedHandled()
	if !ok || h < 3 {
		return nil
	}
	innerW, innerH := max(w-4, 4), h-2
	var body []string
	body = append(body, wrapText(sanitize(s.Question), innerW)...)
	if s.Reason != "" {
		body = append(body, wrapText("reason: "+sanitize(s.Reason), innerW)...)
	}
	if s.Result == protocol.AskSortSend {
		body = append(body, wrapText("sent to "+m.inboxSessionName(s.To), innerW)...)
	}
	if s.Result == protocol.AskSortOwner && s.WhyYou != "" {
		body = append(body, wrapText("why you: "+sanitize(s.WhyYou), innerW)...)
	}
	body = append(body, wrapText(inboxAgeText(m.now(), s.At.Unix()), innerW)...)
	body = append(body, "")
	body = append(body, hintLines(m.deskHandledHints(s), innerW)...)
	for len(body) < innerH {
		body = append(body, "")
	}
	if len(body) > innerH {
		body = body[:innerH]
	}
	return panelWithBorder("Handled · "+handledLabel(s), body, w, roleAccent, roleFaint)
}

// deskUndo puts a handled ask back the way it was before the chief's sort.
func (m Model) deskAskUndo(ask string) tea.Cmd {
	c, ok := m.src.(inboxCaller)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		_, err := c.Call(ctx, protocol.OpAskUndo, protocol.AskUndoRequest{Ask: ask})
		return deskAskMsg{ask: ask, undo: true, err: err}
	}
}

// deskMarkCmd stores one mark with its optional line of why.
func (m Model) deskMarkCmd(p deskMark, why string) tea.Cmd {
	c, ok := m.src.(inboxCaller)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		_, err := c.Call(ctx, protocol.OpAskMark, protocol.AskMarkRequest{Ask: p.ask, Mark: p.mark, Why: why})
		return deskAskMsg{ask: p.ask, err: err}
	}
}

// deskMarkStart opens the one-line why under a mark.
func (m Model) deskMarkStart(ask, mark string) Model {
	m.deskPending = &deskMark{ask: ask, mark: mark}
	m.inboxInput = newTextInput()
	m.inboxInput.CharLimit = protocol.MaxChiefReasonChars
	m.inboxInput.Prompt = "why (optional)> "
	m.inboxInput.Focus()
	m.inboxEditing = true
	return m
}

// deskMarkKey types the why of a mark. Enter stores it, even when it is empty.
func (m Model) deskMarkKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.Code {
	case tea.KeyEsc:
		m.deskPending, m.inboxEditing = nil, false
		return m, nil
	case tea.KeyEnter:
		p := *m.deskPending
		m.deskPending, m.inboxEditing = nil, false
		return m, m.deskMarkCmd(p, strings.TrimSpace(m.inboxInput.Value()))
	}
	var cmd tea.Cmd
	m.inboxInput, cmd = m.inboxInput.Update(k)
	return m, cmd
}

// onDeskAsk shows the answer of an undo or a mark. An undone sort is marked at
// once; the next refresh brings the lists back from the gateway.
func (m Model) onDeskAsk(msg deskAskMsg) Model {
	if msg.err != nil {
		m.fail(sanitize(msg.err.Error()))
		return m
	}
	m.inboxSeen = ""
	if !msg.undo {
		m.say("Noted. The chief reads it in its next batch.")
		return m
	}
	for i := range m.handled {
		if m.handled[i].Ask == msg.ask {
			m.handled[i].Undone = true
		}
	}
	m.say("Undone. The ask is with you again.")
	return m
}
