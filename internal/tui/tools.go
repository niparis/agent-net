package tui

import (
	"context"
	"fmt"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolsdiff"
)

// The Tools tab opens one table of skills by project and switches each cell on
// or off. The all-tools list stays reachable for MCP management.
// Changes use owner-only daemon methods and the existing plan/apply services.
// Legacy catalog helpers remain shared with comparison and backend tests.

// ToolsSource is what the Tools tab needs from the daemon. The operator client
// has these methods; a source without them shows a note instead of the tab.
type ToolsSource interface {
	ToolsItems(context.Context) ([]protocol.ToolItem, error)
	ToolsProjects(context.Context) ([]protocol.ToolProjectState, error)
	ToolsToggle(context.Context, protocol.ToolToggleRequest) (protocol.ToolPlan, error)
	ToolsNewProjects(context.Context, protocol.ToolNewProjectsRequest) (protocol.ToolPlan, error)
	ToolsAccepted(context.Context) ([]string, error)
	ToolsAccept(ctx context.Context, key string, accepted bool) error
	ToolsNeeds(context.Context) ([]protocol.ToolNeed, error)
	ToolsPlan(context.Context, protocol.ToolPlanRequest) (protocol.ToolPlan, error)
	ToolsApply(ctx context.Context, planID string) (protocol.ToolApplyResult, error)
	ToolsImport(context.Context, protocol.ToolImport) error
	ToolsSkillDetails(context.Context, protocol.ToolSkillScan) (protocol.ToolSkillDetails, error)
	ToolsRescan(context.Context) error
	MCPCandidates(context.Context) ([]protocol.MCPImportCandidate, error)
	MCPImportPlan(ctx context.Context, candidate string) (protocol.MCPImportPlan, error)
	MCPImportApply(ctx context.Context, planID string) (protocol.MCPImportResult, error)
	MCPNativeRemovePlan(ctx context.Context, candidate string) (protocol.MCPNativeRemovePlan, error)
	MCPNativeRemoveApply(ctx context.Context, planID string) (protocol.MCPNativeRemoveResult, error)
	MCPCoveragePlan(ctx context.Context, req protocol.MCPCoverageRequest) (protocol.MCPCoveragePlan, error)
	MCPCoverageApply(ctx context.Context, planID string) (protocol.MCPCoverageResult, error)
	MCPCoverageUndo(ctx context.Context, planID string) (protocol.MCPCoverageResult, error)
}

// toolsMsg is one answer to a Tools tab read.
type toolsMsg struct {
	items    []protocol.ToolItem
	states   []protocol.ToolProjectState
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
	states          []protocol.ToolProjectState
	accepted        map[string]bool
	needs           []protocol.ToolNeed
	cat             toolsdiff.Catalog

	// The skill table. row is the selected skill, col the selected project
	// column. col == len(projectCols) is the last column, "new projects".
	row, col    int
	colScroll   int
	costCol     int
	scroll      int
	projectCols []string
	lockReasons map[string]string
	summaries   map[string]string

	// allTools is the MCP and all-tools list, reached with m. It is kept
	// because MCP management is unchanged by decision 0048.
	allTools bool

	search    string // the skill name filter
	searching bool   // the filter text box has the keys

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
func (t toolsState) capturing() bool {
	return t.searching || t.modal != nil
}

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
	m.tl.allTools = false
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
			r.states, _ = ts.ToolsProjects(ctx)
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
	m.tl.items, m.tl.needs, m.tl.states = r.items, r.needs, r.states
	m.tl.accepted = map[string]bool{}
	for _, k := range r.accepted {
		m.tl.accepted[k] = true
	}
	m.rebuildTools()
}

// rebuildTools computes the catalog again from the items and the machines.
func (m *Model) rebuildTools() {
	m.tl.cat = toolsdiff.Build(m.tl.items, toolsdiff.MachinesOf(m.snap.Nodes))
	m.rebuildTable()
	m.toolsClamp()
}

// rebuildTable takes the project columns from the on/off API and keeps the
// selection inside the table.
func (m *Model) rebuildTable() {
	seen := map[string]bool{}
	var cols []string
	for _, st := range m.tl.states {
		for name := range st.Projects {
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			cols = append(cols, name)
		}
	}
	if len(cols) == 0 {
		cols = m.toolsProjects()
	}
	sort.Strings(cols)
	m.tl.projectCols = cols
	m.tableClamp()
}

