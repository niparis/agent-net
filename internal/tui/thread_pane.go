package tui

import (
	"image/color"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// threadMsg is one message of the open conversation as the pane draws it. The
// stored page and the live feed both turn into this, so one renderer owns the
// look of a message.
type threadMsg struct {
	id               string
	parent           string
	from             string
	workerSession    string
	fromCTO          bool
	ownerInstruction string
	at               time.Time
	body             string
	// bodyMissing marks a body that is a placeholder such as "loading body",
	// which a reply must not quote.
	bodyMissing bool
	selected    bool
}

// threadBodyIndent puts a body under its header: two columns for the marker
// the header may carry, two more to set the body in.
const threadBodyIndent = "    "

// senderPalette is the colour set for senders in one thread. It uses only
// theme tokens that read on a light and on a dark terminal. The CTO keeps its
// own colour, and error or warning colours stay out: they mean trouble.
var senderPalette = []color.Color{roleAccent, roleMature, roleGood, roleWarning}

// senderColours gives each sender in a thread one colour for the whole thread.
// Senders are taken in id order, not in the order messages arrive, so a
// message that loads late never changes a colour already on screen.
func senderColours(msgs []threadMsg) map[string]color.Color {
	ids := map[string]bool{}
	for _, msg := range msgs {
		if !msg.fromCTO {
			ids[msg.from] = true
		}
	}
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	out := make(map[string]color.Color, len(sorted))
	for i, id := range sorted {
		out[id] = senderPalette[i%len(senderPalette)]
	}
	return out
}

// threadLines draws the messages of one conversation, oldest first. Each has
// a header (day, time, sender), a dim line of the replied-to message and the
// links it names, and the body set in under the header. A blank line separates
// messages, and a rule names the day when it changes.
func (m Model) threadLines(msgs []threadMsg, w int) []string {
	colours := senderColours(msgs)
	byID := map[string]threadMsg{}
	for _, msg := range msgs {
		if msg.id != "" {
			byID[msg.id] = msg
		}
	}
	bodyW := max(w-len(threadBodyIndent), 8)
	var out []string
	for i, msg := range msgs {
		if i > 0 {
			out = append(out, "")
			if !m.sameDay(msgs[i-1].at, msg.at) {
				out = append(out, m.dateRule(msg.at, w))
			}
		}
		out = append(out, m.messageHeader(msg, colours))
		indented := func(style lipgloss.Style, text string) {
			for _, line := range wrap(text, bodyW) {
				out = append(out, threadBodyIndent+style.Render(line))
			}
		}
		if msg.ownerInstruction != "" {
			indented(sDim, "Your instruction: "+msg.ownerInstruction)
		}
		if msg.parent != "" {
			indented(sDim, m.replyMarker(msg.parent, byID, bodyW))
		}
		if links := messageLinks(msg); len(links) > 0 {
			indented(sDim, strings.Join(links, " · "))
		}
		out = append(out, m.messageBody(msg.body, bodyW)...)
	}
	return out
}

// messageHeader is the day and time, then the sender in the thread's colour
// for that sender. The marker keeps the selected message findable.
func (m Model) messageHeader(msg threadMsg, colours map[string]color.Color) string {
	sender := m.conversationName(msg.from)
	if sender == "ended session" && msg.workerSession != "" && msg.from == msg.workerSession {
		sender = "ended worker"
	}
	senderColour := colours[msg.from]
	if msg.fromCTO {
		sender, senderColour = m.ctoTitle(), roleCTO
	}
	if senderColour == nil {
		senderColour = roleText
	}
	lead := "  "
	if msg.selected {
		lead = lipglossAccent("▸ ")
	}
	marker := ""
	if msg.ownerInstruction != "" {
		marker = lipglossAccent("you→ ")
	}
	id := ""
	if msg.id != "" {
		id = sDim.Render(" · " + msg.id)
	}
	return lead + sDim.Render(m.messageStamp(msg.at)+"  ") + marker +
		lipgloss.NewStyle().Foreground(senderColour).Bold(true).Render(sender) + id
}

// messageStamp is the day and time of a header. The day is always there, so a
// header reads on its own when the pane is scrolled away from its rule.
func (m Model) messageStamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.In(m.loc).Format("02 Jan 15:04")
}

// dateRule is the line that names a new day. It carries the year only when
// the day is not in the current year.
func (m Model) dateRule(t time.Time, w int) string {
	t, now := t.In(m.loc), m.now().In(m.loc)
	layout := "Mon 02 Jan"
	if t.Year() != now.Year() {
		layout = "Mon 02 Jan 2006"
	}
	// Set in two columns, where the headers start, so the left edge lines up.
	label := "  ── " + t.Format(layout) + " "
	return sDim.Render(label + strings.Repeat("─", max(w-lipgloss.Width(label), 2)))
}

// replyMarker names what a reply answers: the sender and the first words of
// the message when the thread holds it, otherwise its id. It is one line: the
// quoted words are cut at a word to fit, and the pane has the whole message.
func (m Model) replyMarker(parent string, byID map[string]threadMsg, w int) string {
	p, ok := byID[parent]
	if !ok {
		return "re: " + parent
	}
	who := m.conversationName(p.from)
	if p.fromCTO {
		who = m.ctoTitle()
	}
	if p.bodyMissing {
		return "re: " + who
	}
	lead := "re: " + who + " · "
	return lead + firstWords(p.body, max(w-lipgloss.Width(lead), 12))
}

// firstWords is the start of a body on one line, cut at a word.
func firstWords(body string, limit int) string {
	line := strings.Join(strings.Fields(body), " ")
	r := []rune(line)
	if len(r) <= limit {
		return line
	}
	cut := string(r[:limit])
	if i := strings.LastIndex(cut, " "); i > limit/2 {
		cut = cut[:i]
	}
	return cut + "…"
}

// messageLinks is the PRs, changes and Desk asks the body names, so the body
// does not have to carry them as noise. A body that names none has no line.
func messageLinks(msg threadMsg) []string {
	var out []string
	for _, link := range linksIn(msg.body) {
		out = append(out, linkLabel(link))
	}
	return out
}

// messageBody wraps a body at the pane width and sets it under its header.
// No line is cut with an ellipsis: the pane scrolls instead.
func (m Model) messageBody(body string, w int) []string {
	if text, ok := workerNoticeLine(body); ok {
		style := sBad
		switch {
		case workerNoticeLifecycle(body):
			style = sDim
		case strings.Contains(text, "✓ done"):
			style = sBusy
		}
		var out []string
		for _, line := range wrap(text, w) {
			out = append(out, threadBodyIndent+style.Render(line))
		}
		return out
	}
	if body == "" {
		return []string{threadBodyIndent + sDim.Render(clip("empty message", w))}
	}
	bodyStyle := lipgloss.NewStyle().Foreground(roleText)
	var out []string
	for _, line := range strings.Split(body, "\n") {
		for _, wrapped := range wrap(line, w) {
			out = append(out, threadBodyIndent+bodyStyle.Render(wrapped))
		}
	}
	return out
}
