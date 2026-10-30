package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// The main screen: header, machines, sessions table, detail pane, feed, key
// line. Colours mean one thing each: green is idle or healthy, amber is busy
// or a loaded machine, blue is a shell, red is trouble or a heavy tree, dim is
// offline, unknown or secondary.

// ---- layout ----

type layout struct {
	banner       bool
	notice       int // rows of the result under the title (0 to 2)
	needs        int // rows of the "Needs you" section
	machines     int // machine rows shown
	moreMachines int // machines not shown
	sessions     int // rows of the table
	detail       int // rows of the detail pane
	feed         int // rows of the feed
}

const maxMachineRows = 5

// layout divides the height between the parts. The sessions table gets what
// the detail pane and the feed leave; on a tall terminal they grow too. With
// expand the detail pane takes the room of the feed.
func (m Model) layout() layout {
	var l layout
	h := max(m.height, 10)
	l.banner = m.problem != ProblemNone
	l.notice = len(m.noticeLines(max(m.width, 40)))
	l.needs = m.needsHeight()
	n := len(m.machineRows())
	l.machines = min(n, maxMachineRows)
	if n > maxMachineRows {
		l.moreMachines = n - maxMachineRows
		l.machines++ // the "+N more" line
	}
	if m.mode == modeNormal {
		box := min(max((h-1)/3, 6), max(h-4, 1))
		fixed := 1 + max(l.machines, 1) + 1 + l.needs + l.notice + 1 + box
		if l.banner {
			fixed++
		}
		l.sessions = max(h-fixed, 1)
		return l
	}
	// title, machines, column heads, detail heading, feed heading, key line
	fixed := 1 + l.machines + 1 + 1 + 1 + 1 + l.needs
	if l.banner {
		fixed++
	}
	fixed += l.notice
	avail := max(h-fixed, 3)
	if m.expand {
		feed := 1
		detail := min(max(avail-feed-5, 3), 14)
		table := max(avail-feed-detail, 1)
		l.sessions, l.detail, l.feed = table, detail, feed
		return l
	}
	// Smallest sizes: the table as many rows as its sessions need (three at
	// least, seven at most at first), the detail pane seven (enough for the
	// top five processes), the feed two. On a small screen they shrink, the
	// feed first. On a big one the detail pane and the feed grow, and what is
	// still left goes to the detail pane: the list never leaves a blank gap
	// before it.
	table, detail, feed := min(7, max(len(m.tableRows()), 3)), 7, 2
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
		grow(&detail, 13)
		grow(&feed, 14)
		detail += extra
	}
	l.sessions, l.detail, l.feed = table, detail, feed
	return l
}

// ---- view ----

// View draws the screen. It never writes more than width columns or height rows.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	w, h := max(m.width, 40), max(m.height, 10)
	var out []string
	add := func(s string) { out = append(out, s) }

	switch {
	case m.inboxOn:
		for _, s := range m.inboxView(w) {
			add(s)
		}
	case m.help:
		for _, s := range m.helpView(w, h-1) {
			add(s)
		}
	case m.learned:
		for _, s := range m.learnedView(w, h-1) {
			add(s)
		}
	case m.conv != nil:
		for _, s := range m.convView(w, h-1) {
			add(s)
		}
	case m.tab == 1:
		for _, s := range m.messagesView(w, h-1) {
			add(s)
		}
	case m.tab == 2:
		for _, s := range m.quotasView(w, h-1) {
			add(s)
		}
	case m.mapOn:
		for _, s := range m.mapView(w, h-1) {
			add(s)
		}
	default:
		for _, s := range m.mainView(w, h-1) {
			add(s)
		}
	}
	// CTO notices must also be visible on the map, messages and quotas views.
	if (m.tab != 0 || m.mapOn) && len(out) > 0 {
		if n := m.activeNotice(); n != nil && strings.HasPrefix(n.text, "CTO: ") {
			line := sIdle.Render(clip("  "+sanitize(n.text), w))
			out = append(out[:1], append([]string{line}, out[1:]...)...)
		}
	}
	// The key line sits on the last row; blank rows fill the gap.
	for len(out) < h-1 {
		add("")
	}
	if len(out) > h-1 {
		out = out[:h-1]
	}
	add(m.helpLine(w))
	return strings.Join(out, "\n")
}

