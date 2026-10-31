package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolsdiff"
)

// The Tools tab (decision 0027, step 8). Three screens: what differs, all tools
// (one column per machine), and what one project gets on one machine. A skill
// is on or off for a machine, or on for chosen projects; the tab never shows
// harness columns. Every change opens a plan first. The tab reads and acts
// through the owner-only daemon methods, in the same way as the CLI and the CTO.

// ToolsSource is what the Tools tab needs from the daemon. The operator client
// has these methods; a source without them shows a note instead of the tab.
type ToolsSource interface {
	ToolsItems(context.Context) ([]protocol.ToolItem, error)
	ToolsAccepted(context.Context) ([]string, error)
	ToolsAccept(ctx context.Context, key string, accepted bool) error
	ToolsNeeds(context.Context) ([]protocol.ToolNeed, error)
	ToolsPlan(context.Context, protocol.ToolPlanRequest) (protocol.ToolPlan, error)
	ToolsApply(ctx context.Context, planID string) (protocol.ToolApplyResult, error)
	ToolsImport(context.Context, protocol.ToolImport) error
	ToolsRescan(context.Context) error
	MCPCandidates(context.Context) ([]protocol.MCPImportCandidate, error)
	MCPImportPlan(ctx context.Context, candidate string) (protocol.MCPImportPlan, error)
	MCPImportApply(ctx context.Context, planID string) (protocol.MCPImportResult, error)
	MCPNativeRemovePlan(ctx context.Context, candidate string) (protocol.MCPNativeRemovePlan, error)
	MCPNativeRemoveApply(ctx context.Context, planID string) (protocol.MCPNativeRemoveResult, error)
}

const (
	toolsDiffs   = 0
	toolsAll     = 1
	toolsProject = 2
	toolsTab     = 4 // the tab index: "5 Tools"
)

var toolsScreenNames = []string{"Differences", "All tools", "By project"}

type toolsMsg struct {
	items    []protocol.ToolItem
	accepted []string
	needs    []protocol.ToolNeed
	err      error
	at       time.Time
}

// toolsState is the Tools tab's part of the model.
type toolsState struct {
	loaded, loading bool
	err             string
	at              time.Time
	items           []protocol.ToolItem
	accepted        map[string]bool
	needs           []protocol.ToolNeed
	cat             toolsdiff.Catalog

	screen       int
	row, col     int
	scroll       int
	showAccepted bool

	search    string // filter of the All tools screen
	projSel   string // the project of the By project screen
	projFind  string // filter of the project list
	machine   int    // the machine of the By project screen
	searching bool   // the text box for search or projFind has the keys

	toast   string
	toastAt time.Time
	modal   *toolsModal
	// nextModal numbers the modals, so a late answer for a closed one is dropped.
	nextModal int
}

func (m Model) toolsSource() (ToolsSource, bool) {
	ts, ok := m.src.(ToolsSource)
	return ts, ok
}

// capturing says the Tools tab needs every key: a text box or a plan is open.
func (t toolsState) capturing() bool { return t.searching || t.modal != nil }

func (m *Model) toolsToast(s string) {
	m.tl.toast, m.tl.toastAt = s, m.now()
}

func (m Model) activeToast() string {
	if m.tl.toast != "" && m.now().Sub(m.tl.toastAt) < 6*time.Second {
		return m.tl.toast
	}
	return ""
}

// startTools opens the tab and reads the tools.
func (m Model) startTools() (tea.Model, tea.Cmd) {
	m.tab, m.mapOn, m.focus = toolsTab, false, focusSessions
	m.uiFilter = ""
	if m.tl.loading {
		return m, nil
	}
	return m.loadTools()
}

func (m Model) loadTools() (tea.Model, tea.Cmd) {
	ts, ok := m.toolsSource()
	if !ok {
		m.tl.err = "this daemon cannot list tools"
		return m, nil
	}
	m.tl.loading = true
	now := m.now
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*Timeout)
		defer cancel()
		r := toolsMsg{at: now()}
		r.items, r.err = ts.ToolsItems(ctx)
		if r.err == nil {
			r.accepted, _ = ts.ToolsAccepted(ctx)
			r.needs, _ = ts.ToolsNeeds(ctx)
		}
		return r
	}
}

func (m *Model) applyTools(r toolsMsg) {
	m.tl.loading = false
	if r.err != nil {
		m.tl.err = r.err.Error()
		if isUnavailable(r.err) {
			m.tl.err = "the daemon or the gateway is older than the Tools tab; upgrade it"
		}
		return
	}
	m.tl.err, m.tl.loaded, m.tl.at = "", true, r.at
	m.tl.items, m.tl.needs = r.items, r.needs
	m.tl.accepted = map[string]bool{}
	for _, k := range r.accepted {
		m.tl.accepted[k] = true
	}
	m.rebuildTools()
}