// tableClamp keeps the row and column selection inside the table.
func (m *Model) tableClamp() {
	rows := m.skillRows()
	m.tl.row = min(max(m.tl.row, 0), max(len(rows)-1, 0))
	m.tl.col = min(max(m.tl.col, 0), max(len(m.tl.projectCols), 0))
	m.tl.colScroll = min(max(m.tl.colScroll, 0), max(len(m.tl.projectCols)-1, 0))
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

// shownDiffs is the differences that still need a choice, one per tool. It
// feeds the fix actions; it is not a screen of its own.
func (m Model) shownDiffs() []toolsdiff.Difference {
	var out []toolsdiff.Difference
	for _, d := range m.tl.cat.Diffs {
		if m.tl.accepted[d.Key] {
			continue
		}
		out = append(out, d)
	}
	return out
}

// shownRows is one row per name and kind, with attention first.
func (m Model) shownRows() []toolsdiff.Row {
	var out []toolsdiff.Row
	seen := map[string]int{}
	for _, r := range m.tl.cat.Rows {
		key := r.Kind + ":" + r.Name
		if i, ok := seen[key]; ok {
			for _, project := range r.Projects {
				exists := false
				for _, current := range out[i].Projects {
					exists = exists || current.Name == project.Name
				}
				if !exists {
					out[i].Projects = append(out[i].Projects, project)
				}
			}
			continue
		}
		seen[key] = len(out)
		out = append(out, r)
	}
	filtered := out[:0]
	for _, r := range out {
		if m.toolMatches(r) {
			filtered = append(filtered, r)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool { return m.toolAttention(filtered[i]) && !m.toolAttention(filtered[j]) })
	return filtered
}

func (m Model) toolsRowCount() int { return len(m.shownRows()) }

func (m *Model) toolsClamp() {
	n := m.toolsRowCount()
	m.tl.row = min(max(m.tl.row, 0), max(n-1, 0))
}

func (m Model) selectedRow() (toolsdiff.Row, bool) {
	rows := m.skillRows()
	if m.tl.allTools {
		rows = m.shownRows()
	}
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
	if m.tl.allTools {
		return m.toolsListKey(k)
	}
	return m.tableKey(k)
}

// toolsListKey keeps the MCP and all-tools list reachable. The table is the
// opening screen; this list remains because MCP management is unchanged.
func (m Model) toolsListKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.tl.searching {
		return m.toolsListSearchKey(k)
	}
	m.tl.toast = ""
	switch keyAction(contextTools, k.String()) {
	case "all tools":
		m.tl.allTools = false
		return m, nil
	case actQuit:
		m.quitting = true
		return m, tea.Quit
	case "help":
		m.openHelp()
	case "rescan":
		return m.toolsRescan()
	case actDown:
		m.tl.row++
	case actUp:
		m.tl.row--
	case "filter":
		m.tl.searching = true
	case "clear filter":
		m.tl.search = ""
	case "open actions":
		return m.openToolMenu()
	case "add":
		return m.openToolAdd()
	}
	m.toolsClamp()
	return m, nil
}

func (m Model) toolsListSearchKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	return m.toolsSearchKey(k)
}

func (m Model) toolsSearchKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	target := &m.tl.search
	switch keyAction(contextToolsFilter, k.String()) {
	case "keep":
		m.tl.searching = false
	case "clear filter":
		if *target != "" {
			*target = ""
		} else {
			m.tl.searching = false
		}
	case "delete text":
		r := []rune(*target)
		if len(r) > 0 {
			*target = string(r[:len(r)-1])
		}
	default:
		if k.Type == tea.KeyRunes || k.Type == tea.KeySpace {
			*target += string(k.Runes)
		}
	}
	m.tl.row, m.tl.col, m.tl.colScroll = 0, 0, 0
	if m.tl.allTools {
		m.toolsClamp()
	} else {
		m.tableClamp()
	}
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
			r.states, _ = ts.ToolsProjects(ctx)
			r.accepted, _ = ts.ToolsAccepted(ctx)
			r.needs, _ = ts.ToolsNeeds(ctx)
		}
		return r
	}
}

func nounCount(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
