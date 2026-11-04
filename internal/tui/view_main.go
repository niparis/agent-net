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
		if !m.expand && m.waitingCount() > 0 {
			fixed++ // the "waiting in Inbox" line
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
	keyRows := 1
	if m.tab == toolsTab && !m.help && !m.mapOn && m.focus != focusCTO {
		keyRows = len(m.toolsKeyLines(w))
	}
	var out []string
	add := func(s string) { out = append(out, s) }

	switch {
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
		case m.mode == modeSessionForm:
			// The New session form replaces the Graph card too, so n on the
			// card keeps the Graph tab and Esc returns to the card.
			out = append(out, m.mainView(w, contentHeight)...)
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
		if n := m.waitingCount(); n > 0 && !m.expand {
			add(sAccent.Render(clip(fmt.Sprintf("%d waiting in Inbox (7)", n), w)))
		}
		for _, s := range m.sessionsTable(w, l.sessions) {
			add(s)
		}
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
	inboxTab    = 6
)

var tabNames = []string{"Sessions", "Graph", "Quotas", "Messages", "Tools", "Settings", "Inbox"}
var shortTabNames = []string{"Sess", "Graph", "Quota", "Msg", "Tools", "Set", "Inbox"}
var sActiveTab = lipgloss.NewStyle().Bold(true).Foreground(roleBase).Background(roleAccent)

// sessionViewName is the view the v key selected.
func (m Model) sessionViewName() string {
	switch {
	case m.activeOnly:
		return "active"
	case m.showHiddenSessions:
		return "offline+hidden"
	case m.showOffline:
		return "offline"
	}
	return "all"
}

func (m Model) titleLine(w int) string {

	var tabs []string
	count := m.waitingCount()
	names := tabNames
	if w <= 80 && count > 0 {
		// The pending count makes the full names run into the online
		// counter. Short names keep the separator between them.
		names = shortTabNames
	}
	fullTabs := len(strings.Join(names, " ")) + len("1 2 3 4 5 6 7 ")
	if count > 0 {
		fullTabs += len(fmt.Sprintf(" (%d)", count))
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
		line += " " + sDim.Render(cell(fig.note, layout.noteW))
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
		l.showNote = false
		l.showTop, l.showWorkers, l.showDisk = true, true, true
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
	return columnsForProject(avail, nameWant, projectWant, false)
}
func columnsForProject(avail, nameWant, projectWant int, policy bool) []column {
	cols := []column{
		{name: "NAME", min: 4, max: min(max(nameWant, 10), 24)},
		{name: "TYPE", min: 6, max: 6},
		{name: "PROJECT", min: 7, max: min(max(projectWant, 7), 16)},
		{name: "MODEL", min: 18, max: 18},
		{name: "STATUS", min: 6, max: 6},
		{name: "CPU", min: 5, max: 5, right: true},
		{name: "RAM", min: 5, max: 5, right: true},
		{name: "TASK", min: 4, max: 4000},
	}
	for i := range cols {
		cols[i].width = cols[i].min
	}
	extra := max(0, avail-sumMin(cols))
	for _, i := range []int{0, 2, 7} {
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
	nameWant, projectWant := 0, 0
	for _, g := range m.groups() {
		for _, a := range g.agents {
			nameWant = max(nameWant, utf8.RuneCountInString(m.nameText(a, g.marks[a.SessionID], 1<<20)))
			projectWant = max(projectWant, ansi.StringWidth(m.agentProjectName(a)))
		}
	}
	indicator := "view: " + m.sessionViewName()
	if w > 80 {
		indicator += "  (v)"
	}
	avail := w - 2 - ansi.StringWidth(indicator) - 2 // marker and view indicator
	statusWidth := 6
	if m.anySessionWaiting() {
		statusWidth = 9
	}
	avail -= statusWidth - 6
	var cols []column
	if w <= 80 {
		cols = columnsNarrow(avail, nameWant, projectWant)
	} else {
		cols = columnsForProject(avail, nameWant, projectWant, false)
	}
	for i := range cols {
		if cols[i].name == "STATUS" {
			cols[i].width = statusWidth
		}
	}
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
	head = padStyled(head, max(0, w-ansi.StringWidth(indicator))) + indicator
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
	visibleIndex := 0
	for i := 0; i < top; i++ {
		if !rows[i].header {
			visibleIndex++
		}
	}
	for i := top; i < len(rows) && len(out) < n+1; i++ {
		r := rows[i]
		if r.header {
			out = append(out, m.groupLine(r.g, w))
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
			if v != "\x00" {
				parts = append(parts, v)
			}
			continue
		}
		var v string
		switch c.name {
		case "NAME":
			v = m.nameText(a, mk, c.width)
		case "TYPE":
			v = protocol.SessionType(a.Harness, a.Worker, a.Type)
		case "AGENT":
			v = a.Harness
		case "PROJECT":
			v = m.agentProjectName(a)
		case "MODEL":
			v = a.Harness + " · " + shortModel(a.Model)
		case "STATUS":
			if m.hiddenSessions[a.SessionID] && m.showHiddenSessions {
				v = "hidden"
			} else {
				v = m.statusCell(a)
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
		}
		if c.name != "TASK" {
			v = dashIfEmpty(v)
		}
		if c.name == "TASK" || c.name == "NAME" || c.name == "PROJECT" || c.name == "MODEL" {
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
		if selected && ((m.sessionCell == 0 && c.name == "NAME") || (m.sessionCell == 1 && c.name == "RULE") || (m.sessionCell == 2 && c.name == "DELEGATE")) {
			v = sSel.Render(stripStyles(v))
		}
		parts = append(parts, v)
	}
	line := marker + strings.Join(parts, " ")
	switch {
	case selected:
		return selectedRow(clip(line, w))
	case m.hiddenSessions[a.SessionID]:
		return sDim.Render(clip(stripStyles(line), w))
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
	if a.Harness == "cto" {
		return "" // the CTO has a fixed brief, not an announced task
	}
	if t := strings.TrimSpace(a.Profile.Task); t != "" {
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

// nameText is the NAME cell of a session in w columns. A worker has its tree
// glyphs before the name, and "@host" after it when it runs on another machine
// than its caller. The name gives way first: it is cut with an ellipsis so the
// glyphs and the host stay.
func (m Model) nameText(a protocol.AgentView, mk treeMark, w int) string {
	name := rowName(a)
	if a.Harness == "cto" {
		name = m.ctoTitle()
	}
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
	out = append(out, sDim.Render(clip("you→: your prompt · orange: waiting for a reply · red: failed · purple: "+m.ctoTitle(), w)))
	rows := m.messageConversations()
	selected := m.selectedConversationIndex(rows)
	list := []string{}
	listW := w - 4
	chatW := w - 4
	if w >= 100 {
		listW = max(w*55/100-4, 1)
		chatW = max(w-(listW+4)-4, 1)
	}
	for i, r := range rows {
		list = append(list, m.messageConversationLine(r, listW, i == selected && m.focus == focusFeed))
	}
	if len(list) == 0 {
		empty := "No conversations"
		if m.feed.unavailable != "" {
			empty = m.feed.unavailable
		}
		for _, line := range wrap(empty, listW) {
			list = append(list, sDim.Render(line))
		}
	}
	eventCount := m.systemEventCount()
	eventWord := "events"
	if eventCount == 1 {
		eventWord = "event"
	}
	footer := fmt.Sprintf("%d system %s", eventCount, eventWord)
	var chat []string
	if len(rows) > 0 {
		chat = m.conversationChat(rows[selected], chatW)
	} else {
		chat = []string{sDim.Render("Select a conversation")}
	}
	chatSelected := 0
	for i, line := range chat {
		if strings.HasPrefix(ansi.Strip(line), "▸ ") {
			chatSelected = min(i+1, len(chat)-1)
			break
		}
	}
	if w >= 100 {
		height := max(h-4, 0)
		list = messageFit(list, max(height-1, 0), selected)
		list = append(list, sDim.Render(clip(footer, listW)))
		chat = messageFit(chat, height, chatSelected)
		left := panelWithRightHeader("Messages", "filter: "+m.messageFilterName()+"  (l)", list, listW+4, roleAccent)
		right := panel("Conversation", chat, chatW+4, roleAccent)
		for i := range left {
			out = append(out, left[i]+right[i])
		}
		return out
	}
	available := max(h-6, 0)
	listHeight := min(max((available+2)/2, 1), len(list)+1)
	chatHeight := max(available-listHeight, 0)
	list = messageFit(list, max(listHeight-1, 0), selected)
	if listHeight > 0 {
		list = append(list, sDim.Render(clip(footer, listW)))
	}
	chat = messageFit(chat, chatHeight, chatSelected)
	out = append(out, panelWithRightHeader("Messages", "filter: "+m.messageFilterName()+"  (l)", list, w, roleAccent)...)
	out = append(out, panel("Conversation", chat, w, roleAccent)...)
	return out
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
