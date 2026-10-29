package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
)

// This file holds the data side of the conversation view: what was loaded,
// paging back, following, and the lines the renderer draws. The renderer
// (view_conv.go) only styles them. The phone view will reuse this logic
// (decision 0006, P8).

// convPage is how many entries one request asks for.
const convPage = 50

// convKind says what a fetch is for.
type convKind int

const (
	convFirst convKind = iota // the newest entries, replacing what is there
	convOlder                 // the entries before the oldest one
	convNewer                 // the entries after the newest one (follow)
)

// convState is the conversation of one session.
type convState struct {
	id      string
	entries []protocol.TextEntry // oldest first
	older   string               // cursor to page back; empty at the start
	newer   string               // cursor to follow
	loaded  bool                 // the first page arrived
	// busy is set while a fetch runs, so there is only one at a time.
	busy bool
	err  string
	// back is how many lines the view is scrolled up from the end. 0 means
	// following: new entries stay in view.
	back int
}

// convMsg is the answer to a conversation fetch.
type convMsg struct {
	id   string
	kind convKind
	page protocol.TextPage
	err  error
}

// convCmd fetches one page of the open conversation. It marks the state busy.
func (m *Model) convCmd(kind convKind) tea.Cmd {
	c := m.conv
	if c == nil || c.busy {
		return nil
	}
	q := protocol.TextQuery{SessionID: c.id, Limit: convPage}
	switch kind {
	case convOlder:
		if c.older == "" {
			return nil
		}
		q.Before = c.older
	case convNewer:
		if c.newer == "" {
			kind = convFirst
		} else {
			q.After, q.Limit = c.newer, protocol.TextMaxLimit
		}
	}
	c.busy = true
	src, id := m.src, c.id
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		page, err := src.SessionText(ctx, q)
		return convMsg{id: id, kind: kind, page: page, err: err}
	}
}

// openConv starts the conversation of the selected session.
func (m *Model) openConv() tea.Cmd {
	a, ok := m.selectedAgent()
	if !ok {
		return nil
	}
	m.conv = &convState{id: a.SessionID}
	return m.convCmd(convFirst)
}

// applyConv merges an answer into the open conversation.
func (m *Model) applyConv(r convMsg) {
	c := m.conv
	if c == nil || c.id != r.id {
		return
	}
	c.busy = false
	if r.err != nil {
		c.err = convError(r.err)
		return
	}
	c.err = ""
	p := r.page
	switch r.kind {
	case convFirst:
		// A repeat of the first fetch (follow with no cursor yet) must not
		// drop what is already there.
		if !c.loaded || len(p.Entries) > 0 {
			c.entries = append([]protocol.TextEntry(nil), p.Entries...)
			c.older = p.Older
		}
		c.loaded = true
		if p.Newer != "" {
			c.newer = p.Newer
		}
	case convOlder:
		have := map[string]bool{}
		for _, e := range c.entries {
			have[e.Cursor] = true
		}
		var add []protocol.TextEntry
		for _, e := range p.Entries {
			if !have[e.Cursor] {
				add = append(add, e)
			}
		}
		c.entries = append(add, c.entries...)
		c.older = p.Older
		if len(add) == 0 {
			c.older = "" // nothing new came: stop asking
		}
	case convNewer:
		have := map[string]bool{}
		for _, e := range c.entries {
			have[e.Cursor] = true
		}
		for _, e := range p.Entries {
			if !have[e.Cursor] {
				c.entries = append(c.entries, e)
			}
		}
		if p.Newer != "" {
			c.newer = p.Newer
		}
	}
	// The memory is capped: drop the oldest entries when following for long.
	if over := len(c.entries) - maxConvEntries; over > 0 {
		c.entries = c.entries[over:]
		c.older = "" // the cursor no longer joins up
	}
}

// maxConvEntries caps what the view keeps.
const maxConvEntries = 2000

// convError explains why the conversation is not there.
func convError(err error) string {
	switch ClassifyError(err) {
	case ProblemDaemon:
		return "The daemon cannot be reached."
	case ProblemGateway:
		return "The gateway cannot be reached, so the session on another machine cannot be read."
	}
	return "Could not read the conversation: " + sanitize(err.Error())
}

// convLine is one drawn line of the conversation.
type convLine struct {
	entry int  // index in the entries
	first bool // the first line of its entry: it carries the time and role
	text  string
}

// Widths of the two columns before the text of an entry.
const (
	convTimeW = 8
	convRoleW = 9
)

// convTextWidth is the room for the text of an entry on a w column screen.
func convTextWidth(w int) int { return max(w-2-convTimeW-1-convRoleW-1, 12) }

// convLines wraps every entry to w columns. The text is exactly what the
// daemon sent (control characters removed), with the tool name before it.
func (m Model) convLines(w int) []convLine {
	c := m.conv
	if c == nil {
		return nil
	}
	tw := convTextWidth(w)
	var out []convLine
	for i, e := range c.entries {
		text := sanitizeBody(strings.TrimRight(e.Text, "\n"))
		// The tool name comes before the text, unless the text starts with it.
		if (e.Role == protocol.RoleToolCall || e.Role == protocol.RoleToolResult) && e.Tool != "" && !strings.HasPrefix(text, e.Tool) {
			text = e.Tool + "  " + text
		}
		if e.Cut {
			text += " [cut]"
		}
		first := true
		for _, l := range strings.Split(text, "\n") {
			for _, wl := range wrap(l, tw) {
				out = append(out, convLine{entry: i, first: first, text: wl})
				first = false
			}
		}
	}
	return out
}

// convRows is how many lines of conversation fit on screen.
func (m Model) convRows() int { return max(max(m.height, 10)-3, 1) }

// convScroll moves the view by delta lines (positive is up, towards older
// text). At the top it asks for older entries.
func (m *Model) convScroll(delta int) tea.Cmd {
	c := m.conv
	if c == nil {
		return nil
	}
	total := len(m.convLines(max(m.width, 40)))
	limit := max(total-m.convRows(), 0)
	c.back = min(max(c.back+delta, 0), limit)
	if delta > 0 && c.back >= limit {
		return m.convCmd(convOlder)
	}
	return nil
}

// convClock formats the time of an entry.
func (m Model) convClock(e protocol.TextEntry) string {
	if e.Time <= 0 {
		return "--:--:--"
	}
	return time.UnixMilli(e.Time).In(m.loc).Format("15:04:05")
}