func (m Model) mainView(w, h int) []string {
	l := m.layout()
	var out []string
	add := func(s string) { out = append(out, s) }

	add(m.titleLine(w))
	if l.banner {
		add(m.bannerLine(w))
	}
	for _, s := range m.noticeLines(w) {
		add(s)
	}
	for _, s := range m.needsLines(w) {
		add(s)
	}
	rows := m.machineRows()
	for i, r := range rows {
		if i >= maxMachineRows {
			add(sDim.Render(clip(fmt.Sprintf("  +%d more", len(rows)-maxMachineRows), w)))
			break
		}
		add(m.machineLine(r, w, machineNameWidth(rows, m.snap.NodeID), machineVersionWidth(rows)))
	}
	if len(rows) == 0 {
		add(sDim.Render("  no machines yet"))
	}
	if m.expand && m.mode == modeNormal {
		box := min(max(h/3, 6), max(h-3, 1))
		add(section("Selected session", w))
		for _, line := range m.detailLines(w, max(h-box-len(out), 0)) {
			add(line)
		}
		for len(out) < h-box {
			add("")
		}
		return append(out, m.ctoView(w, box)...)
	}
	for _, s := range m.sessionsTable(w, l.sessions) {
		add(s)
	}
	if m.mode == modeForm {
		for _, s := range m.formView(w, l.detail+l.feed+2) {
			add(s)
		}
		return out
	}
	if m.mode == modePolicy {
		for _, s := range m.policyView(w, l.detail+l.feed+2) {
			add(s)
		}
		return out
	}
	if m.mode == modeIntegrations {
		for _, s := range m.integrationsView(w, l.detail+l.feed+2) {
			add(s)
		}
		return out
	}
	if m.open != nil {
		for _, s := range m.messageView(w, l.detail+l.feed+2) {
			add(s)
		}
		return out
	}
	if m.cto != nil {
		box := min(max(h/3, 6), max(h-3, 1))
		if len(out) > h-box {
			out = out[:max(h-box, 0)]
		}
		for len(out) < h-box {
			add("")
		}
		out = append(out, m.ctoView(w, box)...)
	} else {
		add(section("Selected session", w))
		out = append(out, m.detailLines(w, l.detail)...)
	}

	return out
}

var tabNames = []string{"Sessions", "Messages", "Quotas"}
var sActiveTab = sBold.Underline(true)

func (m Model) titleLine(w int) string {

	var tabs []string
	for i, name := range tabNames {
		label := fmt.Sprintf("%d %s", i+1, name)
		if i == m.tab {
			tabs = append(tabs, sActiveTab.Render(label))
		} else {
			tabs = append(tabs, sDim.Render(label))
		}
	}
	left := strings.Join(tabs, "  ")
	if m.tab == 0 {
		if warning := m.quotaWarning(); warning != "" {
			left += "  " + sBusy.Render(warning)
		}
	}

	online, total, workers, busy := 0, 0, 0, 0
	for _, a := range m.snap.Agents {
		if protocol.IsOwner(a.Harness) {
			continue
		}
		total++
		if a.Online {
			online++
			if a.Worker != nil {
				workers++
			}
			if a.Status == "busy" {
				busy++
			}
		}
	}
	if n := m.inboxCount(); n > 0 {
		left += sDim.Render(fmt.Sprintf("  inbox %d", n))
	}
	// The gateway's online count is the one to trust; an older gateway has
	// none, so the screen counts what it has. Offline sessions are mostly old
	// ghosts, so the header leaves them out and says what the owner can act
	// on: busy sessions, sessions that wait, and the worker queue.
	mid := fmt.Sprintf("%d sessions, %d online · %d busy", total, online, busy)
	if c := m.usage.counts; c != nil {
		mid = fmt.Sprintf("%d online · %d busy", c.Online, busy)
	}
	if n := len(m.needsRows()); n > 0 {
		mid += " · " + sNeeds.Render(fmt.Sprintf("%d needs you", n))
	}
	if q := m.queuedWorkers(); workers > 0 || q > 0 {
		mid += fmt.Sprintf(" · workers %d running, %d queued", workers, q)
	}
	if n := m.learnedNew(); n > 0 {
		mid += fmt.Sprintf(", learned %d new", n)
	}
	var filters []string
	if m.activeOnly {
		filters = append(filters, "active")
	}
	if m.showOffline {
		filters = append(filters, "offline")
	}
	if m.filter != "" {
		filters = append(filters, "/"+sanitize(m.filter))
	}
	if len(filters) > 0 {
		mid += "  filter: " + strings.Join(filters, ", ")
	}
	right := "no data yet"
	if m.haveData {
		right = "updated " + m.lastOK.In(m.loc).Format("15:04:05")
	}
	if !m.haveData && m.problem == ProblemNone {
		right = "connecting..."
	}
	used := ansi.StringWidth(left) + 3 + ansi.StringWidth(mid) + 2 + ansi.StringWidth(right)
	if used > w {
		right = ""
		used = ansi.StringWidth(left) + 3 + ansi.StringWidth(mid)
	}
	if used > w {
		mid = ""
		used = ansi.StringWidth(left)
	}
	if mid == "" {
		return clip(left, w)
	}
	if right == "" {
		return clip(left+" │ "+mid, w)
	}
	gap := max(w-used, 2)
	return clip(left+" │ "+mid+strings.Repeat(" ", gap)+sDim.Render(right), w)
}