// rebuildTools computes the catalog again from the items and the machines.
func (m *Model) rebuildTools() {
	m.tl.cat = toolsdiff.Build(m.tl.items, toolsdiff.MachinesOf(m.snap.Nodes))
	if m.tl.machine >= len(m.tl.cat.Machines) {
		m.tl.machine = 0
	}
	if m.tl.projSel == "" || !hasStr(m.toolsProjects(), m.tl.projSel) {
		if ps := m.toolsProjects(); len(ps) > 0 {
			m.tl.projSel = ps[0]
		}
	}
	m.toolsClamp()
}

func hasStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// toolsProjects lists the projects the picker offers: registered or seen
// projects, plus any project a tool names.
func (m Model) toolsProjects() []string {
	seen := map[string]bool{}
	for _, p := range m.snap.Projects {
		if p.Name != "" {
			seen[p.Name] = true
		}
	}
	for _, r := range m.tl.cat.Rows {
		for _, p := range r.Projects {
			seen[p.Name] = true
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// shownProjects is the project list after the filter of screen 3.
func (m Model) shownProjects() []string {
	q := strings.ToLower(m.tl.projFind)
	var out []string
	for _, p := range m.toolsProjects() {
		if q == "" || strings.Contains(strings.ToLower(p), q) {
			out = append(out, p)
		}
	}
	return out
}

// shownDiffs is the Differences list: accepted ones only when asked for.
func (m Model) shownDiffs() []toolsdiff.Difference {
	var out []toolsdiff.Difference
	for _, d := range m.tl.cat.Diffs {
		if m.tl.accepted[d.Key] && !m.tl.showAccepted {
			continue
		}
		out = append(out, d)
	}
	return out
}

// shownRows is the All tools list after the search.
func (m Model) shownRows() []toolsdiff.Row {
	q := strings.ToLower(m.tl.search)
	var out []toolsdiff.Row
	for _, r := range m.tl.cat.Rows {
		if q == "" || strings.Contains(strings.ToLower(r.Name), q) || strings.Contains(strings.ToLower(r.From), q) {
			out = append(out, r)
		}
	}
	return out
}

func (m Model) toolsRowCount() int {
	switch m.tl.screen {
	case toolsDiffs:
		return len(m.shownDiffs())
	case toolsAll:
		return len(m.shownRows())
	}
	return len(m.shownProjects())
}

func (m *Model) toolsClamp() {
	n := m.toolsRowCount()
	m.tl.row = min(max(m.tl.row, 0), max(n-1, 0))
	m.tl.col = min(max(m.tl.col, 0), len(m.tl.cat.Machines))
	if m.tl.screen == toolsProject {
		ps := m.shownProjects()
		if len(ps) > 0 && !hasStr(ps, m.tl.projSel) {
			m.tl.projSel = ps[0]
		}
	}
}

func (m *Model) toolsSetScreen(s int) {
	m.tl.screen = (s + len(toolsScreenNames)) % len(toolsScreenNames)
	m.tl.row, m.tl.scroll = 0, 0
	m.tl.searching = false
	m.toolsClamp()
}

func (m Model) selectedRow() (toolsdiff.Row, bool) {
	rows := m.shownRows()
	if m.tl.row < 0 || m.tl.row >= len(rows) {
		return toolsdiff.Row{}, false
	}
	return rows[m.tl.row], true
}

// ---- keys ----

func (m Model) toolsKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.tl.modal != nil {
		return m.toolsModalKey(k)
	}
	if m.tl.searching {
		return m.toolsSearchKey(k)
	}
	key := k.String()
	m.tl.toast = ""
	switch key {
	case "q":
		m.quitting = true
		return m, tea.Quit
	case "?":
		m.help = true
		return m, nil
	case "M":
		return m.startMap()
	case "]":
		m.toolsSetScreen(m.tl.screen + 1)
	case "[":
		m.toolsSetScreen(m.tl.screen - 1)
	case "down", "j":
		if m.tl.screen == toolsProject {
			m.toolsMoveProject(1)
		} else {
			m.tl.row++
		}
	case "up", "k":
		if m.tl.screen == toolsProject {
			m.toolsMoveProject(-1)
		} else {
			m.tl.row--
		}
	case "right":
		if m.tl.screen != toolsAll {
			m.toolsSetScreen(m.tl.screen + 1)
		} else {
			m.tl.col++
		}
	case "left":
		if m.tl.screen != toolsAll {
			m.toolsSetScreen(m.tl.screen - 1)
		} else {
			m.tl.col--
		}
	case "l":
		m.tl.col++
	case "h":
		m.tl.col--
	case "m":
		if n := len(m.tl.cat.Machines); n > 0 {
			m.tl.machine = (m.tl.machine + 1) % n
		}
	case "/":
		m.tl.searching = true
		if m.tl.screen == toolsDiffs {
			m.toolsSetScreen(toolsAll)
			m.tl.searching = true
		}
		return m, nil
	case "esc":
		m.tl.search, m.tl.projFind = "", ""
	case "r":
		return m.toolsRescan()
	case ".":
		if m.tl.screen == toolsDiffs {
			m.tl.showAccepted = !m.tl.showAccepted
		}
	case "x":
		if m.tl.screen == toolsDiffs {
			return m.toolsAccept()
		}
	case "enter":
		switch m.tl.screen {
		case toolsDiffs:
			return m.openFix()
		case toolsAll:
			m.toolsToast("details are below the list")
			return m, nil
		}
	case "a", "u":
		switch m.tl.screen {
		case toolsAll:
			return m.openRowPlan(key == "a")
		case toolsProject:
			if key == "u" {
				m.toolsToast("go to All tools ([ or ]), pick the item, then press u: it opens the same plan")
			}
		}
	}
	m.toolsClamp()
	return m, nil
}

func (m *Model) toolsMoveProject(d int) {
	ps := m.shownProjects()
	if len(ps) == 0 {
		return
	}
	i := 0
	for j, p := range ps {
		if p == m.tl.projSel {
			i = j
		}
	}
	m.tl.projSel = ps[min(max(i+d, 0), len(ps)-1)]
}

func (m Model) toolsSearchKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	target := &m.tl.search
	if m.tl.screen == toolsProject {
		target = &m.tl.projFind
	}
	switch k.Type {
	case tea.KeyEsc:
		*target = ""
		m.tl.searching = false
	case tea.KeyEnter:
		m.tl.searching = false
	case tea.KeyBackspace, tea.KeyCtrlH:
		r := []rune(*target)
		if len(r) > 0 {
			*target = string(r[:len(r)-1])
		}
	case tea.KeyRunes, tea.KeySpace:
		*target += string(k.Runes)
	}
	m.tl.row, m.tl.scroll = 0, 0
	m.toolsClamp()
	return m, nil
}

func (m Model) toolsRescan() (tea.Model, tea.Cmd) {
	ts, ok := m.toolsSource()
	if !ok || m.tl.loading {
		return m, nil
	}
	m.tl.loading = true
	now := m.now
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*Timeout)
		defer cancel()
		_ = ts.ToolsRescan(ctx) // an older daemon cannot rescan: read what the gateway has
		r := toolsMsg{at: now()}
		r.items, r.err = ts.ToolsItems(ctx)
		if r.err == nil {
			r.accepted, _ = ts.ToolsAccepted(ctx)
			r.needs, _ = ts.ToolsNeeds(ctx)
		}
		return r
	}
}

