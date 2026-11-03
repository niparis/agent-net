package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolsdiff"
)

// The Tools tab opens on one table: skills down the side, projects across,
// each cell on or off, and a last column for the standing choice about new
// projects. Folders, machines, harnesses and scopes stay out of the table.

// tableRows is one row per skill name, in name order. The filter matches names
// only, as the owner asked.
func (m Model) skillRows() []toolsdiff.Row {
	q := strings.ToLower(strings.TrimSpace(m.tl.search))
	seen := map[string]bool{}
	var out []toolsdiff.Row
	for _, r := range m.tl.cat.Rows {
		if r.Kind != protocol.InventorySkill || seen[r.Name] {
			continue
		}
		seen[r.Name] = true
		if q != "" && !strings.Contains(strings.ToLower(r.Name), q) {
			continue
		}
		out = append(out, r)
	}
	// A plugin, package or system skill has no catalog row, but the owner must
	// still see it and why it cannot be switched. Add a dim row for it.
	variants := map[string][]protocol.ToolItem{}
	var order []string
	for _, it := range m.tl.items {
		if it.Kind != protocol.InventorySkill || seen[it.Name] {
			continue
		}
		if _, ok := variants[it.Name]; !ok {
			order = append(order, it.Name)
		}
		variants[it.Name] = append(variants[it.Name], it)
	}
	for _, name := range order {
		if q != "" && !strings.Contains(strings.ToLower(name), q) {
			continue
		}
		out = append(out, toolsdiff.Row{Name: name, Kind: protocol.InventorySkill, Item: variants[name][0], Variants: variants[name]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// tableItemID is the stored item whose on/off choices this row uses. A managed
// variant wins over an observed copy with the same name.
func (m Model) tableItemID(r toolsdiff.Row) string {
	for _, v := range r.Variants {
		if v.Desired != nil {
			return v.ID
		}
	}
	for _, v := range r.Variants {
		for _, st := range m.tl.states {
			if st.Item == v.ID {
				return v.ID
			}
		}
	}
	if r.Item.ID != "" {
		return r.Item.ID
	}
	if len(r.Variants) > 0 {
		return r.Variants[0].ID
	}
	return ""
}

// tableState returns the on/off choices for a skill.
func (m Model) tableState(r toolsdiff.Row) (protocol.ToolProjectState, bool) {
	id := m.tableItemID(r)
	for _, st := range m.tl.states {
		if st.Item == id {
			return st, true
		}
	}
	for _, v := range r.Variants {
		for _, st := range m.tl.states {
			if st.Item == v.ID {
				return st, true
			}
		}
	}
	return protocol.ToolProjectState{}, false
}

func (m Model) tableOn(r toolsdiff.Row, project string) bool {
	if st, ok := m.tableState(r); ok {
		if on, has := st.Projects[project]; has {
			return on
		}
	}
	return toolOnProject(r, project)
}

func (m Model) tableNewProjects(r toolsdiff.Row) bool {
	st, _ := m.tableState(r)
	return st.NewProjects
}

// tableAssignment is the current choice, in the form the plan text speaks
// about: every project, a count of projects, or none.
func (m Model) tableAssignment(r toolsdiff.Row) protocol.ToolAssignment {
	if r.Item.Desired != nil {
		return r.Item.Desired.Assignment
	}
	st, ok := m.tableState(r)
	var on []string
	if ok {
		for _, p := range m.tl.projectCols {
			if st.Projects[p] {
				on = append(on, p)
			}
		}
		if len(on) == len(m.tl.projectCols) && len(on) > 0 && st.NewProjects {
			return protocol.ToolAssignment{Scope: "global"}
		}
	} else {
		for _, p := range m.tl.projectCols {
			if toolOnProject(r, p) {
				on = append(on, p)
			}
		}
	}
	if len(on) == 0 {
		return protocol.ToolAssignment{Scope: "uninstalled"}
	}
	return protocol.ToolAssignment{Scope: "projects", Projects: on}
}

// tableLock is why a row cannot be switched, in plain words.
func (m Model) tableLock(r toolsdiff.Row) string {
	if why := m.skillLockReason(r); why != "" {
		return why
	}
	if reason := m.tl.lockReasons[r.Name]; reason != "" {
		return reason
	}
	if r.Origin == toolsdiff.OriginRepo {
		return "in the " + r.Repo + " repo"
	}
	any, readOnly := false, true
	for _, v := range r.Variants {
		for _, l := range v.Locations {
			any = true
			switch l.Source {
			case protocol.InventoryPlugin, protocol.InventoryPackage, protocol.InventorySystem:
			default:
				readOnly = false
			}
		}
	}
	if any && readOnly {
		return "a plugin or system skill; agent-net leaves it alone"
	}
	return ""
}

func (m Model) tableProject() string {
	if len(m.tl.projectCols) == 0 {
		return ""
	}
	return m.tl.projectCols[min(max(m.tl.costCol, 0), len(m.tl.projectCols)-1)]
}

// tableLayout fits the name, cost and project columns into the panel width.
func tableLayout(w, projects int) (nameW, costW, colW, newW, visible int) {
	inner := max(w-4, 20)
	nameW = min(max(inner*28/100, 18), 30)
	costW = 7
	newW = 13
	colW = 10
	// Two spaces separate the name and the cost from the project columns.
	visible = max((inner-nameW-costW-newW-2)/colW, 1)
	if projects > 0 && visible > projects {
		visible = projects
	}
	return
}

// commaInt writes 9800 as 9,800 for the cost line.
func commaInt(n int64) string {
	s := fmt.Sprint(n)
	var out []byte
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}

func centerIn(s string, w int) string {
	if ansi.StringWidth(s) >= w {
		return clip(s, w)
	}
	left := (w - ansi.StringWidth(s)) / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", w-left-ansi.StringWidth(s))
}

// onOffMark is filled when a skill is on and empty when it is off.
func onOffMark(on, locked bool) string {
	switch {
	case locked:
		return tsFaint.Render("×")
	case on:
		return tsGood.Render("●")
	}
	return tsDim.Render("○")
}

func (m Model) tableView(w, h int) []string {
	out := []string{}
	rows := m.skillRows()
	projects := len(m.tl.projectCols)
	locked := func(r toolsdiff.Row) string { return m.tableLock(r) }

	// One line for the selected row's context and one for the cost.
	reserve := 2
	tableH := max(h-len(out)-reserve, 3)
	start := 0
	bodyH := max(tableH-2, 1)
	room := max(bodyH-1, 1)
	if m.tl.row >= room {
		start = m.tl.row - room + 1
	}
	if m.tl.row < start {
		start = m.tl.row
	}

	// Name the reason a dim row cannot be switched, starting with the
	// selected row and otherwise the first visible dim row.
	whyRow, why := toolsdiff.Row{}, ""
	selected, hasSelected := m.selectedRow()
	if hasSelected {
		why = locked(selected)
		whyRow = selected
	}
	if why == "" {
		for i := start; i < len(rows) && i < start+room; i++ {
			if w := locked(rows[i]); w != "" {
				whyRow, why = rows[i], w
				break
			}
		}
	}

	nameW, costW, colW, newW, visible := tableLayout(w, projects)
	first := m.tl.colScroll
	if first > max(projects-visible, 0) {
		first = max(projects-visible, 0)
	}
	if m.tl.col < first {
		first = m.tl.col
	}
	if m.tl.col >= first+visible && projects > 0 {
		first = min(m.tl.col-visible+1, max(projects-visible, 0))
	}
	last := min(first+visible, projects)

	head := cell("SKILL", nameW) + " " + cell("TOKENS", costW) + " "
	for i := first; i < last; i++ {
		label := cell(clip(sanitize(m.tl.projectCols[i]), colW-1), colW)
		if i == m.tl.col {
			label = selectedRow(tsAccB.Render(padRight(clip(sanitize(m.tl.projectCols[i]), colW-1), colW)))
		}
		head += label
	}
	headCell := "NEW PROJECTS"
	if i := m.tl.col; i >= projects {
		head += selectedRow(tsAccB.Render(centerIn("NEW PROJECTS", newW)))
	} else {
		head += centerIn(headCell, newW)
	}
	body := []string{head}
	if !m.tl.loaded {
		body = []string{orStr(m.tl.err, "reading the tools...")}
	} else if len(rows) == 0 {
		body = []string{"No skills match the filter."}
	}

	for i := start; i < len(rows) && i < start+room; i++ {
		r := rows[i]
		lock := locked(r)
		name := r.Name
		nameCell := ""
		if i == m.tl.row {
			nameCell = tsAccB.Render("▸") + " "
		} else {
			nameCell = "  "
		}
		if lock != "" {
			nameCell += tsFaint.Render(cell(name+" ×", nameW-2))
		} else {
			nameCell += tsText.Render(cell(name, nameW-2))
		}
		line := nameCell + " " + cell(formatTokens(r.Item.StartTokens), costW) + " "
		for c := first; c < last; c++ {
			mark := onOffMark(m.tableOn(r, m.tl.projectCols[c]), lock != "")
			text := centerIn(mark, colW)
			if i == m.tl.row && c == m.tl.col {
				text = selectedRow(tsAccB.Render(centerIn("["+onOffMark(m.tableOn(r, m.tl.projectCols[c]), lock != "")+"]", colW)))
			}
			line += text
		}
		newText := "off"
		if m.tableNewProjects(r) {
			newText = "on"
		}
		if lock != "" {
			newText = "-"
		}
		newCell := centerIn(newText, newW)
		if lock != "" {
			newCell = tsFaint.Render(newCell)
		}
		if i == m.tl.row && m.tl.col >= projects {
			newCell = selectedRow(tsAccB.Render(centerIn("["+newText+"]", newW)))
		}
		line += newCell
		body = append(body, line)
	}
	for len(body) < bodyH {
		body = append(body, "")
	}

	title := fmt.Sprintf("Skills by project · %s · %s", nounCount(len(rows), "skill", "skills"), nounCount(projects, "project", "projects"))
	if m.tl.search != "" || m.tl.searching {
		title = fmt.Sprintf("Skills by project / %s%s · %s", sanitize(m.tl.search), searchCursor(m.tl.searching), nounCount(len(rows), "skill", "skills"))
	}
	if projects > visible {
		title += fmt.Sprintf(" · showing %d-%d of %d, ←→ scrolls", first+1, last, projects)
	}
	out = append(out, panel(title, body, w, roleAccent)...)
	if why != "" {
		out = append(out, tsDim.Render(clip("  "+whyRow.Name+" cannot be switched: "+why, w)))
	} else {
		out = append(out, tsDim.Render(clip("  space switches the selected skill on or off", w)))
	}
	out = append(out, m.tableCostLine(w))
	for len(out) < h {
		out = append(out, "")
	}
	return out[:h]
}

// tableCostLine says what the selected project loads in every session.
func (m Model) tableCostLine(w int) string {
	project := m.tableProject()
	if project == "" {
		return tsDim.Render(clip("  No projects yet. agent-net adds one when it sees a Git repository.", w))
	}
	n, cost, _, _ := m.projectCost(project)
	line := fmt.Sprintf("  %s loads %s, about %s tokens per session", project, nounCount(n, "skill", "skills"), commaInt(cost))
	if n == 0 {
		line = fmt.Sprintf("  %s loads no skills yet", project)
	}
	return tsText.Render(clip(line, w))
}

// tableHints is the hint list the help overlay reads.
func (m Model) tableHints() []hint {
	enter := "actions"
	if len(m.tl.projectCols) > 0 && m.tl.col >= len(m.tl.projectCols) {
		enter = "new projects"
	}
	return []hint{
		{"↑↓←→", "move", 0},
		{"space", "on/off", 0},
		{"enter", enter, 0},
		{"/", "filter", 0},
		{"i", "import", 0},
		{"d", "details", 0},
		{"m", "all tools", 0},
		{"R", "rescan", 0},
		{"?", "help", 0},
	}
}

// tableScrollIntoView keeps the selected column visible after a move.
func (m *Model) tableScrollIntoView() {
	_, _, _, _, visible := tableLayout(m.width, len(m.tl.projectCols))
	if m.tl.col < m.tl.colScroll {
		m.tl.colScroll = m.tl.col
	}
	if m.tl.col >= m.tl.colScroll+visible {
		m.tl.colScroll = m.tl.col - visible + 1
	}
	m.tl.colScroll = min(max(m.tl.colScroll, 0), max(len(m.tl.projectCols)-visible, 0))
}

func (m Model) tableKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.tl.searching {
		return m.toolsSearchKey(k)
	}
	projects := len(m.tl.projectCols)
	m.tl.toast = ""
	switch keyAction(contextToolsTable, k.String()) {
	case actUp:
		m.tl.row--
	case actDown:
		m.tl.row++
	case actLeft:
		if m.tl.col > 0 {
			m.tl.col--
			if m.tl.col < projects {
				m.tl.costCol = m.tl.col
			}
		}
	case actRight:
		if m.tl.col < projects {
			m.tl.col++
			if m.tl.col < projects {
				m.tl.costCol = m.tl.col
			}
		}
	case "toggle":
		return m.tableToggle()
	case "open actions":
		if m.tl.col >= projects {
			return m.tableToggleNewProjects()
		}
		return m.openToolMenu()
	case "details":
		return m.tableDetails()
	case "filter":
		m.tl.searching = true
	case "clear filter":
		if m.tl.search != "" {
			m.tl.search = ""
		}
	case "import all":
		return m.importAllSkills()
	case "all tools":
		m.tl.allTools = true
		m.tl.search = ""
	case "rescan":
		return m.toolsRescan()
	case "add":
		return m.openToolAdd()
	case "help":
		m.openHelp()
	case actQuit:
		m.quitting = true
		return m, tea.Quit
	case "":
		switch k.String() {
		case "M", "tab":
			m.tl.allTools = true
			m.tl.search = ""
		}
	}
	m.tableClamp()
	m.tableScrollIntoView()
	return m, nil
}

func (m Model) tableToggle() (tea.Model, tea.Cmd) {
	r, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	if m.tl.col >= len(m.tl.projectCols) {
		return m.tableToggleNewProjects()
	}
	if why := m.tableLock(r); why != "" {
		m.toolsToast(why)
		return m, nil
	}
	project := m.tl.projectCols[m.tl.col]
	return m.toggleSkill(r, project, !m.tableOn(r, project))
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// toggleSkill asks the gateway for the plan and shows it before anything runs.
func (m Model) toggleSkill(r toolsdiff.Row, project string, on bool) (tea.Model, tea.Cmd) {
	ts, ok := m.toolsSource()
	if !ok {
		m.toolsToast("this daemon cannot switch skills")
		return m, nil
	}
	item := m.tableItemID(r)
	if item == "" {
		m.toolsToast("this skill has no project setting yet")
		return m, nil
	}
	verb := "Switch off "
	if on {
		verb = "Switch on "
	}
	md := m.openModal(verb + r.Name + " in " + project)
	md.loading = true
	md.planFoot("apply")
	id := md.id
	current := m.tableAssignment(r)
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*Timeout)
		defer cancel()
		plan, err := ts.ToolsToggle(ctx, protocol.ToolToggleRequest{Item: item, Project: project, On: on})
		msg := toolsModalMsg{id: id, update: func(md *toolsModal) {
			md.loading = false
			if err != nil {
				md.title = "Nothing changed"
				md.lines = []string{tsErr.Render("Nothing changed: " + sanitize(err.Error()))}
				md.foot = []string{"esc", "close"}
				return
			}
			md.lines = togglePlanLines(plan, current, r.Name, project, on)
			md.paths = planPaths(plan)
			if planCanApply(plan) {
				md.actions["enter"] = func(m Model) (Model, tea.Cmd) { return m.applyTogglePlan(plan.ID, r.Name, project, on) }
			} else {
				md.title = "Nothing to change: " + r.Name
				md.foot = []string{"esc", "close"}
			}
		}}
		if err != nil {
			msg.lockName, msg.lockReason = r.Name, sanitize(err.Error())
		}
		return msg
	}
}

func planCanApply(plan protocol.ToolPlan) bool {
	for _, t := range plan.Targets {
		if t.Conflict == "" && t.State != "conflict" {
			return true
		}
	}
	return false
}

// togglePlanLines turns the gateway's plan into plain words.
func togglePlanLines(plan protocol.ToolPlan, current protocol.ToolAssignment, name, project string, on bool) []string {
	lines := []string{"Switch " + onOff(on) + " " + name + " in " + project + "."}
	move := assignmentMove(name, current, plan.Assignment)
	if move != "" {
		lines = append(lines, move)
	}
	if len(plan.Assignment.Machines) == 1 {
		lines = append(lines, "It applies on one machine only; the other machines keep what they have.")
	}
	for _, t := range plan.Targets {
		if t.Action == "remove" && t.Source != "" {
			lines = append(lines, name+" leaves "+sanitize(t.Source)+" on "+sanitize(t.Machine)+".")
		}
	}
	seen := map[string]bool{}
	for _, c := range plan.Conflicts {
		if c != "" && !seen[c] {
			seen[c] = true
			lines = append(lines, tsWarn.Render("! ")+sanitize(c))
		}
	}
	for _, t := range plan.Targets {
		if t.Conflict == "" {
			continue
		}
		who := t.Machine
		if t.Project != "" {
			who += " " + t.Project
		}
		line := sanitize(strings.TrimSpace(who) + ": " + t.Conflict)
		if !seen[line] {
			seen[line] = true
			lines = append(lines, tsWarn.Render("! ")+line)
		}
	}
	if !planCanApply(plan) {
		lines = append(lines, tsErr.Render("Nothing changed: fix the conflicts above, then try again."))
	}
	return lines
}

// assignmentMove describes a shift between every project, some projects and
// no project.
func assignmentMove(name string, from, to protocol.ToolAssignment) string {
	a, b := assignmentWords(from), assignmentWords(to)
	if a == b {
		return ""
	}
	return name + " moves from " + a + " to " + b
}

func assignmentWords(a protocol.ToolAssignment) string {
	switch a.Scope {
	case "global":
		return "every project"
	case "projects":
		return nounCount(len(a.Projects), "project", "projects")
	}
	return "no project"
}

// applyTogglePlan runs the plan the owner saw and says plainly when nothing
// was done.
func (m Model) applyTogglePlan(planID, name, project string, on bool) (Model, tea.Cmd) {
	ts, _ := m.toolsSource()
	md := m.tl.modal
	md.loading = true
	md.lines = append(md.lines, "", tsDim.Render("applying..."))
	id := md.id
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*Timeout)
		defer cancel()
		res, err := ts.ToolsApply(ctx, planID)
		applied := 0
		for _, t := range res.Targets {
			if t.State == "ok" || t.State == "applied" {
				applied++
			}
		}
		return toolsModalMsg{id: id, reload: err == nil && applied > 0, update: func(md *toolsModal) {
			md.loading, md.done, md.paths = false, true, nil
			md.foot = []string{"enter", "close"}
			if err != nil || applied == 0 {
				why := ""
				if err != nil {
					why = err.Error()
				} else {
					for _, t := range res.Targets {
						if t.Reason != "" {
							why = t.Reason
							break
						}
					}
				}
				md.title = "Nothing changed: " + name
				md.lines = []string{tsErr.Render("Nothing changed: " + orStr(why, "no target was changed"))}
				return
			}
			md.title = "Applied: " + name
			md.lines = []string{tsGood.Render("✓ ") + "Switched " + onOff(on) + " " + name + " in " + project + "."}
			md.lines = append(md.lines, outcomeLines(name, res)[1:]...)
		}}
	}
}

