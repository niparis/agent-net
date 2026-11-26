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
// the digest after an hour away, and the owner's marks.

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
	// answer is set for a rule line: the option it answers (decision 0150).
	answer string
}

// deskRuleMark is the mark of a pending rule line.
const deskRuleMark = "rule"

// deskAskMsg is the answer of a mark.
type deskAskMsg struct {
	ask string
	err error
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

// deskSectionCount is how many boxes the Desk draws: Needs you, and the Log
// when it is open.
func (m Model) deskSectionCount() int {
	if m.deskLogOpen {
		return 2
	}
	return 1
}

// deskChiefText names the chief and says whether it is online.
func (m Model) deskChiefText() string {
	switch {
	case !m.chiefLoaded:
		return sDim.Render("chief of staff")
	case m.chief.SessionID == "":
		return sDim.Render("no chief of staff")
	}
	if m.chief.Down != "" {
		return sBad.Render(protocol.ChiefOfflineLine(sanitize(m.chief.Down)))
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

// deskRuleStart opens the line of a standing rule for the marked answer,
// filled with the ask's repo as its area.
func (m Model) deskRuleStart(ask, answer, repo string) Model {
	m.deskPending = &deskMark{ask: ask, mark: deskRuleMark, answer: answer}
	m.inboxInput = newTextInput()
	m.inboxInput.CharLimit = protocol.MaxChiefReasonChars
	m.inboxInput.Prompt = "always " + answer + " for> "
	area := protocol.RuleAreaNetwork
	if repo = strings.TrimSpace(repo); repo != "" {
		area = protocol.RuleAreaRepo + repo
	}
	m.inboxInput.SetValue(" in " + area)
	m.inboxInput.CursorStart()
	m.inboxInput.Focus()
	m.inboxEditing = true
	return m
}

// parseRuleLine reads "<action> in <area>".
func parseRuleLine(s string) (protocol.RuleScope, bool) {
	i := strings.LastIndex(s, " in ")
	if i < 0 {
		return protocol.RuleScope{}, false
	}
	scope := protocol.RuleScope{Action: strings.TrimSpace(s[:i]), Area: strings.TrimSpace(s[i+4:])}
	return scope, scope.Action != "" && scope.Area != ""
}

// inboxAnswerScoped answers with a scope: the gateway also keeps a rule.
func (m Model) inboxAnswerScoped(id, answer string, scope protocol.RuleScope) tea.Cmd {
	c, ok := m.src.(inboxCaller)
	return func() tea.Msg {
		if !ok {
			return inboxAnswerMsg{id, fmt.Errorf("inbox unavailable")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		_, err := c.Call(ctx, "inbox_answer", map[string]any{"id": id, "answer": answer, "scope": scope})
		return inboxAnswerMsg{id, err}
	}
}

// deskMarkKey types the why of a mark. Enter stores it, even when it is empty.
func (m Model) deskMarkKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.Code {
	case tea.KeyEsc:
		m.deskPending, m.inboxEditing = nil, false
		return m, nil
	case tea.KeyEnter:
		p := *m.deskPending
		value := strings.TrimSpace(m.inboxInput.Value())
		if p.mark == deskRuleMark {
			scope, ok := parseRuleLine(value)
			if !ok {
				m.fail("Type the action and its area: merge in repo:<name>, team:<name> or network.")
				return m, nil
			}
			m.deskPending, m.inboxEditing = nil, false
			return m, m.inboxAnswerScoped(p.ask, p.answer, scope)
		}
		m.deskPending, m.inboxEditing = nil, false
		return m, m.deskMarkCmd(p, value)
	}
	var cmd tea.Cmd
	m.inboxInput, cmd = m.inboxInput.Update(k)
	return m, cmd
}

// onDeskAsk shows the answer of a mark. The next refresh brings the lists back
// from the gateway.
func (m Model) onDeskAsk(msg deskAskMsg) Model {
	if msg.err != nil {
		m.failErr(msg.err, sanitize(msg.err.Error()))
		return m
	}
	m.inboxSeen = ""
	m.say("Noted. The chief reads it in its next batch.")
	return m
}
