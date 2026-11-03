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

// The Tools tab opens skills per agent and can switch to the all-tools list.
// Changes use owner-only daemon methods and the existing plan/apply services.
// Legacy catalog helpers remain shared with comparison and backend tests.

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

	allTools                     bool
	agentRow, skillRow           int
	skillsFocus                  bool
	agentFilter                  string
	agentSearching, agentDiscard bool
	agentPending                 map[string]map[string]bool
	row, scroll                  int
	projectsOpen                 bool
	projectRow                   int
	summaries                    map[string]string

	search    string // the filter of the one list
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
	return t.searching || t.modal != nil || t.agentSearching || t.agentDiscard
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
	if !m.tl.allTools {
		return m.agentToolsKey(k)
	}
	if m.tl.projectsOpen {
		projects := m.projectCosts()
		switch k.String() {
		case "esc":
			m.tl.projectsOpen = false
		case "j", "down":
			m.tl.projectRow = min(m.tl.projectRow+1, max(len(projects)-1, 0))
		case "k", "up":
			m.tl.projectRow = max(m.tl.projectRow-1, 0)
		case "enter":
			if len(projects) > 0 {
				m.tl.search = "project:" + projects[m.tl.projectRow].name
				m.tl.row, m.tl.scroll = 0, 0
				m.tl.projectsOpen = false
			}
		}
		return m, nil
	}
	if m.tl.searching {
		return m.toolsSearchKey(k)
	}
	m.tl.toast = ""
	switch keyAction(contextTools, k.String()) {
	case "skills per agent":
		m.tl.allTools = false
	case actQuit:
		m.quitting = true
		return m, tea.Quit
	case "help":
		m.openHelp()
	case "project costs":
		m.tl.projectsOpen = true
		m.tl.projectRow = 0
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

func (m Model) toolsSearchKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	target := &m.tl.search
	switch k.Type {
	case tea.KeyEsc:
		if *target != "" {
			*target = ""
		} else {
			m.tl.searching = false
		}
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

func nounCount(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