// queuedWorkers counts the starts that wait for room on every machine, from
// the admission queue in the usage reports. A machine that has no room is not
// counted twice when its report comes both live and through the gateway.
func (m Model) queuedWorkers() int {
	if !m.usage.have {
		return 0
	}
	var ids []string
	if l := m.usage.raw.Local; l != nil {
		ids = append(ids, m.usage.raw.NodeID)
	}
	if g := m.usage.raw.Gateway; g != nil {
		for _, n := range g.Nodes {
			ids = append(ids, n.NodeID)
		}
	}
	seen := map[string]bool{}
	total := 0
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		n, st := m.nodeUsage(id)
		if st != usageOK || n.Usage.Workers == nil {
			continue
		}
		q := n.Usage.Workers.Queued
		if len(n.Usage.Workers.Queue) > q {
			q = len(n.Usage.Workers.Queue)
		}
		total += q
	}
	return total
}

// ---- machines ----

// machineNameWidth is the width of the name column: the longest name, with
// "(here)", between 8 and 16.
func machineNameWidth(rows []machineRow, here string) int {
	n := 8
	for _, r := range rows {
		l := utf8.RuneCountInString(r.host)
		if r.nodeID == here && here != "" {
			l += len(" (here)")
		}
		n = max(n, l)
	}
	return min(n, 16)
}

// machineVersionWidth is the width of the version column: the longest version.
func machineVersionWidth(rows []machineRow) int {
	n := 3
	for _, r := range rows {
		n = max(n, utf8.RuneCountInString(r.version))
	}
	return min(n, 10)
}

// loadStyle colours a machine figure by how loaded it is (percent).
func loadStyle(pct float64) lipgloss.Style {
	switch {
	case pct >= hotMachine:
		return sBad
	case pct >= warmMachine:
		return sBusy
	}
	return sIdle
}

