package tui

import (
	"context"
	"encoding/json"
	"fmt"

	"image/color"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// The Desk (tab 7, decision 0090) is one list in three sections: Needs you
// (the asks), In flight (the delivery changes) and Log (the event log, closed
// until the owner opens it). One cursor walks all three.

const (
	deskSecAsk = iota
	deskSecChange
	deskSecLog
)

// deskLogShown is how many log lines the open Log section lists.
const deskLogShown = 30

// askRowCount is the number of asks that wait for the owner. The tab label,
// the header and the Sessions tab use it.
func (m Model) askRowCount() int { return len(m.inboxRows()) }

func (m Model) deskLogRows() []protocol.DeskLogEntry {
	if !m.deskLogOpen {
		return nil
	}
	return m.deskLog[:min(len(m.deskLog), deskLogShown)]
}

// deskRowCount is every selectable row of the three sections.
func (m Model) deskRowCount() int {
	return m.askRowCount() + len(m.deliveryVisible()) + len(m.deskLogRows())
}

// deskIndex is the cursor, kept inside the list.
func (m Model) deskIndex() int { return min(max(m.inboxIndex, 0), max(m.deskRowCount()-1, 0)) }

// deskPosition says which section the cursor is in and its place there.
func (m Model) deskPosition() (section, i int) {
	idx := m.deskIndex()
	asks := m.askRowCount()
	if idx < asks {
		return deskSecAsk, idx
	}
	idx -= asks
	if changes := len(m.deliveryVisible()); idx < changes {
		return deskSecChange, idx
	} else {
		idx -= changes
	}
	return deskSecLog, idx
}

// deskOnChange tells whether the cursor is on a delivery change, or inside the
// detail of one.
func (m Model) deskOnChange() bool {
	if m.delivery.detail || m.delivery.reporting {
		return true
	}
	sec, _ := m.deskPosition()
	return sec == deskSecChange && m.deskRowCount() > 0
}

func (m Model) deskSelectedLog() (protocol.DeskLogEntry, bool) {
	sec, i := m.deskPosition()
	rows := m.deskLogRows()
	if sec != deskSecLog || i >= len(rows) {
		return protocol.DeskLogEntry{}, false
	}
	return rows[i], true
}

// deskAsksAbout lists the open asks that are about one change.
func (m Model) deskAsksAbout(change string) []protocol.Ask {
	var out []protocol.Ask
	for _, a := range m.asks {
		if a.AboutKind == protocol.AskAboutChange && a.AboutID == change && a.State != protocol.AskWithSomeone {
			out = append(out, a)
		}
	}
	return out
}

// deskLogLine is one line of the open Log section.
func (m Model) deskLogLine(e protocol.DeskLogEntry, nameW, w int, selected bool) string {
	marker := "  "
	if selected {
		marker = sAccent.Render("▸") + " "
	}
	age := sDim.Render(cellR(needsAge(m.now(), e.At.Unix()), 4))
	room := max(w-ansi.StringWidth(marker)-nameW-1-4-1, 4)
	line := marker + sDim.Render(cell(sanitize(e.Source), nameW)) + " " + padStyled(cutWords(sanitize(e.Text), room), room) + " " + age
	if selected {
		return selectedRow(padStyled(line, w))
	}
	return clip(line, w)
}

// deskSection is one box of the Desk before it is fitted to the screen. sel is
// the body line of the cursor, or -1 when the cursor is elsewhere.
type deskSection struct {
	title string
	color color.Color
	body  []string
	sel   int
}

// fitBody keeps at most room lines of a body. It windows around the cursor and
// says how many rows it left out, so a box is never cut in the middle.
func fitBody(body []string, sel, room int) ([]string, int) {
	if room < 1 {
		room = 1
	}
	if len(body) <= room {
		return body, 0
	}
	if room == 1 {
		if sel >= 0 {
			return []string{body[sel]}, sel
		}
		return []string{sDim.Render(fmt.Sprintf("… %d more", len(body)))}, 0
	}
	keep := room - 1
	top := 0
	if sel >= keep {
		top = sel - keep + 1
	}
	top = min(max(top, 0), len(body)-keep)
	out := append([]string{}, body[top:top+keep]...)
	return append(out, sDim.Render(fmt.Sprintf("… %d more", len(body)-keep))), top
}

// deskBoxes draws the sections at width w in at most room lines (room <= 0
// means no limit) and returns the line of the selected row. A section that
// does not hold the cursor is cut first.
func (m Model) deskBoxes(w, room int) ([]string, int) {
	rows := m.inboxRows()
	changes := m.deliveryVisible()
	logRows := m.deskLogRows()
	sec, at := m.deskPosition()
	innerW := max(w-4, 4)
	var sections []deskSection

	nameW := 12
	for _, row := range rows {
		nameW = max(nameW, ansi.StringWidth(sanitize(row.name)))
	}
	nameW = min(nameW, 20)
	needs := deskSection{title: fmt.Sprintf("Needs you · %d", len(rows)), color: roleWarning, sel: -1}
	if len(rows) == 0 {
		needs.body = []string{sDim.Render("Nothing is waiting for you.")}
	}
	for i, row := range rows {
		if sec == deskSecAsk && i == at {
			needs.sel = len(needs.body)
		}
		needs.body = append(needs.body, m.inboxRowLine(row, nameW, innerW, sec == deskSecAsk && i == at))
	}
	sections = append(sections, needs)

	d := m.delivery
	flight := deskSection{title: fmt.Sprintf("In flight · %d", len(changes)), color: roleAccent, sel: -1}
	if d.err != "" {
		for _, l := range wrap("Could not load changes: "+sanitize(d.err), innerW) {
			flight.body = append(flight.body, sBad.Render(l))
		}
	}
	switch {
	case len(changes) > 0:
		cols := m.deliveryLayout(innerW, changes)
		flight.body = append(flight.body, cols.header())
		for i, ch := range changes {
			if sec == deskSecChange && i == at {
				flight.sel = len(flight.body)
			}
			flight.body = append(flight.body, m.deliveryRow(cols, ch, sec == deskSecChange && i == at))
			if finding := deliveryRowFinding(ch); finding != "" {
				flight.body = append(flight.body, sDim.Render("    "+clip(sanitize(finding), max(innerW-4, 1))))
			}
			for _, a := range m.deskAsksAbout(ch.ID) {
				flight.body = append(flight.body, sNeeds.Render("    asks you: "+clip(sanitize(inboxFirstLine(a.Question)), max(innerW-15, 1))))
			}
		}
		if done, stale := m.deliveryHidden(); !d.showAll && done+stale > 0 {
			flight.body = append(flight.body, sDim.Render(deliveryHiddenText(done, stale)))
		}
	case d.err != "":
	case m.deliverySrc != nil && !d.loaded:
		flight.body = append(flight.body, sDim.Render("Loading changes..."))
	case len(d.changes) > 0:
		done, stale := m.deliveryHidden()
		flight.body = append(flight.body, "Nothing needs attention.", sDim.Render(deliveryHiddenText(done, stale)))
	default:
		flight.body = append(flight.body, sDim.Render("No open changes."))
	}
	sections = append(sections, flight)

	if m.deskLogOpen {
		logName := 8
		for _, e := range logRows {
			logName = max(logName, ansi.StringWidth(sanitize(e.Source)))
		}
		logName = min(logName, 12)
		logSec := deskSection{title: fmt.Sprintf("Log · %d", len(logRows)), color: roleAccent, sel: -1}
		for i, e := range logRows {
			if sec == deskSecLog && i == at {
				logSec.sel = len(logSec.body)
			}
			logSec.body = append(logSec.body, m.deskLogLine(e, logName, innerW, sec == deskSecLog && i == at))
		}
		if len(logSec.body) == 0 {
			logSec.body = []string{sDim.Render("Nothing logged yet.")}
		}
		sections = append(sections, logSec)
	}

	// Fit the sections to the room. The section with the cursor is cut last:
	// the others first shrink to a few lines, then to one.
	limits := make([]int, len(sections))
	total := 0
	for i, sc := range sections {
		limits[i] = len(sc.body)
		total += limits[i] + 2
	}
	if room > 0 {
		for _, cap := range []int{4, 2, 1} {
			if total <= room {
				break
			}
			for i, sc := range sections {
				if sc.sel < 0 && limits[i] > cap {
					total -= limits[i] - cap
					limits[i] = cap
				}
			}
		}
		for i, sc := range sections {
			if total <= room {
				break
			}
			if sc.sel >= 0 {
				cut := min(total-room, max(limits[i]-1, 0))
				limits[i] -= cut
				total -= cut
			}
		}
	}
	var out []string
	selected := 0
	for i, sc := range sections {
		body, top := fitBody(sc.body, sc.sel, limits[i])
		if sc.sel >= 0 {
			selected = len(out) + 1 + sc.sel - top
		}
		out = append(out, panelWithBorder(sc.title, body, w, sc.color, roleFaint)...)
	}
	return out, selected
}

// deskHeaderLine is the Desk's own title line.
func (m Model) deskHeaderLine(w int) string {
	line := sBold.Render("Desk")
	if n := m.askRowCount(); n > 0 {
		line += sDim.Render(" · ") + sNeeds.Render(fmt.Sprintf("%d need you", n))
	} else {
		line += sDim.Render(" · all clear")
	}
	if m.delivery.project != "" {
		line += sDim.Render(" · changes of " + sanitize(m.delivery.project) + " (esc shows all)")
	}
	if !m.deskLogOpen {
		line += sDim.Render(" · log closed (l opens it)")
	}
	return clip(line, w)
}

// deskPanel draws what the cursor is on, in full.
func (m Model) deskPanel(w, h int) []string {
	if h < 3 {
		return nil
	}
	sec, i := m.deskPosition()
	innerW, innerH := max(w-4, 4), h-2
	var title string
	var body []string
	switch sec {
	case deskSecAsk:
		return m.inboxPanel(w, h)
	case deskSecChange:
		changes := m.deliveryVisible()
		if i >= len(changes) {
			return nil
		}
		ch := changes[i]
		title = "Change " + sanitize(ch.ID)
		status, _ := m.deliveryStatusText(ch)
		body = append(body, wrapText(deliveryTitle(ch), innerW)...)
		body = append(body, wrapText("step "+firstNonEmpty(ch.Step, "-")+": "+status, innerW)...)
		if who := m.deliveryWho(ch); who != "" {
			body = append(body, wrapText("with "+who, innerW)...)
		}
		if finding := deliveryRowFinding(ch); finding != "" {
			body = append(body, wrapText(finding, innerW)...)
		}
		for _, a := range m.deskAsksAbout(ch.ID) {
			body = append(body, wrapText("Asks you: "+inboxFirstLine(a.Question), innerW)...)
		}
		body = append(body, "")
		body = append(body, hintLines(m.deskChangeHints(ch), innerW)...)
	default:
		e, ok := m.deskSelectedLog()
		if !ok {
			return nil
		}
		title = "Log · " + sanitize(e.Source)
		body = append(body, wrapText(e.Text, innerW)...)
		if e.Detail != "" && e.Source != "apps" {
			body = append(body, wrapText(e.Detail, innerW)...)
		}
		body = append(body, wrapText(inboxAgeText(m.now(), e.At.Unix()), innerW)...)
		body = append(body, "")
		body = append(body, hintLines(m.deskLogHints(e), innerW)...)
	}
	for len(body) < innerH {
		body = append(body, "")
	}
	if len(body) > innerH {
		body = body[:innerH]
	}
	return panelWithBorder(title, body, w, roleAccent, roleFaint)
}

func (m Model) deskChangeHints(ch deliveryChange) []hint {
	h := []hint{bindingHint(contextDelivery, "open"), bindingHint(contextInbox, "show all"), bindingHint(contextDelivery, "refresh")}
	if ch.Group == groupToTry {
		h = append(h, hint{key: "y", label: "it works", prio: 1}, hint{key: "n", label: "it fails", prio: 1})
	}
	return h
}

func (m Model) deskLogHints(e protocol.DeskLogEntry) []hint {
	h := []hint{bindingHint(contextInbox, "log")}
	if _, _, ok := logUndo(e); ok {
		h = append([]hint{bindingHint(contextInbox, "undo")}, h...)
	}
	return h
}

// inboxTabView draws the Desk: a title, the three sections and a panel for
// what the cursor is on. On a wide screen the panel sits on the right; on a
// narrow one it sits under the sections.
func (m Model) inboxTabView(w, h int) []string {
	if m.delivery.detail {
		return m.deliveryView(w, h)
	}
	out := []string{m.titleLine(w), m.deskHeaderLine(w)}
	if m.inboxError != "" {
		out = append(out, sBad.Render(clip("  "+m.inboxError, w)))
	}
	if m.inboxNote != "" {
		out = append(out, sDim.Render(clip("  "+m.inboxNote, w)))
	}
	if m.delivery.reporting {
		out = append(out, sDim.Render(clip("  say what is wrong with the change below", w)))
	}
	if m.inboxDetail {
		rows := m.inboxRows()
		out = append(out, section("Inbox detail", w))
		row, _ := m.inboxSelected(rows)
		var lines []string
		for _, line := range m.inboxDetailLines(row) {
			if line == "" {
				lines = append(lines, "")
				continue
			}
			for _, wrapped := range wrapText(line, max(w-2, 1)) {
				lines = append(lines, "  "+wrapped)
			}
		}
		room := max(h-len(out), 0)
		start := min(max(m.inboxDetailScroll, 0), max(len(lines)-room, 0))
		more := len(lines)-start > room
		if more && room > 0 {
			room--
		}
		out = append(out, lines[start:min(start+room, len(lines))]...)
		if more && len(out) < h {
			out = append(out, sDim.Render("  more below"))
		}
		for len(out) < h {
			out = append(out, "")
		}
		return out[:max(h, 1)]
	}

	avail := max(h-len(out), 1)
	if w >= 100 {
		panelW := min(max(w*2/5, 30), 48)
		leftW := max(w-panelW-1, 20)
		panelW = w - leftW - 1
		display, selected := m.deskBoxes(leftW, avail)
		panelH := min(avail, max(len(display), 12))
		left := windowLines(display, selected, panelH)
		panel := m.deskPanel(panelW, panelH)
		for i := 0; i < panelH; i++ {
			var l, r string
			if i < len(left) {
				l = left[i]
			}
			if i < len(panel) {
				r = panel[i]
			}
			out = append(out, padStyled(l, leftW)+" "+padStyled(r, panelW))
		}
		for len(out) < h {
			out = append(out, "")
		}
		return out[:max(h, 1)]
	}

	panelH := 0
	if avail >= 12 {
		panelH = min(max(avail/3, 4), 8)
	}
	boxRoom := avail - panelH
	display, selected := m.deskBoxes(w, boxRoom)
	out = append(out, windowLines(display, selected, boxRoom)...)
	if panelH > 0 {
		out = append(out, m.deskPanel(w, panelH)...)
	}
	for len(out) < h {
		out = append(out, "")
	}
	return out[:max(h, 1)]
}

// deskLogCmd reads the newest log lines.
func (m Model) deskLogCmd() tea.Cmd {
	c, ok := m.src.(inboxCaller)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		raw, err := c.Call(ctx, protocol.OpDeskLog, protocol.DeskLogQuery{Limit: deskLogShown})
		if err != nil {
			return deskLogMsg{err: err}
		}
		var res protocol.DeskLogResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return deskLogMsg{err: err}
		}
		return deskLogMsg{entries: res.Entries}
	}
}

