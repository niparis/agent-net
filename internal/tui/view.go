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

type layout struct {
	banner       bool
	machines     int // machine rows shown
	moreMachines int // machines not shown
	sessions     int // rows of the table
	detail       int // rows of the detail pane
	feed         int // rows of the feed
}

const maxMachineRows = 5

// layout divides the height between the parts. The sessions table gets what
// the detail pane and the feed leave; on a tall terminal they grow too.
func (m Model) layout() layout {
	var l layout
	h := max(m.height, 10)
	l.banner = m.problem != ProblemNone
	n := len(m.machineRows())
	l.machines = min(n, maxMachineRows)
	if n > maxMachineRows {
		l.moreMachines = n - maxMachineRows
		l.machines++ // the "+N more" line
	}
	// title, machines, column heads, detail heading, feed heading, help
	fixed := 1 + l.machines + 1 + 1 + 1 + 1
	if l.banner {
		fixed++
	}
	avail := max(h-fixed, 3)
	// Every part has a smallest size: the table eight rows (two machine
	// headings and six sessions), the detail pane five, the feed three. On a
	// small screen they shrink below that, the feed first. On a big one the
	// table gets the rows its sessions need, then the detail pane and the
	// feed grow, and whatever is left stays blank in the table.
	table, detail, feed := 8, 5, 3
	if avail < table+detail+feed {
		table = max(avail-detail-feed, 3)
		for table+detail+feed > avail && (feed > 1 || detail > 3) {
			if feed > 1 {
				feed--
			} else {
				detail--
			}
		}
		if table+detail+feed > avail {
			// Hopeless: the table keeps one row, the others share the rest.
			table = 1
			detail = max((avail-1)/2, 1)
			feed = max(avail-1-detail, 0)
		}
	} else {
		extra := avail - table - detail - feed
		grow := func(p *int, limit int) {
			n := min(extra, max(limit-*p, 0))
			*p += n
			extra -= n
		}
		grow(&table, len(m.tableRows()))
		grow(&detail, 12)
		grow(&feed, 14)
		table += extra
	}
	l.sessions, l.detail, l.feed = table, detail, feed
	return l
}

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

// View draws the screen. It never writes more than width columns or height rows.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	w, h := max(m.width, 40), max(m.height, 10)
	l := m.layout()
	var out []string
	add := func(s string) { out = append(out, s) }

	add(m.titleLine(w))
	if l.banner {
		add(m.bannerLine(w))
	}

	rows := m.machineRows()
	for i, r := range rows {
		if i >= maxMachineRows {
			add(sDim.Render(clip(fmt.Sprintf("  +%d more", len(rows)-maxMachineRows), w)))
			break
		}
		add(m.machineLine(r, w))
	}
	if len(rows) == 0 {
		add(sDim.Render("  no machines yet"))
	}

	for _, s := range m.sessionsTable(w, l.sessions) {
		add(s)
	}

	if m.open != nil {
		for _, s := range m.messageView(w, l.detail+l.feed+2) {
			add(s)
		}
	} else {
		add(section("Selected session", w))
		for _, s := range m.detailLines(w, l.detail) {
			add(s)
		}
		add(m.feedHeading(w))
		for _, s := range m.feedLines(w, l.feed) {
			add(s)
		}
	}

	// The help line sits on the last row; blank rows fill the gap.
	for len(out) < h-1 {
		add("")
	}
	if len(out) > h-1 {
		out = out[:h-1]
	}
	add(m.helpLine(w))
	return strings.Join(out, "\n")
}

func section(title string, w int) string {
	head := title + " "
	rule := strings.Repeat("─", max(w-ansi.StringWidth(head), 0))
	return sBold.Render(head) + sDim.Render(rule)
}