// machineLine writes one machine: name, version, state, CPU now and over the
// last 5 minutes, memory, load, and the harnesses when there is room.
func (m Model) machineLine(r machineRow, w, nameW, versionW int) string {
	name := r.host
	if r.nodeID == m.snap.NodeID && r.nodeID != "" {
		name += " (here)"
	}
	version := "-"
	if r.version != "" {
		version = r.version
	}
	dot, state, style := "○", "offline", sDim
	if r.online {
		dot, state, style = "●", "online", sIdle
	}
	line := " " + style.Render(dot) + " " + cell(name, nameW) + " " + cell(version, versionW) + " " + style.Render(cell(state, 7))
	line += " " + m.machineUsage(r, w-ansi.StringWidth(line)-1)
	if !r.online {
		return clip(sDim.Render(ansi.Strip(line)), w)
	}
	rest := w - ansi.StringWidth(line) - 2
	// The shared OpenCode service belongs to the machine, not to any one
	// session: a short note here, its process list in a session's detail.
	if t, st := m.sharedService(r.nodeID); st == usageOK {
		note := fmt.Sprintf("%s service %s %s", sanitize(t.Harness), fmtMem(t.RSS), plural(t.Procs, "proc"))
		if ansi.StringWidth(note) <= rest {
			line += "  " + sDim.Render(note)
			rest -= ansi.StringWidth(note) + 2
		}
	}
	// How many workers run and wait here, and whether a new one would start.
	if n, st := m.nodeUsage(r.nodeID); st == usageOK && n.Usage.Workers != nil && (n.Usage.Workers.Running > 0 || n.Usage.Workers.Queued > 0 || n.Usage.Workers.Room != protocol.RoomOK) {
		wl := *n.Usage.Workers
		if note := wl.RoomText(); ansi.StringWidth(note) <= rest {
			style := sDim
			if wl.Room != protocol.RoomOK {
				style = sBusy
			}
			line += "  " + style.Render(note)
			rest -= ansi.StringWidth(note) + 2
		}
	}
	if rest >= 8 && r.harnesses != nil {
		for _, withVersions := range []bool{true, false} {
			if hl := harnessText(r.harnesses, withVersions); ansi.StringWidth(hl) <= rest {
				line += "  " + sDim.Render(hl)
				break
			}
		}
	}
	return clip(line, w)
}

// Widths of the fixed parts of a machine's usage. The CPU cell holds a
// three-digit percentage now and in the 5 minute average: "cpu ████ 100% (100%)".
const (
	machineCPUW  = 20
	machineMemW  = 18
	machineLoadW = len("load 1.2")
)

// machineUsage writes the CPU, memory and load of a machine in a fixed width
// (about 48 columns). An unknown figure is "?", dim.
func (m Model) machineUsage(r machineRow, room int) string {
	unknownW := machineCPUW + 1 + machineMemW + 1 + machineLoadW
	if !r.online {
		return strings.Repeat(" ", unknownW)
	}
	n, st := m.nodeUsage(r.nodeID)
	if st != usageOK || n.Usage.Machine.Samples == 0 && n.Usage.Machine.MemTotal == 0 {
		label := "cpu ?  mem ?  load ?"
		if st == usageStale {
			label += "  (stale)"
		}
		return cell(sDim.Render(label), unknownW)
	}
	mu := n.Usage.Machine
	var cpu string
	if mu.Samples == 0 {
		cpu = sDim.Render("cpu ...")
	} else {
		cpu = "cpu " + loadStyle(mu.CPU).Render(bar(mu.CPU/100, 4)) + " " + cellR(fmtPct(mu.CPU), 4) + sDim.Render(" ("+fmtPct(mu.CPUAvg)+")")
	}
	memPct := 0.0
	if mu.MemTotal > 0 {
		memPct = float64(mu.MemUsed) / float64(mu.MemTotal) * 100
	}
	mem := "mem " + loadStyle(memPct).Render(bar(memPct/100, 4)) + " " + fmtMemPair(mu.MemUsed, mu.MemTotal)
	load := sDim.Render("load ") + fmt.Sprintf("%.1f", mu.Load[0])
	if room < unknownW {
		return padStyled(cpu, machineCPUW) + " " + padStyled(mem, machineMemW) // no room for the load
	}
	return padStyled(cpu, machineCPUW) + " " + padStyled(mem, machineMemW) + " " + load
}

// padStyled pads (or cuts) a string that holds colour codes to w columns.
func padStyled(s string, w int) string {
	if ansi.StringWidth(s) > w {
		return ansi.Truncate(s, w, "")
	}
	return s + strings.Repeat(" ", w-ansi.StringWidth(s))
}

// harnessText lists the installed harnesses compactly: "claude 2.1 opencode 2.2".
func harnessText(hs []protocol.HarnessInfo, versions bool) string {
	var parts []string
	for _, h := range hs {
		if v := numberOf(h.Version); v != "" && versions {
			parts = append(parts, h.Name+" "+v)
		} else {
			parts = append(parts, h.Name)
		}
	}
	return strings.Join(parts, "  ")
}

// ---- sessions table ----

// column is one column of the sessions table.
type column struct {
	name     string
	min, max int
	width    int
	right    bool
}