type deskLogMsg struct {
	entries []protocol.DeskLogEntry
	err     error
}

// logUndo accepts only the owner operations that the CTO daemon wrote into a
// log line.
func logUndo(e protocol.DeskLogEntry) (string, json.RawMessage, bool) {
	if e.Source != "cto" || len(e.Options) != 2 {
		return "", nil, false
	}
	if _, ok := protocol.CTOOperatorMethods()[e.Options[0]]; ok {
		raw := json.RawMessage(e.Options[1])
		return e.Options[0], raw, json.Valid(raw)
	}
	switch e.Options[0] {
	case "apps.rename", "apps.grant", "apps.ungrant", "apps.public.on", "apps.public.off", "graph_undo", "settings_undo", "session_rename", "session_trust", "node_rename", "project_display_set", "worker_profile_add", "worker_profile_remove", "worker_providers_set", "protected_providers_set", "scores_set", "integrations_set", "playbook_set", "mcp_remove", "mcp_enable", "mcp_native_remove", "tools_undo", "tools_new_projects", "sessions_archive", "folder_trust", "folder_mcp", "standing_orders", "session_resume":
	default:
		return "", nil, false
	}
	raw := json.RawMessage(e.Options[1])
	if !json.Valid(raw) {
		return "", nil, false
	}
	return e.Options[0], raw, true
}