// tableToggleNewProjects changes the standing choice. The gateway saves it at
// once; a placement plan is shown when files also have to move.
func (m Model) tableToggleNewProjects() (tea.Model, tea.Cmd) {
	r, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	if why := m.tableLock(r); why != "" {
		m.toolsToast(why)
		return m, nil
	}
	ts, ok := m.toolsSource()
	if !ok {
		m.toolsToast("this daemon cannot change that setting")
		return m, nil
	}
	item := m.tableItemID(r)
	if item == "" {
		m.toolsToast("this skill has no project setting yet")
		return m, nil
	}
	on := !m.tableNewProjects(r)
	md := m.openModal("New projects: " + onOff(on))
	md.loading = true
	md.planFoot("apply")
	id := md.id
	current := m.tableAssignment(r)
	req := protocol.ToolNewProjectsRequest{Item: item, On: on}
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*Timeout)
		defer cancel()
		plan, err := ts.ToolsNewProjects(ctx, req)
		msg := toolsModalMsg{id: id, newProjects: &req, update: func(md *toolsModal) {
			md.loading = false
			if err != nil {
				md.title = "Nothing changed"
				md.lines = []string{tsErr.Render("Nothing changed: " + sanitize(err.Error()))}
				md.foot = []string{"esc", "close"}
				return
			}
			md.lines = []string{"Skills switched on for future projects: " + onOff(on) + "."}
			md.lines = append(md.lines, togglePlanLines(plan, current, r.Name, "future projects", on)...)
			md.paths = planPaths(plan)
			if planCanApply(plan) {
				md.foot = []string{"enter", "also move the files", "esc", "leave the files as they are"}
				md.actions["enter"] = func(m Model) (Model, tea.Cmd) { return m.applyTogglePlan(plan.ID, r.Name, "future projects", on) }
			} else {
				md.foot = []string{"esc", "close"}
			}
		}}
		if err != nil {
			msg.lockName, msg.lockReason = r.Name, sanitize(err.Error())
		}
		return msg
	}
}

