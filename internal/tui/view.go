package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// ---- styles ----
//
// Colours are adaptive: each has a value for dark and for light terminals.
// Everything also reads without colour: the selection has a marker, offline
// rows say "off", and the banner starts with "!".

var (
	sBold   = lipgloss.NewStyle().Bold(true)
	sDim    = lipgloss.NewStyle().Faint(true)
	sSel    = lipgloss.NewStyle().Reverse(true)
	sTitle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "25", Dark: "81"})
	sBusy   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "130", Dark: "214"})
	sIdle   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "28", Dark: "78"})
	sShell  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "25", Dark: "75"})
	sBad    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "160", Dark: "203"})
	sBanner = lipgloss.NewStyle().Bold(true).Reverse(true).Foreground(lipgloss.AdaptiveColor{Light: "160", Dark: "203"})
)

// ---- text helpers ----

// sanitize makes a string from the network safe for one line of the terminal:
// no control characters (so no escape sequences), no line breaks, no
// characters that reorder text.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r) && (r >= 0x200b && r <= 0x200f || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069):
			return -1
		}
		return r
	}, s)
}

// sanitizeBody is sanitize for a message body: line breaks stay.
func sanitizeBody(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = sanitize(l)
	}
	return strings.Join(lines, "\n")
}

// cell fits s into exactly w columns: cut with an ellipsis, or padded.
func cell(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = sanitize(s)
	if ansi.StringWidth(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	if pad := w - ansi.StringWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// clip cuts a line to w columns without padding.
func clip(s string, w int) string {
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

func dashIfEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// shortModel shortens a model name for the table: no provider, no "claude-"
// prefix. The detail pane has the full name.
func shortModel(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 && i < len(s)-1 {
		s = s[i+1:]
	}
	return strings.TrimPrefix(s, "claude-")
}

func modeShort(mode string) string {
	switch mode {
	case protocol.ModeForeground:
		return "fg"
	case protocol.ModeBackground:
		return "bg"
	case protocol.ModeUnknown:
		return "?"
	}
	return "-" // an older daemon does not say
}

func statusShort(s string) string {
	switch s {
	case "idle", "busy", "shell":
		return s
	case protocol.StatusDormant:
		return "dorm"
	case "":
		return "-"
	}
	return "?"
}

func statusStyle(s string) lipgloss.Style {
	switch s {
	case "busy":
		return sBusy
	case "idle":
		return sIdle
	case "shell":
		return sShell
	}
	return sDim
}

func stateStyle(s string) lipgloss.Style {
	switch s {
	case protocol.StateDelivered, protocol.StateReplied:
		return sIdle
	case protocol.StateFailed, protocol.StateExpired, "refused":
		return sBad
	}
	return sBusy
}

// clock formats a time for the screen: the hour and minute today, the day and
// month otherwise.
func (m Model) clock(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	t, now := t.In(m.loc), m.now().In(m.loc)
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	return t.Format("02 Jan")
}

// shortID shortens a session ID for a line that has no room for 18 characters.
func shortID(id string) string {
	if len(id) > 10 {
		return id[:10]
	}
	return id
}

// ---- layout ----

// ---- machines ----

type machineRow struct {
	nodeID  string
	host    string
	version string
	online  bool
	// harnesses: nil means the machine did not report (older agent-net).
	harnesses []protocol.HarnessInfo
	known     bool // from the node list; false when only agents told us
}

// machineRows lists the machines: the node list, plus machines that only show
// up in the agents (if the node list is missing).
func (m Model) machineRows() []machineRow {
	by := map[string]*machineRow{}
	var rows []*machineRow
	for _, n := range m.snap.Nodes {
		r := &machineRow{nodeID: n.NodeID, host: n.Hostname, version: n.Version, online: n.Online, harnesses: n.Harnesses, known: true}
		by[n.NodeID] = r
		rows = append(rows, r)
	}
	for _, a := range m.snap.Agents {
		r := by[a.NodeID]
		if r == nil {
			r = &machineRow{nodeID: a.NodeID, host: a.Hostname}
			by[a.NodeID] = r
			rows = append(rows, r)
		}
		if !r.known && a.Online {
			r.online = true
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if hi, hj := rows[i].nodeID == m.snap.NodeID, rows[j].nodeID == m.snap.NodeID; hi != hj {
			return hi // this machine first
		}
		a, b := strings.ToLower(rows[i].host), strings.ToLower(rows[j].host)
		if a != b {
			return a < b
		}
		return rows[i].nodeID < rows[j].nodeID
	})
	out := make([]machineRow, len(rows))
	for i, r := range rows {
		out[i] = *r
	}
	return out
}

// numberOf finds the version number in what `<program> --version` printed,
// such as "2.1.312 (Claude Code)" or "opencode v2.2.0": the first word that
// starts with a digit, after an optional "v".
func numberOf(s string) string {
	for _, f := range strings.Fields(s) {
		f = strings.TrimPrefix(f, "v")
		if f != "" && f[0] >= '0' && f[0] <= '9' {
			return f
		}
	}
	return ""
}

func harnessList(hs []protocol.HarnessInfo) string {
	if hs == nil {
		return "harnesses unknown"
	}
	if len(hs) == 0 {
		return "no harnesses"
	}
	var parts []string
	for _, h := range hs {
		if v := numberOf(h.Version); v != "" {
			parts = append(parts, h.Name+" "+v)
		} else {
			parts = append(parts, h.Name)
		}
	}
	return strings.Join(parts, "  ")
}

// ---- view ----

func section(title string, w int) string {
	head := title + " "
	rule := strings.Repeat("─", max(w-ansi.StringWidth(head), 0))
	return sBold.Render(head) + sDim.Render(rule)
}

func (m Model) bannerLine(w int) string {
	const tail = " Showing last data. Retrying."
	var s string
	switch m.problem {
	case ProblemDaemon:
		// The text says which socket; it is left out when the line is too narrow.
		s = "! Daemon not reachable (" + sanitize(socketOf(m.problemT)) + ")." + tail
		if ansi.StringWidth(s) > w {
			s = "! Daemon not reachable." + tail
		}
	case ProblemGateway:
		s = "! Gateway offline: the daemon cannot reach it." + tail
	default:
		s = "! " + sanitize(m.problemT) + "." + tail
	}
	return sBanner.Render(cell(s, w))
}

// socketOf pulls the socket path out of "daemon not reachable (PATH)".
func socketOf(s string) string {
	if i := strings.Index(s, "("); i >= 0 {
		return strings.TrimSuffix(s[i+1:], ")")
	}
	return s
}

// stripStyles removes the colour codes from a line, so a whole-line style
// (reverse for the selection, faint for offline) is not cut by inner codes.
func stripStyles(s string) string { return ansi.Strip(s) }

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// ---- detail ----

// projectSource says where the project of a session is known from: registered
// (someone added it with agent-net projects add), seen (a session ran there)
// or both. It is empty when the project list has no entry for the session's
// repository on its machine, or the gateway is too old to say.
func (m Model) projectSource(a protocol.AgentView) string {
	root := firstNonEmpty(a.Repo.Root, a.Cwd)
	for _, p := range m.snap.Projects {
		if p.NodeID == a.NodeID && p.Path == root {
			if p.Source == "" {
				return protocol.SourceSeen
			}
			return sanitize(p.Source)
		}
	}
	return ""
}

// sessionMessageLine describes one message of a session: time, direction, the
// other end, type and state.
func (m Model) sessionMessageLine(self string, it *feedItem) string {
	arrow, peer := "->", it.To
	if it.To == self {
		arrow, peer = "<-", it.From
	}
	return fmt.Sprintf("%s  %s %s  %s  %s", m.clock(it.At), arrow, m.who(peer), dashIfEmpty(it.Type), it.State)
}

// who names a session for a message line: its name and machine, or its ID
// when the session is gone.
func (m Model) who(id string) string {
	if id == "" {
		return "?"
	}
	if a, ok := m.agentByID(id); ok {
		if protocol.IsOwner(a.Harness) {
			return dashIfEmpty(a.Profile.Name) // already "you (on host)"
		}
		return fmt.Sprintf("%s (%s)", dashIfEmpty(a.Profile.Name), m.nodeName(a.NodeID, a.Hostname))
	}
	return shortID(id)
}

// ---- feed ----

func (m Model) feedHeading(w int) string {
	title := "Messages (live)"
	if m.focus == focusFeed {
		title = "Messages (live, enter opens one)"
	}
	return section(title, w)
}

// feedWindow returns the first item to show: the newest items fill the pane,
// unless the cursor (feed focus) is further back.
func (m Model) feedWindow(rows int) int {
	n := len(m.feed.items)
	top := max(n-rows, 0)
	if m.focus == focusFeed && m.feedCur >= 0 && m.feedCur < top {
		top = m.feedCur
	}
	return top
}

func (m Model) feedLines(w, n int) []string {
	var out []string
	switch {
	case m.feed.unavailable != "":
		out = append(out, sDim.Render(clip("  "+m.feed.unavailable, w)))
	case len(m.feed.items) == 0:
		out = append(out, sDim.Render("  no messages yet"))
	default:
		top := m.feedWindow(n)
		for i := top; i < len(m.feed.items) && len(out) < n; i++ {
			out = append(out, m.feedLine(i, w))
		}
	}
	for len(out) < n {
		out = append(out, "")
	}
	return out
}

func (m Model) feedLine(i, w int) string {
	it := m.feed.items[i]
	marker := "  "
	selected := m.focus == focusFeed && i == m.feedCur
	if selected {
		marker = "> "
	}
	at := cell(m.clock(it.At), 6)
	var line string
	if it.isMessage() {
		const typeW, stateW = 12, 10
		leftW := min(max(w-2-6-1-typeW-1-stateW-1, 10), 64)
		half := (leftW - 4) / 2
		from, to := m.who(it.From), m.who(it.To)
		// Give the longer name the room the shorter one does not use.
		fw, tw := half, leftW-4-half
		if lf := ansi.StringWidth(from); lf < fw {
			fw, tw = lf, leftW-4-lf
		} else if lt := ansi.StringWidth(to); lt < tw {
			fw, tw = leftW-4-lt, lt
		}
		ends := cell(from, fw) + " -> " + cell(to, tw)
		state := cell(it.State, stateW)
		if !selected {
			state = stateStyle(it.State).Render(state)
		}
		line = marker + at + " " + ends + " " + cell(dashIfEmpty(it.Type), typeW) + " " + state
		line = clip(line, w)
	} else {
		line = marker + at + " " + clip(m.infoText(it), max(w-2-6-1, 0))
	}
	if selected {
		return sSel.Render(clip(stripStyles(line), w))
	}
	return clip(line, w)
}

// infoText describes an event that is not a message.
func (m Model) infoText(it *feedItem) string {
	host := dashIfEmpty(it.Host)
	switch it.Kind {
	case "node.connected":
		return sanitize(host) + " connected"
	case "node.disconnected":
		return sanitize(host) + " disconnected"
	case "node.enrolled":
		return "machine " + sanitize(it.Detail) + " joined"
	case "session.new":
		d := parseDetail(it.Detail)
		return fmt.Sprintf("new session %s (%s) on %s", sanitize(dashIfEmpty(d["name"])), sanitize(dashIfEmpty(d["harness"])), sanitize(host))
	case "msg.refused":
		return "send refused from " + m.who(it.From) + ": " + sanitize(it.Detail)
	case "worker.started":
		d := parseDetail(it.Detail)
		return fmt.Sprintf("worker started: %s %s in %s for %s", m.who(it.Session), sanitize(dashIfEmpty(d["harness"])),
			sanitize(dashIfEmpty(d["project"])), m.who(d["owner"]))
	case "worker.stopped":
		d := parseDetail(it.Detail)
		return fmt.Sprintf("worker stopped: %s (%s)", m.who(it.Session), sanitize(strings.ReplaceAll(dashIfEmpty(d["reason"]), "_", " ")))
	case "worker.refused":
		d := parseDetail(it.Detail)
		return fmt.Sprintf("worker refused in %s: %s", sanitize(dashIfEmpty(d["project"])), sanitize(detailReason(it.Detail)))
	}
	return sanitize(it.Kind)
}

// ---- message body ----

func (m Model) messageView(w, rows int) []string {
	o := m.open
	out := []string{section("Message "+sanitize(o.id), w)}
	body := func(lines ...string) {
		for _, l := range lines {
			out = append(out, clip(l, w))
		}
	}
	it := m.feed.byMsg[o.id]
	if it != nil {
		body(fmt.Sprintf("  %s -> %s   %s   %s   %s", m.who(it.From), m.who(it.To), dashIfEmpty(it.Type), it.State, m.clock(it.At)))
	}
	switch {
	case o.loading:
		body(sDim.Render("  fetching..."))
	case o.err != "":
		for _, l := range wrap(o.err, w-4) {
			body("  " + sBad.Render(l))
		}
	default:
		body(sDim.Render(fmt.Sprintf("  sent %s, conversation %s, hop %d", o.msg.CreatedAt.In(m.loc).Format("2006-01-02 15:04:05"),
			sanitize(o.msg.ConversationID), o.msg.Hop)))
		var lines []string
		for _, l := range strings.Split(sanitizeBody(o.msg.Body), "\n") {
			lines = append(lines, wrap(l, w-4)...)
		}
		room := max(rows-len(out), 1)
		start := min(o.scroll, max(len(lines)-room, 0))
		for i := start; i < len(lines) && i < start+room; i++ {
			body("  " + lines[i])
		}
		if len(lines) > room {
			out[len(out)-1] = clip(out[len(out)-1], w)
		}
	}
	for len(out) < rows {
		out = append(out, "")
	}
	return out[:rows]
}

// wrap breaks a line at spaces to fit w columns; a word longer than w is cut.
func wrap(s string, w int) []string {
	if w < 8 {
		w = 8
	}
	if s == "" {
		return []string{""}
	}
	var out []string
	cur := ""
	flush := func() {
		out = append(out, cur)
		cur = ""
	}
	for _, word := range strings.Split(s, " ") {
		for ansi.StringWidth(word) > w {
			if cur != "" {
				flush()
			}
			out = append(out, ansi.Truncate(word, w, ""))
			word = ansi.TruncateLeft(word, w, "")
		}
		switch {
		case cur == "":
			cur = word
		case ansi.StringWidth(cur)+1+ansi.StringWidth(word) <= w:
			cur += " " + word
		default:
			flush()
			cur = word
		}
	}
	if cur != "" || len(out) == 0 {
		flush()
	}
	return out
}

// ---- help ----