func (m Model) titleLine(w int) string {
	left := sTitle.Render("agent-net") + " " + sDim.Render("tui")
	online, total := 0, len(m.snap.Agents)
	for _, a := range m.snap.Agents {
		if a.Online {
			online++
		}
	}
	mid := fmt.Sprintf("%d sessions, %d online", total, online)
	if m.showOffline {
		mid += "  +offline"
	}
	if m.filter != "" {
		mid += "  filter: " + sanitize(m.filter)
	}
	right := "no data yet"
	if m.haveData {
		right = "updated " + m.lastOK.In(m.loc).Format("15:04:05")
	}
	if !m.haveData && m.problem == ProblemNone {
		right = "connecting..."
	}
	used := ansi.StringWidth("agent-net tui") + 2 + ansi.StringWidth(mid) + 2 + ansi.StringWidth(right)
	gap := max(w-used, 2)
	return clip(left+"  "+mid+strings.Repeat(" ", gap)+sDim.Render(right), w)
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

func (m Model) machineLine(r machineRow, w int) string {
	name := r.host
	if r.nodeID == m.snap.NodeID && r.nodeID != "" {
		name += " (here)"
	}
	version := "-"
	if r.version != "" {
		version = r.version
	}
	state, style := "offline", sBad
	if r.online {
		state, style = "online", sIdle
	}
	prefix := "  " + cell(name, 16) + " " + cell(version, 9) + " "
	rest := w - ansi.StringWidth(prefix) - 8
	line := prefix + style.Render(cell(state, 8)) + cell(harnessList(r.harnesses), max(rest, 0))
	return clip(line, w)
}

// column is one column of the sessions table.
type column struct {
	name     string
	min, max int
	width    int
}

// columns picks the columns that fit in avail columns and gives each a width.
func columns(avail int) []column {
	cols := []column{
		{name: "AGENT", min: 8, max: 8},
		{name: "NAME", min: 10, max: 18},
		{name: "PROJECT", min: 9, max: 16},
		{name: "BRANCH", min: 8, max: 16},
		{name: "MODEL", min: 10, max: 18},
		{name: "MODE", min: 4, max: 4},
		{name: "STATUS", min: 6, max: 6},
		{name: "ON", min: 3, max: 3},
		{name: "FUNCTION", min: 8, max: 80},
	}
	// Narrow terminal: drop the least useful columns first.
	for _, drop := range []string{"FUNCTION", "BRANCH", "PROJECT"} {
		if sumMin(cols) <= avail {
			break
		}
		for i, c := range cols {
			if c.name == drop {
				cols = append(cols[:i], cols[i+1:]...)
				break
			}
		}
	}
	for i := range cols {
		cols[i].width = cols[i].min
	}
	extra := avail - sumMin(cols)
	for _, name := range []string{"NAME", "PROJECT", "MODEL", "BRANCH", "FUNCTION"} {
		for i := range cols {
			if cols[i].name == name && extra > 0 {
				add := min(extra, cols[i].max-cols[i].min)
				cols[i].width += add
				extra -= add
			}
		}
	}
	return cols
}

func sumMin(cols []column) int {
	n := len(cols) - 1 // one space between columns
	for _, c := range cols {
		n += c.min
	}
	return n
}

func (m Model) sessionsTable(w, n int) []string {
	cols := columns(w - 2) // two columns for the selection marker
	head := "  "
	for i, c := range cols {
		if i > 0 {
			head += " "
		}
		head += cell(c.name, c.width)
	}
	out := []string{sDim.Render(clip(head, w))}
	rows := m.tableRows()
	if !m.haveData {
		msg := "  waiting for the daemon..."
		if m.problem != ProblemNone {
			msg = "  no data yet"
		}
		out = append(out, sDim.Render(msg))
	} else if len(rows) == 0 {
		msg := "  no sessions"
		if m.filter != "" {
			msg = "  no session matches the filter"
		} else if !m.showOffline {
			msg = "  no online sessions (o shows the offline ones)"
		}
		out = append(out, sDim.Render(msg))
	}
	top := min(m.tableTop, max(len(rows)-n, 0))
	if top > 0 && top < len(rows) && !rows[top].header {
		// The machine heading has scrolled off: keep it as the first line.
		for j := top - 1; j >= 0; j-- {
			if rows[j].header {
				out = append(out, m.groupLine(rows[j].g, w))
				break
			}
		}
	}
	for i := top; i < len(rows) && len(out) < n+1; i++ {
		r := rows[i]
		if r.header {
			out = append(out, m.groupLine(r.g, w))
			continue
		}
		out = append(out, m.sessionLine(r.agent, cols, w))
	}
	// A table with fewer rows than room keeps its height.
	for len(out) < n+1 {
		out = append(out, "")
	}
	return out
}

func (m Model) groupLine(g group, w int) string {
	name := g.host
	if g.nodeID == m.snap.NodeID && g.nodeID != "" {
		name += " (here)"
	}
	return clip(sBold.Render(" "+sanitize(name)), w)
}

func (m Model) sessionLine(a protocol.AgentView, cols []column, w int) string {
	selected := a.SessionID == m.selected
	marker := "  "
	if selected {
		marker = "> "
	}
	var parts []string
	for _, c := range cols {
		var v string
		switch c.name {
		case "AGENT":
			v = a.Harness
		case "NAME":
			v = a.Profile.Name
		case "PROJECT":
			v = firstNonEmpty(a.Repo.Project, a.Profile.Project)
		case "BRANCH":
			v = a.Repo.Branch
		case "MODEL":
			v = shortModel(a.Model)
		case "MODE":
			v = modeShort(a.Mode)
		case "STATUS":
			v = statusShort(a.Status)
		case "ON":
			v = "off"
			if a.Online {
				v = "on"
			}
		case "FUNCTION":
			v = firstNonEmpty(a.Profile.Function, a.Profile.Task)
		}
		v = cell(dashIfEmpty(v), c.width)
		if !selected && a.Online {
			switch c.name {
			case "STATUS":
				v = statusStyle(a.Status).Render(v)
			case "MODE":
				if a.Mode == protocol.ModeBackground {
					v = sDim.Render(v)
				}
			}
		}
		parts = append(parts, v)
	}
	line := marker + strings.Join(parts, " ")
	switch {
	case selected:
		return sSel.Render(clip(stripStyles(line), w))
	case !a.Online:
		return sDim.Render(clip(stripStyles(line), w))
	}
	return clip(line, w)
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

func (m Model) detailLines(w, n int) []string {
	a, ok := m.selectedAgent()
	if !ok {
		out := []string{sDim.Render("  nothing selected")}
		for len(out) < n {
			out = append(out, "")
		}
		return out
	}
	const lw = 9
	kv := func(label, value string) string {
		return clip("  "+sDim.Render(cell(label, lw))+" "+sanitize(value), w)
	}
	name := a.Profile.Name
	title := "  " + sBold.Render(sanitize(dashIfEmpty(name))) + " " + sanitize(a.Harness) + "  " + a.SessionID
	if a.PID > 0 {
		title += fmt.Sprintf("  pid %d", a.PID)
	}
	if src := m.projectSource(a); src != "" {
		title += "  project: " + src
	}
	if !a.Online {
		title += "  " + sBad.Render("offline, last seen "+m.clock(time.Unix(a.LastSeen, 0)))
	}
	status := fmt.Sprintf("%s, %s   branch %s   model %s", statusShort(a.Status), dashIfEmpty(a.Mode),
		dashIfEmpty(a.Repo.Branch), dashIfEmpty(a.Model))
	info := []string{
		clip(title, w),
		kv("folder", dashIfEmpty(a.Cwd)),
		kv("remote", dashIfEmpty(a.Repo.Remote)),
	}
	// A linked git worktree (not the main working tree), or a folder inside
	// one: say which.
	switch {
	case a.Repo.Worktree != "" && a.Repo.Root != "" && a.Repo.Worktree != a.Repo.Root:
		info = append(info, kv("worktree", a.Repo.Worktree+"  (linked to "+a.Repo.Root+")"))
	case a.Repo.Worktree != "" && a.Repo.Worktree != a.Cwd:
		info = append(info, kv("worktree", a.Repo.Worktree))
	}
	info = append(info, kv("state", status))
	if t := firstNonEmpty(a.Profile.Function, a.Profile.Task); t != "" {
		task := a.Profile.Function
		if a.Profile.Task != "" {
			task = strings.TrimSuffix(task+" / "+a.Profile.Task, " / ")
		}
		info = append(info, kv("task", task))
	}

	msgs := m.feed.messagesOf(a.SessionID, 8)
	// Messages need at least one row; the last info lines give way.
	keep := len(info)
	if len(msgs) > 0 || m.feed.unavailable != "" {
		keep = min(len(info), max(n-1, 1))
	}
	out := append([]string(nil), info[:keep]...)
	room := n - len(out)
	switch {
	case m.feed.unavailable != "":
		if room > 0 {
			out = append(out, kv("messages", m.feed.unavailable))
		}
	case len(msgs) == 0:
		if room > 0 {
			out = append(out, kv("messages", "none seen yet"))
		}
	default:
		for i, it := range msgs {
			if i >= room {
				break
			}
			label := ""
			if i == 0 {
				label = "messages"
			}
			out = append(out, kv(label, m.sessionMessageLine(a.SessionID, it)))
		}
	}
	for len(out) < n {
		out = append(out, "")
	}
	return out[:n]
}

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
		return fmt.Sprintf("%s (%s)", dashIfEmpty(a.Profile.Name), a.Hostname)
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

func (m Model) helpLine(w int) string {
	if m.filtering {
		return clip(m.input.View()+sDim.Render("   enter keeps the filter, esc clears it"), w)
	}
	var s string
	switch {
	case m.open != nil:
		s = "up/down scroll  esc close  q quit"
	case m.focus == focusFeed:
		s = "up/down move  enter open body  tab sessions  / filter  o offline  q quit"
	default:
		s = "up/down select  / filter  o offline  tab messages  r refresh  q quit"
	}
	return sDim.Render(clip(s, w))
}