// columns picks the columns that fit in avail columns and gives each a width.
// The columns are, in order: NAME, AGENT, PROJECT, MODEL, STATUS, TRUST,
// POLICY, CPU, RAM, TASK. TRUST is always there. TASK takes whatever is left
// after the others. When the terminal is narrow the columns that give way are
// TASK, MODEL, PROJECT, then POLICY (decision 0017).
func columns(avail, nameWant int) []column { return columnsFor(avail, nameWant, false) }

// columnsFor is columns, with the POLICY column when some session has a
// delegation policy (decision 0014). Without one the column would only take
// room from the others.
func columnsFor(avail, nameWant int, policy bool) []column {
	cols := []column{
		{name: "NAME", min: 12, max: min(max(nameWant, 18), 32)},
		{name: "AGENT", min: 8, max: 8},
		{name: "PROJECT", min: 9, max: 16},
		{name: "MODEL", min: 10, max: 18},
		{name: "STATUS", min: 6, max: 6},
		{name: "TRUST", min: 5, max: 5},
		{name: "CPU", min: 5, max: 5, right: true},
		{name: "RAM", min: 5, max: 5, right: true},
		{name: "TASK", min: 10, max: 4000},
	}
	if policy {
		// After TRUST.
		for i, c := range cols {
			if c.name == "TRUST" {
				cols = append(cols[:i+1], append([]column{{name: "POLICY", min: 6, max: 6}}, cols[i+1:]...)...)
				break
			}
		}
	}
	for _, drop := range []string{"TASK", "MODEL", "PROJECT", "POLICY"} {
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
	if hasColumn(cols, "TASK") {
		// Grow NAME to what it needs, then give every other column to TASK.
		extra := avail - sumMin(cols)
		for i := range cols {
			switch cols[i].name {
			case "NAME":
				add := min(extra, cols[i].max-cols[i].min)
				cols[i].width += add
				extra -= add
			case "TASK":
				cols[i].width += extra
				extra = 0
			}
		}
		return cols
	}
	// Without TASK the room goes to the name and the project columns.
	extra := avail - sumMin(cols)
	for _, name := range []string{"NAME", "PROJECT", "MODEL"} {
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

func hasColumn(cols []column, name string) bool {
	for _, c := range cols {
		if c.name == name {
			return true
		}
	}
	return false
}

func sumMin(cols []column) int {
	n := len(cols) - 1 // one space between columns
	for _, c := range cols {
		n += c.min
	}
	return n
}

// cellDots fits s into w columns and marks a cut with an ellipsis, as the TASK
// column does.
func cellDots(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = sanitize(s)
	if ansi.StringWidth(s) > w {
		if w <= 1 {
			s = ansi.Truncate(s, w, "")
		} else {
			s = ansi.Truncate(s, w, "…")
		}
	}
	if pad := w - ansi.StringWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// cellR fits s into w columns, right aligned.
func cellR(s string, w int) string {
	s = sanitize(s)
	if ansi.StringWidth(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	return strings.Repeat(" ", max(w-ansi.StringWidth(s), 0)) + s
}

func (m Model) sessionsTable(w, n int) []string {
	nameWant, policy := 0, false
	for _, g := range m.groups() {
		for _, a := range g.agents {
			nameWant = max(nameWant, utf8.RuneCountInString(nameText(a, g.marks[a.SessionID], 1<<20)))
			policy = policy || policyBadge(a.Delegation) != ""
		}
	}
	cols := m.needsColumns(columnsFor(w-2, nameWant, policy)) // two columns for the selection marker
	wanted := 5
	for _, g := range m.groups() {
		for _, a := range g.agents {
			wanted = max(wanted, utf8.RuneCountInString(m.requestRuleBadge(a)))
		}
	}
	for i := range cols {
		if cols[i].name == "TRUST" && wanted > cols[i].width {
			for j := range cols {
				if cols[j].name == "TASK" {
					delta := min(wanted-cols[i].width, max(0, cols[j].width-cols[j].min))
					cols[i].width += delta
					cols[j].width -= delta
				}
			}
		}
	}
	head := "  "
	for i, c := range cols {
		if i > 0 {
			head += " "
		}
		if c.right {
			head += cellR(c.name, c.width)
		} else {
			head += cell(c.name, c.width)
		}
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
		switch {
		case m.filter != "":
			msg = "  no session matches the filter"
		case m.activeOnly:
			msg = "  no active sessions (a shows the quiet ones)"
		case !m.showOffline:
			msg = "  no online sessions (o shows the offline and dormant ones)"
		}
		out = append(out, sDim.Render(msg))
	}
	top := min(m.tableTop, max(len(rows)-1, 0))
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
		} else {
			out = append(out, m.sessionLine(r.agent, r.mark, cols, w))
		}
	}
	// A table with fewer rows than room keeps its height.
	for len(out) < n+1 {
		out = append(out, "")
	}
	return out
}

// groupLine is the heading of one machine: its name, bold, a light rule, the
// session count and how many the active filter hides.
func (m Model) groupLine(g group, w int) string {
	name := g.host
	if g.nodeID == m.snap.NodeID && g.nodeID != "" {
		name += " (here)"
	}
	var notes []string
	if len(g.agents) > 0 {
		notes = append(notes, plural(len(g.agents), "session"))
	}
	if g.hidden > 0 {
		notes = append(notes, fmt.Sprintf("%d hidden", g.hidden))
	}
	head := " " + sBold.Render(sanitize(name))
	if len(notes) > 0 {
		head += " " + sDim.Render(strings.Join(notes, " · ")+" ")
	} else {
		head += " "
	}
	rule := strings.Repeat("─", max(w-ansi.StringWidth(head), 0))
	return clip(head+sDim.Render(rule), w)
}

// usageCells writes the CPU and RAM cells of a session. They merge into one
// note for a tree that several sessions share, and read "?" when the figure is
// missing or old. A worker's private OpenCode server is its own tree, so its
// figures stay on the worker's row.
func (m Model) usageCells(a protocol.AgentView, cols []column) map[string]string {
	cells := map[string]string{}
	width := func(name string) int {
		for _, c := range cols {
			if c.name == name {
				return c.width
			}
		}
		return 0
	}
	t, st := m.treeOf(a)
	switch {
	case !a.Online:
		return cells
	case st != usageOK:
		for _, k := range []string{"CPU", "RAM"} {
			cells[k] = sDim.Render(cellR("?", width(k)))
		}
	case sharedTree(t):
		all := width("CPU") + width("RAM") + 1
		cells["CPU"] = sDim.Render(cell("shared", all))
		cells["RAM"] = "\x00" // swallowed by the CPU cell
	default:
		cpu, ram := cellR(fmtPct(t.CPU), width("CPU")), cellR(fmtMem(t.RSS), width("RAM"))
		if t.Samples == 0 {
			cpu = sDim.Render(cellR("...", width("CPU")))
		}
		if heavy(t) {
			if t.CPU >= HeavyCPU {
				cpu = sBad.Render(cpu)
			}
			if t.RSS >= HeavyRSS {
				ram = sBad.Render(ram)
			}
		}
		cells["CPU"], cells["RAM"] = cpu, ram
	}
	return cells
}

func (m Model) sessionLine(a protocol.AgentView, mk treeMark, cols []column, w int) string {
	selected := a.SessionID == m.selected
	marker := "  "
	if selected {
		marker = "> "
	}
	uc := m.usageCells(a, cols)
	var parts []string
	for _, c := range cols {
		if v, ok := uc[c.name]; ok {
			if v != "\x00" {
				parts = append(parts, v)
			}
			continue
		}
		var v string
		switch c.name {
		case "NAME":
			v = nameText(a, mk, c.width)
		case "AGENT":
			v = a.Harness
		case "PROJECT":
			v = firstNonEmpty(a.Repo.Project, a.Profile.Project)
		case "MODEL":
			v = shortModel(a.Model)
		case "STATUS":
			v = m.statusCell(a)
		case "TRUST":
			v = m.requestRuleBadge(a)
		case "POLICY":
			v = policyBadge(a.Delegation)
		case "TASK":
			v = taskText(a)
		}
		if c.name != "TASK" {
			v = dashIfEmpty(v)
		}
		if c.name == "TASK" {
			v = cellDots(v, c.width)
		} else {
			v = cell(v, c.width)
		}
		if !selected && a.Online {
			switch c.name {
			case "STATUS":
				v = m.statusCellStyle(a).Render(v)
			case "AGENT", "POLICY", "TASK":
				v = sDim.Render(v)
			case "TRUST":
				if m.wildcardRule(firstNonEmpty(a.Repo.Project, a.Profile.Project)) != "normal" {
					v = sBusy.Render(v)
				} else {
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
	case !a.Online || m.idleLong(a):
		return sDim.Render(clip(stripStyles(line), w))
	}
	return clip(line, w)
}

// rowName is the name of a session in the table: the automatic name the
// gateway gave it, which is the project, or "<project> worker <n>" for a
// worker (decision 0024). A name the agent set with agent_announce wins. A row
// with no name at all falls back to the worker ID or the session ID.
func rowName(a protocol.AgentView) string {
	if name := sanitize(a.Profile.Name); name != "" {
		return name
	}
	if a.Worker != nil {
		id := a.Worker.ID
		if id == "" {
			id = a.SessionID
		}
		return "worker " + sanitize(a.Harness) + " " + shortID(id)
	}
	return shortID(a.SessionID)
}

// taskText is the TASK cell: the task the session announced. A worker without
// an announcement shows the first line of the task its owner gave it. Any
// other session falls back to its harness title when that says something the
// NAME does not. It is blank, not a dash, when there is nothing to say.
func taskText(a protocol.AgentView) string {
	if t := strings.TrimSpace(a.Profile.Task); t != "" {
		return sanitize(t)
	}
	if a.Worker != nil {
		if t := strings.TrimSpace(a.Worker.Task); t != "" && t != strings.TrimSpace(rowName(a)) {
			return sanitize(t)
		}
	}
	title := strings.TrimSpace(a.Title)
	if title == "" || title == strings.TrimSpace(rowName(a)) {
		return ""
	}
	return sanitize(title)
}

// nameText is the NAME cell of a session in w columns. A worker has its tree
// glyphs before the name, and "@host" after it when it runs on another machine
// than its caller. The name gives way first: it is cut with an ellipsis so the
// glyphs and the host stay.
func nameText(a protocol.AgentView, mk treeMark, w int) string {
	name := rowName(a)
	suffix := ""
	if mk.host != "" {
		suffix = " @" + sanitize(mk.host)
	}
	room := w - utf8.RuneCountInString(mk.prefix) - utf8.RuneCountInString(suffix)
	if room >= 4 && utf8.RuneCountInString(name) > room {
		name = ansi.Truncate(name, room, "…")
	}
	return mk.prefix + name + suffix
}

// plural writes a count and a noun: "1 session", "3 sessions".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// noAgentsMD is what FUNCTION says for an online session whose project has no
// description (decision 0009). It is a clear message to fix the project.
const noAgentsMD = "no AGENTS.md"

// functionText is the FUNCTION of a session: the project description the
// gateway read from AGENTS.md. Empty is a real answer for an online session.
func functionText(a protocol.AgentView) string {
	if f := strings.TrimSpace(a.Profile.Function); f != "" {
		return f
	}
	if a.Online {
		return noAgentsMD
	}
	return ""
}

func (m Model) wildcardRule(project string) string {
	for _, e := range m.snap.Edges {
		if e.From == "*" && e.To == project {
			return e.Rule
		}
	}
	return "normal"
}
func (m Model) requestRuleBadge(a protocol.AgentView) string {
	project := firstNonEmpty(a.Repo.Project, a.Profile.Project)
	rule := m.wildcardRule(project)
	if rule == "normal" {
		rule = "-"
	}
	n := 0
	for _, e := range m.snap.Edges {
		if e.To == project && e.From != "*" {
			n++
		}
	}
	if n > 0 {
		return fmt.Sprintf("%s +%d", rule, n)
	}
	return rule
}

func (m Model) messagesView(w, h int) []string {
	out := []string{m.titleLine(w)}
	if m.open != nil {
		return append(out, m.messageView(w, h-1)...)
	}
	out = append(out, m.feedHeading(w))
	return append(out, m.feedLines(w, max(h-2, 0))...)
}
