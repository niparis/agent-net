package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolsdiff"
)

func (m Model) agentProject() string {
	ps := m.toolsProjects()
	if len(ps) == 0 {
		return ""
	}
	return ps[min(m.tl.agentRow, len(ps)-1)]
}

func (m Model) agentSkills() []toolsdiff.Row {
	// The all-tools filter belongs to that view only.
	m.tl.search = ""
	var rows []toolsdiff.Row
	for _, r := range m.shownRows() {
		if r.Kind == protocol.InventorySkill && strings.Contains(strings.ToLower(r.Name), strings.ToLower(m.tl.agentFilter)) {
			rows = append(rows, r)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

func (m Model) agentNode(project string) string {
	_, _, _, machine := m.projectCost(project)
	for _, p := range m.projectRows() {
		if p.Name == project && (p.Hostname == machine || p.NodeID == machine) {
			return p.NodeID
		}
	}
	return ""
}

// Match projectCostOn, including observed user copies and tracked repo skills.
func (m Model) agentSkillState(name, project string) (on bool, repo string) {
	node := m.agentNode(project)
	for _, it := range m.tl.items {
		if it.Kind != protocol.InventorySkill || it.Name != name {
			continue
		}
		if it.Desired != nil {
			a := it.Desired.Assignment
			on = on || a.OnMachine(node) && (a.Scope == "global" || a.Scope == "projects" && hasStr(a.Projects, project))
		}
		for _, l := range it.Locations {
			if l.Scope == "project" && l.Tracked && (l.GraphNode == project || l.Project == project) {
				repo = project
			}
			if l.NodeID == node && (l.Scope == "user" || l.Scope == "project" && l.Tracked && (l.GraphNode == project || l.Project == project)) {
				on = true
			}
		}
	}
	return
}

func (m Model) agentJobs(project string) ([]assignJob, string) {
	var jobs []assignJob
	for _, r := range m.agentSkillsUnfiltered() {
		on, ok := m.tl.agentPending[project][r.Name]
		if !ok {
			continue
		}
		if why := m.skillLockReason(r); why != "" {
			return nil, r.Name + ": " + why
		}
		target := protocol.ToolAssignment{Scope: "projects"}
		if r.Item.Desired != nil {
			target = r.Item.Desired.Assignment
			target.Projects = append([]string(nil), target.Projects...)
			target.Machines = append([]string(nil), target.Machines...)
		}
		// An imported user copy still loads in every session on its original
		// machines. Turning it off for one agent must keep the other projects.
		if r.Item.Desired == nil && !on && r.Origin == toolsdiff.OriginMachines {
			target.Scope = "global"
			for _, v := range r.Variants {
				for _, loc := range v.Locations {
					if loc.Scope == protocol.InventoryUser && loc.NodeID != "" && !hasStr(target.Machines, loc.NodeID) {
						target.Machines = append(target.Machines, loc.NodeID)
					}
				}
			}
			sort.Strings(target.Machines)
		}
		if target.Scope == "global" {
			target.Scope = "projects"
			target.Projects = m.toolsProjects()
		}
		var ps []string
		for _, p := range target.Projects {
			if p != project {
				ps = append(ps, p)
			}
		}
		if on {
			ps = append(ps, project)
		}
		sort.Strings(ps)
		target.Scope = "projects"
		target.Projects = ps
		if len(ps) == 0 {
			target.Scope = "uninstalled"
		}
		job, why := m.jobFor(r, target)
		if why != "" {
			return nil, why
		}
		job.agentProject, job.agentOn = project, on
		jobs = append(jobs, job)
	}
	return jobs, ""
}
func (m Model) agentSkillsUnfiltered() []toolsdiff.Row { m.tl.agentFilter = ""; return m.agentSkills() }

func (m Model) agentToolsContext() keyContext {
	if m.tl.agentDiscard {
		return contextToolsDiscard
	}
	if m.tl.skillsFocus {
		return contextToolsSkills
	}
	return contextToolsAgents
}
func (m Model) agentToolsHints() []hint {
	ctx := m.agentToolsContext()
	if m.tl.agentDiscard {
		return []hint{bindingHint(ctx, "discard"), bindingHint(ctx, "keep")}
	}
	out := []hint{bindingPairHint(ctx, actDown, actUp)}
	if m.tl.skillsFocus {
		out = append(out, bindingHint(ctx, "toggle"), bindingHint(ctx, "review"), bindingHint(ctx, "agents"))
	} else {
		out = append(out, bindingHint(ctx, "skills"))
	}
	return append(out, bindingHint(ctx, "filter"), bindingHint(ctx, "all tools"), bindingHint(ctx, "back"), bindingHint(ctx, "help"), bindingHint(ctx, actQuit))
}

func (m Model) agentToolsKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	project := m.agentProject()
	if m.tl.agentSearching {
		switch k.Type {
		case tea.KeyEsc:
			m.tl.agentFilter = ""
			m.tl.agentSearching = false
		case tea.KeyEnter:
			m.tl.agentSearching = false
		case tea.KeyBackspace, tea.KeyCtrlH:
			r := []rune(m.tl.agentFilter)
			if len(r) > 0 {
				m.tl.agentFilter = string(r[:len(r)-1])
			}
		case tea.KeyRunes:
			m.tl.agentFilter += string(k.Runes)
		case tea.KeySpace:
			m.tl.agentFilter += " "
		}
		m.tl.skillRow = 0
		return m, nil
	}
	action := keyAction(m.agentToolsContext(), k.String())
	if m.tl.agentDiscard {
		if action == "discard" {
			delete(m.tl.agentPending, project)
			m.tl.agentDiscard = false
			m.tl.skillsFocus = false
		}
		if action == "keep" {
			m.tl.agentDiscard = false
		}
		return m, nil
	}
	m.tl.toast = ""
	switch action {
	case "import all":
		return m.importAllSkills()
	case "all tools":
		m.tl.allTools = true
	case "help":
		m.openHelp()
	case actQuit:
		m.quitting = true
		return m, tea.Quit
	case "filter":
		m.tl.agentSearching = true
	case "skills":
		m.tl.skillsFocus = true
	case "agents":
		m.tl.skillsFocus = false
	case "back":
		if len(m.tl.agentPending[project]) > 0 {
			m.tl.agentDiscard = true
		} else {
			m.tl.skillsFocus = false
			m.tl.agentFilter = ""
		}
	case actDown, actUp:
		delta := 1
		if action == actUp {
			delta = -1
		}
		if m.tl.skillsFocus {
			m.tl.skillRow = min(max(0, m.tl.skillRow+delta), max(0, len(m.agentSkills())-1))
		} else {
			m.tl.agentRow = min(max(0, m.tl.agentRow+delta), max(0, len(m.toolsProjects())-1))
			m.tl.skillRow = 0
		}
	case "toggle":
		rows := m.agentSkills()
		if project == "" || len(rows) == 0 {
			break
		}
		r := rows[min(m.tl.skillRow, len(rows)-1)]
		if why := m.skillLockReason(r); why != "" {
			m.toolsToast(why)
			break
		}
		on, repo := m.agentSkillState(r.Name, project)
		if repo == "" && r.Origin == toolsdiff.OriginRepo {
			repo = r.Repo
		}
		if repo != "" {
			m.toolsToast("In the " + repo + " repo (always on). Change this skill in the repo.")
			break
		}
		if m.tl.agentPending == nil {
			m.tl.agentPending = map[string]map[string]bool{}
		}
		if m.tl.agentPending[project] == nil {
			m.tl.agentPending[project] = map[string]bool{}
		}
		if _, ok := m.tl.agentPending[project][r.Name]; ok {
			delete(m.tl.agentPending[project], r.Name)
		} else {
			m.tl.agentPending[project][r.Name] = !on
		}
	case "review":
		jobs, why := m.agentJobs(project)
		if why != "" {
			m.toolsToast(why)
			break
		}
		if len(jobs) == 0 {
			m.toolsToast("Use space to change a skill, then enter to review.")
			break
		}
		model, cmd := m.openAssign("Review "+nounCount(len(jobs), "change", "changes")+" for "+project, "apply", jobs, nil)
		next := model.(Model)
		// Keep pending changes when the review is cancelled. Consume them only
		// after confirmation starts the existing apply path.
		if apply, ok := next.tl.modal.actions["enter"]; ok {
			next.tl.modal.actions["enter"] = func(m Model) (Model, tea.Cmd) { delete(m.tl.agentPending, project); return apply(m) }
		} else if cmd != nil {
			original := cmd
			cmd = func() tea.Msg {
				msg := original().(toolsModalMsg)
				update := msg.update
				msg.update = func(md *toolsModal) {
					update(md)
					if apply, ok := md.actions["enter"]; ok {
						md.actions["enter"] = func(m Model) (Model, tea.Cmd) { delete(m.tl.agentPending, project); return apply(m) }
					}
				}
				return msg
			}
		}
		return next, cmd
	}
	return m, nil
}

func (m Model) agentToolsView(w, h int) []string {
	var banner []string
	if n := m.outsideSkillsCount(); n > 0 {
		banner = wrapStyled(outsideSkillsBanner(n), w)
		h -= len(banner)
	}
	if m.tl.agentDiscard {
		banner = append(banner, "Discard "+nounCount(len(m.tl.agentPending[m.agentProject()]), "change", "changes")+"? y/n")
		h--
	} else if toast := m.activeToast(); toast != "" {
		lines := wrapStyled(toast, w)
		banner = append(banner, lines...)
		h -= len(lines)
	}
	left := min(25, max(20, w/5))
	right := w - left
	project := m.agentProject()
	n, c, _, machine := m.projectCost(project)
	title := fmt.Sprintf("%s · on %s · %s on · %s per session", project, orStr(machine, "-"), nounCount(n, "skill", "skills"), tokenCountText(c))
	if w < 100 {
		title = fmt.Sprintf("%s · on %s · %d on · %s/session", project, orStr(machine, "-"), n, formatTokens(c))
	}
	a, b := []string{}, []string{}
	room := max(1, h-3)
	ps := m.toolsProjects()
	start := max(0, m.tl.agentRow-room+1)
	for i := start; i < min(len(ps), start+room); i++ {
		count, _, _, _ := m.projectCost(ps[i])
		lead := "  "
		if i == m.tl.agentRow {
			lead = "▸ "
		}
		line := lead + cell(clip(sanitize(ps[i]), left-10), left-9) + fmt.Sprintf("%3d", count)
		if i == m.tl.agentRow && !m.tl.skillsFocus {
			line = selectedRow(padStyled(line, left-4))
		}
		a = append(a, line)
	}
	rows := m.agentSkills()
	start = max(0, m.tl.skillRow-room+1)
	for i := start; i < min(len(rows), start+room); i++ {
		r := rows[i]
		on, repo := m.agentSkillState(r.Name, project)
		check := "[ ] "
		if on {
			check = "[x] "
		}
		state := ""
		if on {
			state = "on for this agent"
		}
		if r.Item.Desired != nil && r.Item.Desired.Assignment.Scope == "global" {
			state = "on for every project"
		}
		if r.Origin == toolsdiff.OriginMachines || r.Origin == toolsdiff.OriginLocal {
			state = m.outsideToolStatus(r)
		}
		if repo != "" {
			state = "in the " + repo + " repo (always on)"
		}
		if why := m.skillLockReason(r); why != "" {
			check = "×   "
			state = why
		}
		name := r.Name
		if pending, ok := m.tl.agentPending[project][r.Name]; ok {
			if pending {
				name += " → on"
			} else {
				name += " → off"
			}
			state = "change not applied yet"
		}
		cost := r.Item.StartTokens
		for _, it := range m.tl.items {
			if it.Kind == protocol.InventorySkill && it.Name == r.Name && it.StartTokens > cost {
				cost = it.StartTokens
			}
		}
		nameW := min(34, right-17)
		line := check + cell(clip(sanitize(name), nameW), nameW) + cell(formatTokens(cost), 7)
		if w >= 100 {
			stateW := max(0, right-4-nameW-12)
			if strings.Contains(state, "outside agent-net") && len(state) > stateW {
				state = "outside agent-net"
			}
			line += " " + clip(sanitize(state), stateW)
		}
		if i == m.tl.skillRow && m.tl.skillsFocus {
			line = selectedRow(padStyled(line, right-4))
		}
		b = append(b, line)
	}
	if !m.tl.loaded {
		b = []string{orStr(m.tl.err, "reading the tools...")}
	} else if project == "" {
		b = []string{"No projects registered."}
	} else if len(rows) == 0 {
		b = []string{"No matching skills."}
	}
	for len(a) < h-2 {
		a = append(a, "")
	}
	for len(b) < h-2 {
		b = append(b, "")
	}
	heading := "Agents"
	if m.tl.agentFilter != "" || m.tl.agentSearching {
		heading += " / " + m.tl.agentFilter + searchCursor(m.tl.agentSearching)
	}
	b[len(b)-1] = "skills per agent · tab: all tools"
	ap, bp := panel(heading, a, left, roleAccent), panel(title, b, right, roleAccent)
	out := make([]string, len(ap))
	for i := range ap {
		out[i] = ap[i] + bp[i]
	}
	// The view label stays visible even when a narrow title must be clipped.

	return append(banner, out...)
}