// tableDetails shows the description, the cost and the reason a skill is
// locked, without folders, machines, harnesses or scopes.
func (m Model) tableDetails() (tea.Model, tea.Cmd) {
	r, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	md := m.openModal(r.Name)
	var body []string
	if r.Item.Description != "" {
		body = append(body, wordsLines(sanitize(r.Item.Description), 76)...)
		body = append(body, "")
	}
	body = append(body, tokenCountText(r.Item.StartTokens)+" in every session")
	body = append(body, "+"+formatTokens(r.Item.FullTokens)+" when the skill is used")
	if on := m.projectsOn(r); on > 0 {
		body = append(body, fmt.Sprintf("on for %s of %s", nounCount(on, "project", "projects"), nounCount(len(m.tl.projectCols), "project", "projects")))
	} else {
		body = append(body, "off for every project")
	}
	if m.tableNewProjects(r) {
		body = append(body, "new projects: on")
	} else {
		body = append(body, "new projects: off")
	}
	if why := m.tableLock(r); why != "" {
		body = append(body, "")
		body = append(body, tsDim.Render("cannot be switched: "+why))
	}
	md.lines = body
	md.foot = []string{"esc", "close", "enter", "actions"}
	md.actions["enter"] = func(m Model) (Model, tea.Cmd) {
		m.tl.modal = nil
		model, cmd := m.openToolMenu()
		return model.(Model), cmd
	}
	return m, nil
}

func (m Model) projectsOn(r toolsdiff.Row) int {
	n := 0
	for _, p := range m.tl.projectCols {
		if m.tableOn(r, p) {
			n++
		}
	}
	return n
}