type toolsAcceptMsg struct {
	key      string
	accepted bool
	err      error
}

// toolsAccept accepts (or takes back) the selected difference. The gateway
// stores the mark, so it stays after a restart.
func (m Model) toolsAccept() (tea.Model, tea.Cmd) {
	ds := m.shownDiffs()
	if m.tl.row >= len(ds) {
		return m, nil
	}
	d := ds[m.tl.row]
	if d.Info {
		m.toolsToast("nothing to accept: " + d.Fix)
		return m, nil
	}
	ts, ok := m.toolsSource()
	if !ok {
		return m, nil
	}
	on := !m.tl.accepted[d.Key]
	acc := map[string]bool{}
	for k, v := range m.tl.accepted {
		acc[k] = v
	}
	if on {
		acc[d.Key] = true
		m.toolsToast("accepted: it stays as it is and leaves this list")
	} else {
		delete(acc, d.Key)
		m.toolsToast("back on the list")
	}
	m.tl.accepted = acc
	m.toolsClamp()
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		return toolsAcceptMsg{key: d.Key, accepted: on, err: ts.ToolsAccept(ctx, d.Key, on)}
	}
}

func (m *Model) onToolsAccept(r toolsAcceptMsg) {
	if r.err == nil {
		return
	}
	// The gateway did not store it: undo what the screen showed.
	acc := map[string]bool{}
	for k, v := range m.tl.accepted {
		acc[k] = v
	}
	if r.accepted {
		delete(acc, r.key)
	} else {
		acc[r.key] = true
	}
	m.tl.accepted = acc
	m.toolsToast("could not store the mark: " + r.err.Error())
}

func nounCount(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