// deskUndo runs the undo of a log line, then marks the line as undone so that
// it cannot be undone twice.
func (m Model) deskUndo(e protocol.DeskLogEntry) tea.Cmd {
	op, args, ok := logUndo(e)
	c, can := m.src.(inboxCaller)
	if !ok || !can {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		result, err := c.Call(ctx, op, args)
		if err != nil {
			return actionMsg{err: err}
		}
		if op == "mcp_native_remove" {
			var restored protocol.MCPNativeRemoveResult
			if json.Unmarshal(result, &restored) != nil || restored.State != protocol.MCPNativeRestored {
				reason := restored.Reason
				if reason == "" {
					reason = "native MCP configuration was not restored"
				}
				return actionMsg{err: fmt.Errorf("undo: %s", reason)}
			}
		}
		if _, err := c.Call(ctx, protocol.OpDeskSettle, map[string]int64{"id": e.ID}); err != nil {
			return actionMsg{err: err}
		}
		return deskUndoneMsg{id: e.ID}
	}
}

type deskUndoneMsg struct{ id int64 }

// deskKey handles the keys of a row that is not an ask: a delivery change or
// a log line. It reports false when the key is not for them.
func (m Model) deskRowKey(k tea.KeyPressMsg, key actionID) (Model, tea.Cmd, bool) {
	sec, i := m.deskPosition()
	switch sec {
	case deskSecChange:
		m.delivery.row = i
		mm, cmd := m.deliveryKey(k)
		mm.inboxIndex = min(max(mm.inboxIndex, 0), max(mm.deskRowCount()-1, 0))
		return mm, cmd, true
	case deskSecLog:
		if e, ok := m.deskSelectedLog(); ok && key == "undo" {
			return m, m.deskUndo(e), true
		}
		return m, nil, true
	}
	return m, nil, false
}
