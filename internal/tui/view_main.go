package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
	machines     int // machine rows shown
	moreMachines int // machines not shown
	sessions     int // rows of the table
	detail       int // rows of the detail pane
	feed         int // rows of the feed
}

const maxMachineRows = 5

// ctoBoxHeight keeps half the rows previously reserved for the CTO. Round up
// when the old height is odd so that its latest output still has room.
func (m Model) ctoBoxHeight(tab int) int {
	if !m.showCTOBox(tab) {
		return 0
	}
	h := max(m.height, 10)
	box := min(max((h-1)/3, 6), max(h-4, 1))
	if tab == quotasTab && h <= 24 && box > 6 {
		box = 6
	}
	return (box + 1) / 2
}

// showCTOBox hides the box on the Graph tab unless the CTO proposes a change
// to a node or an edge. The canvas needs those rows more than an empty box.
func (m Model) showCTOBox(tab int) bool {
	if tab != graphTab || m.focus == focusCTO || m.mapOn {
		return true
	}
	for _, p := range m.proposals {
		if p.Status != "open" {
			continue
		}
		if p.Tool == "charter_set" || p.Action == "charter_set" || p.Tool == "edge_set" || p.Action == "edge_set" {
			return true
		}
	}
	return false
}

// layout divides the height between the parts. The sessions table gets what
// the detail pane and the feed leave; on a tall terminal they grow too. With
// expand the detail pane takes the room of the feed.
func (m Model) layout() layout {
	var l layout
	h := max(m.height, 10)
	l.banner = m.problem != ProblemNone || (m.haveData && len(m.snap.FailedCalls) > 0)
	n := len(m.machineRows())
	l.machines = min(n, maxMachineRows)
	if n > maxMachineRows {
		l.moreMachines = n - maxMachineRows
		l.machines++ // the "+N more" line
	}
	if m.mode == modeNormal {
		box := m.ctoBoxHeight(m.tab)
		fixed := 1 + max(l.machines, 1) + 3 + 1 + box
		if l.banner {
			fixed++
		}
		l.sessions = max(h-fixed, 1)
		return l
	}
	// title, machines, column heads, detail heading, feed heading, key line
	fixed := 1 + l.machines + 1 + 1 + 1 + 1
	if l.banner {
		fixed++
	}
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
func (m Model) View() tea.View {
	v := tea.NewView(m.renderView())
	v.AltScreen = true
	if m.graphMouseActive() && !m.quitting {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

func (m Model) renderView() string {
	if m.quitting {
		return ""
	}
	w, h := max(m.width, 40), max(m.height, 10)
	if m.finder {
		background := m
		background.finder = false
		background.tab = sessionsTab
		lines := strings.Split(background.renderView(), "\n")
		box := m.finderView(max(40, w-8))
		y := max(0, (h-len(box))/2)
		x := max(0, (w-ansi.StringWidth(box[0]))/2)
		for i, line := range box {
			if y+i >= len(lines) {
				break
			}
			right := x + ansi.StringWidth(line)
			lines[y+i] = padStyled(ansi.Cut(lines[y+i], 0, x), x) + line + ansi.Cut(lines[y+i], right, w)
		}
		return strings.Join(lines, "\n")
	}
	keyRows := 1
	if m.tab == toolsTab && !m.help && !m.mapOn && m.focus != focusCTO {
		keyRows = len(m.toolsKeyLines(w))
	}
	var out []string
	add := func(s string) { out = append(out, s) }

	switch {
	case m.finder:
		out = append(out, m.finderView(w)...)
	case m.proposalConfirm:
		out = append(out, m.proposalConfirmView(w)...)
	case m.proposalHistory && m.proposalDetail == nil && !m.help:
		out = append(out, m.proposalHistoryView(w)...)
	case m.proposalDetail != nil && !m.help:
		out = append(out, m.proposalDetailView(w)...)
	case m.tab == quotasTab && m.qualityDetail && !m.help:
		out = append(out, m.qualityDetailsView(w, h-1)...)
	case m.help:
		for _, s := range m.helpView(w, h-1) {
			add(s)
		}
	default:
		box := m.ctoBoxHeight(m.tab)
		contentHeight := h - keyRows - box
		switch {
		case m.archive != nil:
			out = append(out, m.titleLine(w))
			out = append(out, m.sessionsArchiveView(w, contentHeight-1)...)
		case m.mode == modeSessionForm:
			// The New session form replaces the Graph card too, so n on the
			// card keeps the Graph tab and Esc returns to the card.
			out = append(out, m.mainView(w, contentHeight)...)
		case m.mode == modeLeadPicker:
			out = append(out, m.titleLine(w))
			out = append(out, m.leadPickerView(w, contentHeight-1)...)
		case m.mode == modeTeams:
			out = append(out, m.titleLine(w))
			out = append(out, m.teamsView(w, contentHeight-1)...)
		case m.mode == modeCharterForm:
			out = append(out, m.titleLine(w))
			out = append(out, m.charterFormView(w, contentHeight-1)...)
		case m.mode == modeTeamForm:
			out = append(out, m.titleLine(w))
			out = append(out, m.teamFormView(w, contentHeight-1)...)
		case m.mapOn:
			out = append(out, m.titleLine(w))
			out = append(out, m.mapView(w, contentHeight-1)...)
		case m.tab == graphTab:
			out = append(out, m.graphView(w, contentHeight)...)
		case m.tab == toolsTab:
			out = append(out, m.toolsView(w, contentHeight)...)
		case m.tab == messagesTab:
			out = append(out, m.messagesView(w, contentHeight)...)
		case m.tab == quotasTab:
			out = append(out, m.quotasView(w, contentHeight)...)
		case m.tab == settingsTab:
			out = append(out, m.settingsView(w, contentHeight)...)
		case m.tab == inboxTab:
			out = append(out, m.inboxTabView(w, contentHeight)...)
		default:
			out = append(out, m.mainView(w, contentHeight)...)
		}
		if len(out) > contentHeight {
			out = out[:contentHeight]
		}
		for len(out) < contentHeight {
			out = append(out, "")
		}
		if m.cto != nil && box > 0 {
			out = append(out, m.ctoView(w, box)...)
		}

	}
	// The key line sits on the last row; blank rows fill the gap. A result
	// replaces its hints there, so no row is added or removed.
	for len(out) < h-keyRows {
		add("")
	}
	if len(out) > h-keyRows {
		out = out[:h-keyRows]
	}
	add(m.helpLine(w))
	return strings.Join(out, "\n")
}

func (m Model) mainView(w, h int) []string {
	// A stop question uses only the key line; keep the selected board in place.
	if m.mode == modeConfirm {
		m.mode = modeNormal
	}
	l := m.layout()
	var out []string
	add := func(s string) { out = append(out, s) }

	add(m.titleLine(w))
	if l.banner {
		if m.problem != ProblemNone {
			add(m.bannerLine(w))
		} else {
			add(m.partialLine(w))
		}
	}
	rows := m.machineRows()
	var machines []string
	for i, r := range rows {
		if i >= maxMachineRows {
			machines = append(machines, sDim.Render(clip(fmt.Sprintf("  +%d more", len(rows)-maxMachineRows), w-4)))
			break
		}
		machines = append(machines, m.machineLineFor(r, rows, w-4, machineNameWidth(rows, m.snap.NodeID)))
	}
	if len(rows) == 0 {
		machines = append(machines, sDim.Render("  no machines yet"))
	}
	out = append(out, panel("Machines", machines, w, roleAccent)...)

	if m.expand && m.mode == modeNormal {
		add(section("Selected session", w))
		return append(out, m.detailLines(w, max(h-len(out), 0))...)
	}

	if m.mode == modeNormal {
		for _, s := range m.sessionsTable(w, l.sessions) {
			add(s)
		}
	}
	if m.mode == modeCompose {
		for _, s := range m.composeView(w, l.detail+l.feed+2) {
			add(s)
		}
		return out
	}
	if m.mode == modeSessionForm {
		for _, s := range m.sessionFormView(w, l.detail+l.feed+2) {
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
	if m.mode == modeApps {
		for _, s := range m.appsView(w, l.detail+l.feed+2) {
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

	return out
}

const (
	sessionsTab = 0
	graphTab    = 1
	quotasTab   = 2
	messagesTab = 3
	toolsTab    = 4
	settingsTab = 5
	inboxTab    = 6 // the Desk (decision 0090); the Delivery tab is part of it
)

func (m Model) composeView(w, room int) []string {
	title := "Message"
	if m.composeName != "" {
		title = "Message to " + m.composeName
	}
	var lines []string
	val := m.compose.Value()
	if val == "" {
		lines = append(lines, sDim.Render("type a message..."))
	} else {
		for _, l := range wrap(val, w-4) {
			lines = append(lines, l)
		}
	}
	for len(lines) < max(room-2, 3) {
		lines = append(lines, "")
	}
	return panel(title, lines, w, roleAccent)
}

var tabNames = []string{"Sessions", "Graph", "Quotas", "Messages", "Tools", "Settings", "Desk"}
var shortTabNames = []string{"Sess", "Graph", "Quota", "Msg", "Tools", "Set", "Desk"}
var sActiveTab = lipgloss.NewStyle().Bold(true).Foreground(roleBase).Background(roleAccent)

// sessionViewName is the view the v key selected.
func (m Model) sessionViewName() string {
	name := "active"
	if !m.activeOnly {
		name = "all"
	}
	if m.showHiddenSessions {
		name += "+hidden"
	}
	return name
}

func (m Model) titleLine(w int) string {

	var tabs []string
	count := m.waitingCount()
	names := tabNames
	fullWidth := ansi.StringWidth(strings.Join(tabNames, " ")) + len("1 2 3 4 5 6 7 ")
	if count > 0 {
		fullWidth += len(fmt.Sprintf(" (%d)", count))
	}
	onlineCount := 0
	for _, a := range m.snap.Agents {
		if !protocol.IsOwner(a.Harness) && a.Online {
			onlineCount++
		}
	}
	if m.usage.counts != nil {
		onlineCount = m.usage.counts.Online
	}
	required := fmt.Sprint(onlineCount)
	if !m.haveData {
		required = "loading…"
	}
	if m.filter != "" {
		required = "filter: /" + sanitize(m.filter)
	}
	if len(m.snap.GatewayWarnings) > 0 {
		required = strings.Join(m.snap.GatewayWarnings, "  ")
	}
	if fullWidth+1+ansi.StringWidth(required) > w {
		names = shortTabNames
	}

	for i, name := range names {
		label := fmt.Sprintf("%d %s", i+1, name)
		if i == inboxTab && count > 0 {
			label += fmt.Sprintf(" (%d)", count)
		}
		switch {
		case i == m.tab:
			tabs = append(tabs, sActiveTab.Render(label))
		case i == inboxTab && count > 0:
			tabs = append(tabs, sNeeds.Render(label))
		default:
			tabs = append(tabs, sDim.Render(label))
		}
	}
	left := strings.Join(tabs, " ")
	if len(m.hiddenSessions) > 0 {
		left += sDim.Render(fmt.Sprintf("  hidden %d", len(m.hiddenSessions)))
	}
	if m.tab == sessionsTab {
		for _, w := range m.snap.GatewayWarnings {
			style := lipgloss.NewStyle().Foreground(roleWarning)
			left += "  " + style.Render(w)
		}
		if warning, exhausted := m.quotaWarning(); warning != "" {
			style := lipgloss.NewStyle().Foreground(roleWarning)
			if exhausted {
				style = sBad
			}
			left += "  " + style.Render(warning)
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
	// The gateway's online count is the one to trust; an older gateway has
	// none, so the screen counts what it has. Offline sessions are mostly old
	// ghosts, so the header leaves them out and says what the owner can act
	// on: busy sessions, sessions that wait, and the worker queue.
	// compact is the same status in fewer words, for a narrow terminal.
	mid := fmt.Sprintf("%d sessions, %d online · %d busy", total, online, busy)
	compact := fmt.Sprintf("%d online · %d busy", online, busy)
	if c := m.usage.counts; c != nil {
		mid = fmt.Sprintf("%d online · %d busy", c.Online, busy)
		compact = mid
	}
	if n := m.waitingCount(); n > 0 {
		mid += " · " + sNeeds.Render(fmt.Sprintf("%d needs you", n))
		compact += " · " + sNeeds.Render(fmt.Sprintf("%d needs you", n))
	}
	if q := m.queuedWorkers(); workers > 0 || q > 0 {
		mid += fmt.Sprintf(" · workers %d running, %d queued", workers, q)
		if workers > 0 {
			compact += " · " + plural(workers, "worker")
		}
		if q > 0 {
			compact += fmt.Sprintf(" · %d queued", q)
		}
	}
	if n := m.learnedNew(); n > 0 {
		mid += fmt.Sprintf(", learned %d new", n)
	}
	var filters []string
	if m.filter != "" {
		filters = append(filters, "/"+sanitize(m.filter))
	}
	if len(filters) > 0 {
		mid += "  filter: " + strings.Join(filters, ", ")
	}
	right := "no data yet"
	if m.haveData {
		right = "updated " + m.displayTime(m.lastOK.Unix()).Format("15:04:05")
	}
	if !m.haveData && m.problem == ProblemNone {
		right = "loading…"
	}
	if m.mapOn {
		mid = fmt.Sprintf("%d nodes, %d edges", len(m.mp.nodes), len(m.mp.edges))
		switch {
		case m.mp.loading:
			right = "reading..."
		case m.mp.loaded:
			right = "updated " + m.clock(m.mp.at)
		default:
			right = ""
		}
	}
	used := ansi.StringWidth(left) + 3 + ansi.StringWidth(mid) + 2 + ansi.StringWidth(right)
	if used > w && !m.haveData && ansi.StringWidth(left)+3+ansi.StringWidth(right) <= w {
		return clip(left+" │ "+sDim.Render(right), w)
	}
	// The tabs fill most of 80 columns: say just the time before dropping it.
	if used > w && strings.HasPrefix(right, "updated ") {
		right = strings.TrimPrefix(right, "updated ")
		used = ansi.StringWidth(left) + 3 + ansi.StringWidth(mid) + 2 + ansi.StringWidth(right)
	}
	if used > w && w <= 80 && m.usage.counts != nil && len(filters) == 0 && !m.mapOn {
		short := fmt.Sprintf("%d online", m.usage.counts.Online)
		if ansi.StringWidth(left)+3+ansi.StringWidth(short) <= w {
			return clip(left+" │ "+short, w)
		}
	}
	// Then drop the time, then say the same status in fewer words.
	if used > w {
		right = ""
		used = ansi.StringWidth(left) + 3 + ansi.StringWidth(mid)
	}
	if used > w && !m.mapOn && len(filters) == 0 {
		mid = compact
		used = ansi.StringWidth(left) + 3 + ansi.StringWidth(mid)
	}
	if used > w && len(filters) > 0 {
		mid = "filter: " + strings.Join(filters, ", ")
		used = ansi.StringWidth(left) + 3 + ansi.StringWidth(mid)
	}
	// Last, drop whole parts from the end: a part is never cut in the middle.
	if used > w {
		room := w - ansi.StringWidth(left) - 3
		plain := ansi.Strip(mid)
		for room > 0 && ansi.StringWidth(plain) > room {
			i := max(strings.LastIndex(plain, " · "), strings.LastIndex(plain, ", "))
			if i < 0 {
				plain = ""
				break
			}
			plain = plain[:i]
		}
		if room <= 0 {
			plain = ""
		}
		mid = plain
		used = ansi.StringWidth(left) + 3 + ansi.StringWidth(mid)
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

// machineNameWidth is the display width of the longest machine name, with
// "(here)" when it is local.
func machineNameWidth(rows []machineRow, here string) int {
	n := 1
	for _, r := range rows {
		l := ansi.StringWidth(r.host)
		if r.nodeID == here && here != "" {
			l += len(" (here)")
		}
		n = max(n, l)
	}
	return min(n, 15)
}

// loadStyle colours a machine figure by how loaded it is (percent).
func loadStyle(pct float64) lipgloss.Style {
	switch {
	case pct >= hotMachine:
		return sBad
	case pct >= warmMachine:
		return sNeeds
	}
	return sIdle
}

type machineFigures struct {
	cpu, mem, disk, workers, top, note string
	machine                            protocol.MachineUsage
	cpuPct, memPct                     float64
	diskFree                           uint64
	workerCount                        int
	knownCPU, knownMem                 bool
}

type machineRowLayout struct {
	nameW, versionW, cpuW, memW, diskW, workersW, topW, noteW int
	showDisk, showWorkers, showTop, showNote                  bool
}

func (m Model) machineLine(r machineRow, w, nameW int) string {
	return m.machineLineFor(r, m.machineRows(), w, nameW)
}

func (m Model) machineLineFor(r machineRow, rows []machineRow, w, nameW int) string {
	figures := make(map[string]machineFigures, len(rows))
	for _, row := range rows {
		figures[row.nodeID] = m.machineFigures(row, rows)
	}
	layout := machineRowWidths(rows, figures, w, nameW)
	fig := figures[r.nodeID]
	name := r.host
	if r.nodeID == m.snap.NodeID && r.nodeID != "" {
		name += " (here)"
	}
	dot, style := "○", sDim
	if r.online {
		dot, style = "●", sBusy
		if _, st := m.nodeUsage(r.nodeID); st == usageStale {
			style = sNeeds
		}
	}
	line := " " + style.Render(dot) + " " + cell(name, layout.nameW)
	if layout.versionW > 0 {
		line += " " + m.machineVersionCell(r, rows, layout.versionW)
	}
	line += " " + padStyled(m.machineCPUCell(fig), layout.cpuW)
	line += " " + padStyled(m.machineMemCell(fig), layout.memW)
	if layout.showDisk {
		line += " " + padStyled(m.machineDiskCell(fig), layout.diskW)
	}
	if layout.showWorkers {
		line += " " + padStyled(m.machineWorkersCell(fig), layout.workersW)
	}
	if layout.showTop {
		line += " " + padStyled(m.machineTopCell(fig), layout.topW)
	}
	if layout.showNote && fig.note != "" {
		note := fig.note
		if ansi.StringWidth(note) > layout.noteW {
			note = ansi.Truncate(note, layout.noteW, "…")
		}
		line += " " + sDim.Render(cell(note, layout.noteW))
	}
	return clip(strings.TrimRight(line, " "), w)
}

func (m Model) machineVersionCell(r machineRow, rows []machineRow, width int) string {
	if r.version == "" {
		return strings.Repeat(" ", width)
	}
	style := sDim
	if r.online {
		for _, other := range rows {
			if other.online && versionGreater(other.version, r.version) {
				style = sNeeds
				break
			}
		}
	}
	return style.Render(cell(r.version, width))
}

func (m Model) machineFigures(r machineRow, rows []machineRow) machineFigures {
	f := machineFigures{cpu: "cpu ?", mem: "mem ?", disk: "disk ?", note: m.machineHarnessNote(r, rows)}
	if !r.online {
		return f
	}
	n, st := m.nodeUsage(r.nodeID)
	if st != usageOK {
		return f
	}
	mu := n.Usage.Machine
	f.machine = mu
	if mu.Samples > 0 && mu.Cores > 0 {
		f.cpuPct, f.knownCPU = mu.CPU, true
		f.cpu = fmt.Sprintf("cpu %s %s of %d", barText(mu.CPU, 8), fmtPct(mu.CPU), mu.Cores)
	}
	if mu.MemTotal > 0 {
		f.memPct, f.knownMem = float64(mu.MemUsed)/float64(mu.MemTotal)*100, true
		f.mem = "mem " + barText(f.memPct, 4) + " " + fmtMemPair(mu.MemUsed, mu.MemTotal)
	}
	if mu.DiskFree > 0 {
		f.diskFree = mu.DiskFree
		f.disk = "disk " + fmtMem(mu.DiskFree) + " free"
	}
	f.workerCount = 0
	for _, a := range m.snap.Agents {
		if a.NodeID == r.nodeID && a.Online && a.Worker != nil {
			f.workerCount++
		}
	}
	if n.Usage.Workers != nil {
		f.workerCount = n.Usage.Workers.Running
	}
	if f.workerCount > 0 {
		memory := "?"
		if used := m.machineWorkerMemory(r.nodeID, n.Usage.Trees); used > 0 {
			memory = fmtMem(used)
		}
		f.workers = fmt.Sprintf("%d workers · %s", f.workerCount, memory)
	}
	if name, pct, ok := m.machineTop(r.nodeID, n.Usage.Trees); ok {
		f.top = fmt.Sprintf("top: %s %s", name, fmtPct(pct))
	}
	return f
}

func barText(pct float64, width int) string {
	fraction := pct / 100
	filled := int(fraction*float64(width) + 0.5)
	filled = min(max(filled, 0), width)
	if fraction > 0 && filled == 0 {
		filled = 1
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

func machineRowWidths(rows []machineRow, figures map[string]machineFigures, w, nameW int) machineRowLayout {
	l := machineRowLayout{showDisk: true, showWorkers: true, showTop: true}
	for _, r := range rows {
		f := figures[r.nodeID]
		l.versionW = max(l.versionW, ansi.StringWidth(sanitize(r.version)))
		l.cpuW = max(l.cpuW, ansi.StringWidth(f.cpu))
		l.memW = max(l.memW, ansi.StringWidth(f.mem))
		l.diskW = max(l.diskW, ansi.StringWidth(f.disk))
		l.workersW = max(l.workersW, ansi.StringWidth(f.workers))
		l.topW = max(l.topW, ansi.StringWidth(f.top))
		l.noteW = max(l.noteW, ansi.StringWidth(f.note))
	}
	l.showNote = l.noteW > 0
	width := func() int {
		n := 3 + nameW
		if l.versionW > 0 {
			n += 1 + l.versionW
		}
		n += 1 + l.cpuW + 1 + l.memW
		if l.showDisk {
			n += 1 + l.diskW
		}
		if l.showWorkers {
			n += 1 + l.workersW
		}
		if l.showTop {
			n += 1 + l.topW
		}
		if l.showNote {
			n += 1 + l.noteW
		}
		return n
	}
	for _, drop := range []string{"top", "workers", "disk"} {
		if width() <= w {
			break
		}
		switch drop {
		case "top":
			l.showTop = false
		case "workers":
			l.showWorkers = false
		case "disk":
			l.showDisk = false
		}
	}
	if width() > w && l.showNote {
		avail := w - (width() - l.noteW)
		if avail >= 8 {
			l.noteW = avail
		} else {
			l.showNote = false
		}
	}
	if width() > w {
		nameW = max(1, nameW-(width()-w))
	}
	l.nameW = nameW
	return l
}

func (m Model) machineCPUCell(f machineFigures) string {
	if !f.knownCPU {
		return sDim.Render(f.cpu)
	}
	return "cpu " + progressBar(f.cpuPct/100, 8, loadStyle(f.cpuPct).GetForeground()) + " " + loadStyle(f.cpuPct).Render(fmtPct(f.cpuPct)) + sDim.Render(fmt.Sprintf(" of %d", f.machine.Cores))
}

func (m Model) machineMemCell(f machineFigures) string {
	if !f.knownMem {
		return sDim.Render(f.mem)
	}
	return "mem " + progressBar(f.memPct/100, 4, roleAccent) + " " + fmtMemPair(f.machine.MemUsed, f.machine.MemTotal)
}

func (m Model) machineDiskCell(f machineFigures) string {
	if f.diskFree == 0 {
		return sDim.Render(f.disk)
	}
	switch {
	case f.diskFree < 10<<30:
		return sBad.Render(f.disk)
	case f.diskFree < 20<<30:
		return sNeeds.Render(f.disk)
	default:
		return f.disk
	}
}

func (m Model) machineWorkersCell(f machineFigures) string {
	if f.workerCount == 0 {
		return ""
	}
	return f.workers
}

func (m Model) machineTopCell(f machineFigures) string {
	return f.top
}

func (m Model) machineWorkerMemory(nodeID string, trees []protocol.TreeUsage) uint64 {
	workers := map[string]bool{}
	for _, a := range m.snap.Agents {
		if a.NodeID == nodeID && a.Online && a.Worker != nil {
			workers[a.SessionID] = true
		}
	}
	seen := map[int]bool{}
	var total uint64
	for i, t := range trees {
		match := false
		for _, id := range t.Sessions {
			if workers[id] {
				match = true
				break
			}
		}
		key := t.PID
		if key <= 0 {
			key = -i - 1
		}
		if match && !seen[key] {
			total += t.RSS
			seen[key] = true
		}
	}
	return total
}

func (m Model) machineTop(nodeID string, trees []protocol.TreeUsage) (string, float64, bool) {
	byID := map[string]protocol.AgentView{}
	for _, a := range m.snap.Agents {
		if a.NodeID == nodeID && a.Online && !protocol.IsOwner(a.Harness) {
			byID[a.SessionID] = a
		}
	}
	var name string
	var pct float64
	found := false
	for _, t := range trees {
		if t.Samples == 0 {
			continue
		}
		for _, id := range t.Sessions {
			a, ok := byID[id]
			if !ok {
				continue
			}
			if !found || t.CPU > pct {
				name, pct, found = rowName(a), t.CPU, true
			}
		}
	}
	return name, pct, found
}

func (m Model) machineHarnessNote(r machineRow, rows []machineRow) string {
	if r.harnesses == nil {
		return ""
	}
	for _, h := range r.harnesses {
		if h.DeliveryWarning != "" {
			return sanitize(h.DeliveryWarning)
		}
		if h.Update != nil && h.Update.Result == "failed" {
			return sanitize(h.Name + " update failed: " + h.Update.Detail)
		}
		if h.Update != nil && h.Update.Result == "update by hand" {
			return sanitize(h.Name + " update by hand: " + h.Update.Detail)
		}
	}
	best := map[string]string{}
	present := map[string]string{}
	for _, row := range rows {
		if row.harnesses == nil {
			continue
		}
		for _, h := range row.harnesses {
			if h.Name == "agy" {
				continue
			}
			version := numberOf(h.Version)
			if current, ok := best[h.Name]; !ok || versionGreater(version, current) {
				best[h.Name] = version
			}
			if row.nodeID == r.nodeID {
				present[h.Name] = version
			}
		}
	}
	var names []string
	for name := range best {
		names = append(names, name)
	}
	sort.Strings(names)
	var notes []string
	for _, name := range names {
		local, ok := present[name]
		if !ok {
			notes = append(notes, "no "+name)
		} else if best[name] != "" && local != "" && versionGreater(best[name], local) {
			notes = append(notes, "old "+name)
		}
	}
	return strings.Join(notes, " · ")
}

func versionGreater(a, b string) bool {
	a, b = strings.TrimPrefix(a, "v"), strings.TrimPrefix(b, "v")
	if a == "" || b == "" {
		return false
	}
	aa, bb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(aa), len(bb)); i++ {
		ai, bi := 0, 0
		if i < len(aa) {
			ai, _ = strconv.Atoi(aa[i])
		}
		if i < len(bb) {
			bi, _ = strconv.Atoi(bb[i])
		}
		if ai != bi {
			return ai > bi
		}
	}
	return false
}

// padStyled pads (or cuts) a string that holds colour codes to w columns.
func padStyled(s string, w int) string {
	if ansi.StringWidth(s) > w {
		return ansi.Truncate(s, w, "")
	}
	return s + strings.Repeat(" ", w-ansi.StringWidth(s))
}

// ---- sessions table ----

// column is one column of the sessions table.
type column struct {
	name     string
	min, max int
	width    int
	right    bool
}

// columns chooses the visible columns and fits them to the available width.
func columns(avail, nameWant int) []column { return columnsFor(avail, nameWant, false) }
func columnsFor(avail, nameWant int, policy bool) []column {
	return columnsForProject(avail, nameWant, 0, policy)
}

// Every width keeps the owner's columns. Give spare room to names and
// projects before the task, so task text is the first thing to shrink.
func columnsNarrow(avail, nameWant, projectWant int) []column {
	return sessionColumns(avail, nameWant, projectWant, true)
}
func columnsForProject(avail, nameWant, projectWant int, policy bool) []column {
	return sessionColumns(avail, nameWant, projectWant, false)
}
func sessionColumns(avail, nameWant, projectWant int, narrow bool) []column {
	cols := []column{
		{name: "NAME", min: 4, max: min(max(nameWant, 18), 32)},
		{name: "MACHINE", min: 7, max: 12},
		{name: "PROJECT", min: 7, max: min(max(projectWant, 9), 16)},
		{name: "MODEL", min: 18, max: 24},
		{name: "STATUS", min: 6, max: 6},
		{name: "CPU", min: 5, max: 5, right: true},
		{name: "RAM", min: 5, max: 5, right: true},
		{name: "TASK", min: 4, max: 4000},
	}
	if narrow {
		cols[0].min, cols[0].max = 8, 14
		cols[1].max = 7
		cols[2].min = 3
		cols[3].min, cols[3].max = 12, 12
		cols[7].min = 12
	} else {
		cols[7].min = 20
	}
	for i := range cols {
		cols[i].width = cols[i].min
	}
	// Below the normal minimum, clip project and name before model text.
	deficit := max(0, sumMin(cols)-avail)
	for _, i := range []int{2, 0, 3, 1, 4, 5, 6, 7} {
		cut := min(deficit, cols[i].min-1)
		cols[i].min -= cut
		cols[i].width -= cut
		deficit -= cut
	}
	extra := max(0, avail-sumMin(cols))
	for _, i := range []int{0, 1, 2, 3, 7} {
		add := min(extra, cols[i].max-cols[i].min)
		cols[i].width += add
		extra -= add
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
	nameWant, projectWant, summaryWant := 0, 0, 0
	for _, g := range m.groups() {
		for _, a := range g.agents {
			summaryWant = max(summaryWant, ansi.StringWidth(g.marks[a.SessionID].summary))
			name := m.nameText(a, g.marks[a.SessionID], 1<<20)
			nameWant = max(nameWant, ansi.StringWidth(name))
			projectWant = max(projectWant, ansi.StringWidth(m.agentProjectName(a)))
		}
	}
	indicator := "view: " + m.sessionViewName()
	if w > 80 {
		indicator += "  (v)"
	} else {
		indicator = m.sessionViewName() + " (v)"
	}
	avail := w - 2
	statusWidth := 6
	if m.anySessionWaiting() {
		statusWidth = 9
	}
	for _, a := range m.snap.Agents {
		if a.StalledSince > 0 && a.Worker == nil {
			statusWidth = max(statusWidth, min(18, len(m.statusCell(a))))
		}
	}
	for _, a := range m.snap.Agents {
		if m.sessionNeedsOwner(a) {
			statusWidth = max(statusWidth, 9)
		}
		statusWidth = max(statusWidth, ansi.StringWidth(m.sessionStatusCell(a)))
	}
	for _, pending := range m.pendingStops {
		statusWidth = max(statusWidth, ansi.StringWidth(pending.verb))
	}
	avail -= statusWidth - 6
	var cols []column
	if w <= 80 {
		cols = columnsNarrow(avail, nameWant, projectWant)
	} else {
		cols = columnsForProject(avail, nameWant, projectWant, false)
	}

	// Preserve the entire collapsed count, including double-digit child counts.
	if summaryWant > cols[7].width {
		need := summaryWant - cols[7].width
		for _, i := range []int{0, 2, 1, 3} {
			cut := min(need, max(0, cols[i].width-1))
			cols[i].width -= cut
			cols[7].width += cut
			need -= cut
		}
	}
	if w <= 80 {
		hasDelivery := false
		for _, a := range m.snap.Agents {
			if m.sessionTaskText(a) != "" {
				hasDelivery = true
				break
			}
		}
		if hasDelivery {
			need := max(0, 12-cols[7].width)
			for _, i := range []int{3, 0, 2} {
				cut := min(need, max(0, cols[i].width-5))
				cols[i].width -= cut
				cols[7].width += cut
				need -= cut
			}
		}
	}

	for i := range cols {
		if cols[i].name == "PROJECT" && m.sessionCell == 3 {
			cols[i].name = "AGENT"
		}
		if cols[i].name == "STATUS" {
			cols[i].width = statusWidth
		}
	}
	head := "  "
	for i, c := range cols {
		if i > 0 {
			head += " "
		}
		label := c.name
		if w <= 80 && label == "DELEGATE" {
			label = "DLG"
		}
		if c.right {
			head += cellR(label, c.width)
		} else {
			head += cell(label, c.width)
		}
	}
	var out []string
	if limits := m.repoLimitsLine(w); limits != "" {
		out = append(out, limits)
	}
	out = append(out, sDim.Render(clip(head, w)))
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
			msg = "  no active sessions (v shows all, ctrl+p finds any)"
		default:
			msg = "  no sessions (H shows hidden, a archives offline)"
		}
		out = append(out, sDim.Render(msg))
	}
	top := min(m.tableTop, max(len(rows)-1, 0))
	if top > 0 && top < len(rows) && !rows[top].header {
		// The group heading has scrolled off: keep it as the first line.
		for j := top - 1; j >= 0; j-- {
			if rows[j].header {
				out = append(out, m.groupLine(rows[j].g, w))
				break
			}
		}
	}
	visibleIndex := 0
	for i := 0; i < top; i++ {
		if !rows[i].header && !rows[i].fold {
			visibleIndex++
		}
	}
	viewShown := false
	for i := top; i < len(rows) && len(out) < n+1; i++ {
		r := rows[i]
		if r.header {
			line := m.groupLine(r.g, w)
			if indicator != "" && !viewShown {
				if !r.g.first || m.waitingCount() == 0 {
					line = ansi.Cut(line, 0, max(0, w-ansi.StringWidth(indicator)-1)) + sDim.Render(" "+indicator)
				}
				viewShown = true
			}
			out = append(out, line)
		} else if r.fold {
			out = append(out, sDim.Render(clip(fmt.Sprintf("  %d more idle · v to show", r.g.hidden), w)))
		} else {
			out = append(out, m.sessionLine(r.agent, r.mark, cols, w, visibleIndex))
			visibleIndex++
		}
	}
	// A table with fewer rows than room keeps its height.
	for len(out) < n+1 {
		out = append(out, "")
	}
	return out
}

// groupLine names an urgency band and puts the Inbox count on the first one.
func (m Model) groupLine(g group, w int) string {
	head := " " + sBold.Render(fmt.Sprintf("%s (%d)", g.host, g.total))
	right := ""
	if g.first && m.waitingCount() > 0 {
		right = fmt.Sprintf("Desk: %d to decide", m.waitingCount())
	}
	room := max(0, w-ansi.StringWidth(head)-ansi.StringWidth(right))
	return clip(head+sDim.Render(strings.Repeat(" ", room))+sAccent.Render(right), w)
}

// repoLimitsLine shows network-wide counts once, above the session table.
func (m Model) repoLimitsLine(w int) string {
	type limit struct {
		name       string
		count, max int
	}
	byRepo := map[string]limit{}
	for _, a := range m.snap.Agents {
		if a.RepoMaxAgents <= 0 {
			continue
		}
		name := m.agentProjectName(a)
		key := firstNonEmpty(a.RepoContext, firstNonEmpty(a.Repo.Project, name))
		byRepo[key] = limit{name: name, count: a.RepoAgentCount, max: a.RepoMaxAgents}
	}
	if len(byRepo) == 0 {
		return ""
	}
	limits := make([]limit, 0, len(byRepo))
	for _, l := range byRepo {
		limits = append(limits, l)
	}
	sort.Slice(limits, func(i, j int) bool {
		a, b := limits[i], limits[j]
		if (a.count > a.max) != (b.count > b.max) {
			return a.count > a.max
		}
		return a.name < b.name
	})
	line := sDim.Render("agents per repo: ")
	for i, l := range limits {
		if i > 0 {
			line += sDim.Render(" · ")
		}
		line += sDim.Render(sanitize(l.name) + " ")
		count := fmt.Sprintf("%d/%d", l.count, l.max)
		if l.count > l.max {
			line += sBad.Render(count + " over max")
		} else {
			line += sDim.Render(count)
		}
	}
	return ansi.Truncate(line, w, "…")
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
	case !a.Online || a.Status == protocol.StatusDormant:
		cells["CPU"], cells["RAM"] = "\x00", "\x00"
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

// pendingTask fits the request description while preserving its age.
func (m Model) pendingTask(a protocol.AgentView, width int) string {
	if !a.Online || a.Status != "idle" || len(a.Pending) == 0 || width <= 0 {
		return ""
	}
	p := a.Pending[0]
	for _, request := range a.Pending {
		if !request.Outgoing {
			p = request
			break
		}
	}
	prefix := "waiting on "
	if !p.Outgoing {
		prefix = "owes reply to "
	}
	prefix += sanitize(p.PeerName)
	age := needsAge(m.now(), p.Since)
	suffix := " · " + age
	if ansi.StringWidth(prefix+suffix) <= width {
		return prefix + suffix
	}
	if width < ansi.StringWidth(suffix) {
		if width >= ansi.StringWidth(age) {
			return age
		}
		return ""
	}
	return strings.TrimSpace(cellDots(prefix, width-ansi.StringWidth(suffix))) + suffix
}

func (m Model) sessionLine(a protocol.AgentView, mk treeMark, cols []column, w, visibleIndex int) string {
	selected := a.SessionID == m.selected && m.selIdx == visibleIndex
	marker := "  "
	if selected {
		marker = lipgloss.NewStyle().Foreground(roleAccent).Render("▸") + " "
	}
	if a.Harness == "bot" {
		state := "offline"
		if a.Online {
			state = "online"
		}
		var parts []string
		for _, c := range cols {
			v := ""
			switch c.name {
			case "NAME":
				v = sanitize(a.Profile.Name)
			case "MACHINE":
				v = m.sessionMachine(a)
			case "TYPE", "AGENT":
				v = "bot"
			case "PROJECT", "MODEL", "TRUST", "RULE", "DELEGATE":
				v = "-"
			case "STATUS":
				v = state
			case "TASK":
				d := m.appsDate(time.Unix(a.LastSeen, 0))
				v = "last call " + strings.TrimPrefix(d, "today ")
			}
			if c.name == "TASK" {
				parts = append(parts, cellDots(v, c.width))
			} else {
				parts = append(parts, cell(v, c.width))
			}
		}
		line := marker + strings.Join(parts, " ")
		if selected {
			line = selectedRow(clip(line, w))
		}
		return clip(line, w)
	}
	uc := m.usageCells(a, cols)
	var parts []string
	for _, c := range cols {
		if v, ok := uc[c.name]; ok {
			// Keep the CPU and RAM columns even when the row is
			// offline, so every row lines up.
			if v == "\x00" {
				parts = append(parts, cell("", c.width))
			} else if v != "\x00" {
				parts = append(parts, v)
			}
			continue
		}
		var v string
		switch c.name {
		case "NAME":
			v = m.sessionNameCell(a, mk, c.width)
		case "MACHINE":
			v = m.sessionMachine(a)
		case "AGENT":
			v = a.AgentName
		case "PROJECT":
			v = m.agentProjectName(a)
		case "MODEL":
			v = a.Harness
			if model := shortModel(a.Model); model != "" {
				v += " · " + model
			}
		case "STATUS":
			if pending, ok := m.pendingStops[a.SessionID]; ok {
				v = pending.verb
			} else if m.hiddenSessions[a.SessionID] && m.showHiddenSessions {
				v = "hidden"
			} else {
				v = m.sessionStatusCell(a)
				if m.sessionNeedsOwner(a) {
					v = "needs you"
				}
			}
		case "TRUST":
			v = m.requestRuleBadge(a)
		case "RULE":
			v = m.wildcardRule(firstNonEmpty(a.Repo.Project, a.Profile.Project))
		case "DELEGATE":
			v = "-"
			if a.Delegation != nil && a.Delegation.Delegate != "" {
				v = a.Delegation.Delegate
			}
		case "TASK":
			v = taskText(a)
			if pending := m.pendingTask(a, c.width); pending != "" {
				v = pending
			}
			if delivery := m.sessionTaskText(a); delivery != "" {
				v = delivery
			}
			if a.Online && a.Limited(m.now()) {
				// The provider usage limit ends at this time (UTC in the
				// receipt line, the owner's zone here).
				v = strings.TrimSpace("until " + m.displayTime(a.LimitedUntil).Format("15:04") + " " + v)
			}
			if a.ToolsDisconnected() {
				// The session lost its agent_* tools; the detail panel says how to fix it.
				v = strings.TrimSpace("! no tools " + v)
			}
			if mk.summary != "" {
				room := max(0, c.width-ansi.StringWidth(mk.summary)-1)
				v = strings.TrimSpace(cellDots(v, room))
				if v != "" {
					v += " "
				}
				v += mk.summary
			}
			if !a.Online {
				v = offlineAge(m.now(), a.LastSeen)
			}
		}
		if c.name != "TASK" {
			v = dashIfEmpty(v)
		}
		if c.name == "NAME" {
			// sessionNameCell already fitted and styled the role tag.
		} else if c.name == "TASK" || c.name == "PROJECT" || c.name == "AGENT" || c.name == "MODEL" {
			v = cellDots(v, c.width)
		} else {
			v = cell(v, c.width)
		}
		if a.Online {
			switch c.name {
			case "STATUS":
				v = statusLabel(v)
			case "AGENT", "TASK":
				v = sDim.Render(v)
			case "TRUST", "RULE", "DELEGATE":
				value := strings.TrimSpace(v)
				if value == "-" || c.name == "RULE" && value == "normal" {
					v = sDim.Render(v)
				}
			}
		}
		if selected && ((m.sessionCell == 0 && c.name == "NAME") || (m.sessionCell == 1 && c.name == "RULE") || (m.sessionCell == 2 && c.name == "DELEGATE") || (m.sessionCell == 3 && c.name == "AGENT")) {
			v = sSel.Render(stripStyles(v))
		}
		parts = append(parts, v)
	}
	line := marker + strings.Join(parts, " ")
	switch {
	case m.pendingStops[a.SessionID].verb != "":
		return sDim.Render(clip(stripStyles(line), w))
	case selected:
		return selectedRow(clip(line, w))
	case m.hiddenSessions[a.SessionID]:
		return sDim.Render(clip(stripStyles(line), w))
	case a.StalledSince == 0 && (!a.Online || a.Status == protocol.StatusDormant || m.idleLong(a)):
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

// sessionMachine is the short host label used in the machine column.
func (m Model) sessionMachine(a protocol.AgentView) string {
	host := sanitize(m.nodeName(a.NodeID, a.Hostname))
	return strings.Split(host, ".")[0]
}

// sessionStatusCell is the STATUS cell: the short status text, and behind it
// the counts of what waits for the session. The age of an offline session
// moves to TASK, so every state fits the STATUS column.
func (m Model) sessionStatusCell(a protocol.AgentView) string {
	s := m.sessionStatusBase(a)
	if a.GatewayHeld > 0 || a.InSession > 0 {
		s += " gw " + strconv.Itoa(a.GatewayHeld) + " · in " + strconv.Itoa(a.InSession)
	}
	return s
}

func (m Model) sessionStatusBase(a protocol.AgentView) string {
	switch {
	case a.Worker == nil && a.StalledSince > 0:
		return "stalled"
	case !a.Online:
		return "offline"
	case a.Limited(m.now()):
		return "limited"
	case a.Status == protocol.StatusDormant:
		return "dormant"
	}
	if s := waitingStatus(a); s != "" {
		return s
	}
	if a.Status == "idle" && len(a.Pending) > 0 {
		for _, p := range a.Pending {
			if !p.Outgoing {
				return "owes"
			}
		}
		return "wait"
	}
	return statusShort(a.Status)
}

func offlineAge(now time.Time, lastSeen int64) string {
	if lastSeen <= 0 {
		return "no report"
	}
	age := needsAge(now, lastSeen)
	if age == "now" {
		return "just now"
	}
	return age + " ago"
}

// taskText is the TASK cell: the task the session announced. A worker without
// an announcement shows the first line of the task its owner gave it. Any
// other session falls back to its harness title when that says something the
// NAME does not. It is blank, not a dash, when there is nothing to say.
func taskText(a protocol.AgentView) string {
	if a.Harness == "cto" {
		return "" // the CTO has a fixed brief, not an announced task
	}
	if t := strings.TrimSpace(a.Profile.TaskLine()); t != "" {
		return sanitize(t)
	}
	if a.Worker != nil {
		if t := strings.TrimSpace(a.Worker.Title); t != "" && t != strings.TrimSpace(rowName(a)) {
			return sanitize(t)
		}
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

// nameText builds a plain session name with its tree, pin and role marks.
func (m Model) nameText(a protocol.AgentView, mk treeMark, w int) string {
	name := rowName(a)
	if a.Harness == "cto" {
		name = m.ctoTitle()
	}
	mark := modeMark(a)
	lead := ""
	if leadWidth(a) > 0 && a.PinnedAt == 0 {
		lead = "★ "
	}
	pin := ""
	if a.PinnedAt > 0 {
		pin = "★ "
	}
	suffix := ""
	if a.Harness == "cto" {
		suffix = " cto"
	} else if isMerger(a) {
		if !strings.HasSuffix(name, "merger") {
			suffix = " merger"
		}
	}
	room := w - ansi.StringWidth(mk.prefix) - ansi.StringWidth(pin) - ansi.StringWidth(mark) - ansi.StringWidth(lead) - ansi.StringWidth(suffix)
	if room >= 4 && utf8.RuneCountInString(name) > room {
		name = ansi.Truncate(name, room, "…")
	}
	return mk.prefix + pin + mark + lead + name + suffix
}

// sessionNameCell applies role colour after sanitizing and fitting plain text.
func (m Model) sessionNameCell(a protocol.AgentView, mk treeMark, w int) string {
	if a.Loop != nil && a.Worker == nil && w > 6 {
		// A small mark after the name says the agent has a standing loop.
		inner := strings.TrimRight(m.sessionNameCellPlain(a, mk, w-2), " ")
		fitted := inner + sDim.Render(" ↻")
		return fitted + strings.Repeat(" ", max(0, w-ansi.StringWidth(fitted)))
	}
	return m.sessionNameCellPlain(a, mk, w)
}

func (m Model) sessionNameCellPlain(a protocol.AgentView, mk treeMark, w int) string {
	name := m.nameText(a, mk, 1<<20)
	tag := ""
	if a.Harness == "cto" {
		tag = " cto"
	} else if isMerger(a) {
		tag = " merger"
	}
	if tag == "" {
		return cellDots(name, w)
	}
	name = strings.TrimSuffix(name, tag)
	if w <= ansi.StringWidth(tag) {
		return cellDots(name, w)
	}
	fitted := strings.TrimRight(cellDots(name, w-ansi.StringWidth(tag)), " ") + sDim.Render(tag)
	return fitted + strings.Repeat(" ", max(0, w-ansi.StringWidth(fitted)))
}

func (m Model) leadLive(a protocol.AgentView) bool {
	if a.Online {
		return true
	}
	if a.AgentID != "" {
		for _, other := range m.snap.Agents {
			if other.AgentID == a.AgentID && other.Online {
				return true
			}
		}
	}
	return false
}

func leadWidth(a protocol.AgentView) int {
	if a.RepoLeadExplicit || a.TeamLead {
		return 2
	}
	return 0
}

func repoLimitCardText(count, maxAgents int) string {
	if maxAgents <= 0 {
		return ""
	}
	text := fmt.Sprintf("agents %d/%d", count, maxAgents)
	if count > maxAgents {
		text += " · over max"
	}
	return text
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
	if a.Harness == "cto" {
		return "fixed by agent-net"
	}
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
	if a.Harness == "cto" {
		project = protocol.CTONodeName
	}
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
	out = append(out, m.messageSubtabs(w))
	out = append(out, sDim.Render(clip(m.messageLegend(), w)))
	rows := m.messageConversations()
	selected := m.selectedConversationIndex(rows)
	listW := w - 4
	chatW := w - 4
	if w >= 100 {
		// The list is narrower than before; the conversation gets the room.
		listW = min(max(w*45/100-4, 30), 72)
		chatW = max(w-(listW+4)-4, 1)
	}
	var chat []string
	if len(rows) > 0 {
		chat = m.conversationChat(rows[selected], chatW)
		header := m.threadParticipants(rows[selected])
		if header != "" {
			var head []string
			for _, line := range wrap(header, chatW) {
				head = append(head, sDim.Render(line))
			}
			chat = append(head, chat...)
		}
		if links := m.threadLinks(rows[selected]); len(links) > 0 {
			labels := make([]string, 0, len(links))
			for _, link := range links {
				labels = append(labels, linkLabel(link))
			}
			chat = append([]string{sDim.Render("links: " + strings.Join(labels, " · "))}, chat...)
		}
	} else {
		chat = []string{sDim.Render("Select a thread")}
	}
	chatSelected := 0
	for i, line := range chat {
		if strings.HasPrefix(ansi.Strip(line), "▸ ") {
			chatSelected = min(i+1, len(chat)-1)
			break
		}
	}
	footer := m.messageFooter(rows)
	if w >= 100 {
		height := max(h-4, 0)
		list, listSel := m.threadListLines(rows, listW, selected, max(height-1, 0))
		if len(list) == 0 {
			list = m.emptyThreadLines(listW)
		}
		list = messageFit(list, max(height-1, 0), listSel)
		list = append(list, sDim.Render(clip(footer, listW)))
		chat = messageFit(chat, height, chatSelected)
		left := panelWithRightHeader("Threads", m.messageFilterLine(), list, listW+4, roleAccent)
		right := panel("Conversation", chat, chatW+4, roleAccent)
		for i := range left {
			out = append(out, left[i]+right[i])
		}
		return out
	}
	available := max(h-6, 0)
	listHeight := max(available/2, 1)
	chatHeight := max(available-listHeight, 0)
	if len(chat) < chatHeight {
		chatHeight = len(chat)
	}
	list, listSel := m.threadListLines(rows, listW, selected, max(listHeight-1, 0))
	if len(list) == 0 {
		list = m.emptyThreadLines(listW)
	}
	list = messageFit(list, max(listHeight-1, 0), listSel)
	if listHeight > 0 {
		list = append(list, sDim.Render(clip(footer, listW)))
	}
	chat = messageFit(chat, chatHeight, chatSelected)
	out = append(out, panelWithRightHeader("Threads", m.messageFilterLine(), list, w, roleAccent)...)
	out = append(out, panel("Conversation", chat, w, roleAccent)...)
	return out
}

// threadListLines draws the rows around the selection and, for an expanded
// worker run, up to four of its messages under the row. Work stays
// proportional to the rows on screen.
func (m Model) threadListLines(rows []messageConversation, w, selected, height int) ([]string, int) {
	if height < 1 {
		height = 1
	}
	start := max(selected-height, 0)
	end := min(start+height+4, len(rows))
	start = max(end-(height+4), 0)
	lines := []string{}
	sel := 0
	for i := start; i < end; i++ {
		r := rows[i]
		if i == selected {
			sel = len(lines)
		}
		lines = append(lines, m.messageConversationLine(r, w, i == selected && m.focus == focusFeed))
		if m.threadExpanded[r.id] && r.worker {
			shown := 0
			for _, idx := range r.items {
				if shown >= 4 {
					break
				}
				it := m.feedItemAt(idx)
				if it == nil {
					continue
				}
				body := firstNonEmpty(it.Snippet, firstNonEmpty(it.Body, m.missingMessageBody(it)))
				for _, line := range wrap("    "+body, w) {
					lines = append(lines, sDim.Render(line))
					shown++
				}
			}
		}
	}
	return lines, sel
}

// emptyThreadLines is the empty list's one message.
func (m Model) emptyThreadLines(w int) []string {
	empty := "No threads"
	if m.feed.unavailable != "" {
		empty = m.feed.unavailable
	}
	var out []string
	for _, line := range wrap(empty, w) {
		out = append(out, sDim.Render(line))
	}
	return out
}

// messageSubtabs is the line under the title, switched like Quotas.
func (m Model) messageSubtabs(w int) string {
	labels := []string{"Agents", "Workers", "System"}
	var parts []string
	for i, label := range labels {
		if messageSubtab(i) == m.messageSubtab {
			parts = append(parts, sActiveTab.Render("[ "+label+" ]"))
		} else {
			parts = append(parts, sDim.Render("  "+label+"  "))
		}
	}
	return clip(strings.Join(parts, "  "), w)
}

// messageLegend explains the compact marks on one dim line.
func (m Model) messageLegend() string {
	return "you→ owner · ~ waiting · ✓ answered · gw held · ⚡ burst · ! blocked"
}

// messageFilterLine is the panel's right header: the active filters and the
// search key.
func (m Model) messageFilterLine() string {
	project := firstNonEmpty(m.threadProject, "all")
	team := firstNonEmpty(m.threadTeam, "all")
	search := " / search"
	if m.uiFilter != "" || m.filter != "" {
		search = " / " + firstNonEmpty(m.uiFilter, m.filter)
	}
	return "p: " + project + " · t: " + team + search
}

// messageFooter is the list's last line: the row count and, in the System
// sub-tab, the non-message events.
func (m Model) messageFooter(rows []messageConversation) string {
	word := "threads"
	if len(rows) == 1 {
		word = "thread"
	}
	footer := fmt.Sprintf("%d %s", len(rows), word)
	eventCount := m.systemEventCount()
	eventWord := "events"
	if eventCount == 1 {
		eventWord = "event"
	}
	footer += fmt.Sprintf(" · %d system %s", eventCount, eventWord)
	if len(m.threadExpanded) > 0 {
		footer += " · e collapse"
	}
	return footer
}

func messageFit(lines []string, height, selected int) []string {
	height = max(height, 0)
	start := 0
	if selected >= height {
		start = selected - height + 1
	}
	start = min(start, max(len(lines)-height, 0))
	end := min(start+height, len(lines))
	lines = append([]string(nil), lines[start:end]...)
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines
}

// sessionTaskText shows idle compaction or the claimant for a running step.
func (m Model) sessionTaskText(a protocol.AgentView) string {
	if a.IdleCompactedAt > 0 {
		return "compacted while idle " + m.displayTime(a.IdleCompactedAt).Format("15:04")
	}
	for _, ch := range m.delivery.changes {
		for _, step := range ch.Steps {
			if step.State != protocol.StepStateRunning {
				continue
			}
			for _, note := range strings.Split(step.Note, "; ") {
				if strings.TrimPrefix(note, "claimed by ") == a.SessionID && strings.HasPrefix(note, "claimed by ") {
					return step.Name + " " + ch.ID[:min(len(ch.ID), 7)]
				}
			}
		}
	}
	return ""
}
