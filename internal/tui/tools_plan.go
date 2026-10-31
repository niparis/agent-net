package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolsdiff"
)

// Plans. Every change in the Tools tab opens a plan first, in terms of machines
// and projects. d shows the exact folders, enter applies, esc cancels. Nothing
// runs before enter. The plan comes from the gateway (tools_plan), so what the
// owner reads is what the gateway would do.

var (
	tsGood   = lipgloss.NewStyle().Foreground(roleGood)
	tsWarn   = lipgloss.NewStyle().Foreground(roleWarning)
	tsErr    = lipgloss.NewStyle().Foreground(roleError)
	tsDim    = lipgloss.NewStyle().Foreground(roleDim)
	tsFaint  = lipgloss.NewStyle().Foreground(roleFaint)
	tsAcc    = lipgloss.NewStyle().Foreground(roleAccent)
	tsAccB   = lipgloss.NewStyle().Foreground(roleAccent).Bold(true)
	tsMat    = lipgloss.NewStyle().Foreground(roleMature)
	tsText   = lipgloss.NewStyle().Foreground(roleText)
	tsStrike = lipgloss.NewStyle().Foreground(roleDim).Strikethrough(true)
)

// toolsModal is the plan (or the project picker, or the result) on screen.
type toolsModal struct {
	id         int
	title      string
	lines      []string
	paths      []string
	pathsTitle string
	showPaths  bool
	scroll     int
	loading    bool
	done       bool
	actions    map[string]func(Model) (Model, tea.Cmd)
	foot       []string // key and label pairs
	picker     *projPicker
}

// toolsModalMsg changes the open modal when a background call ends.
type toolsModalMsg struct {
	id     int
	update func(md *toolsModal)
	reload bool
}

func (m *Model) openModal(title string) *toolsModal {
	m.tl.nextModal++
	md := &toolsModal{id: m.tl.nextModal, title: title, actions: map[string]func(Model) (Model, tea.Cmd){}}
	m.tl.modal = md
	return md
}

func (m Model) onToolsModal(r toolsModalMsg) (Model, tea.Cmd) {
	if md := m.tl.modal; md != nil && md.id == r.id {
		r.update(md)
	}
	if r.reload {
		model, cmd := m.loadTools()
		return model.(Model), cmd
	}
	return m, nil
}

func (md *toolsModal) planFoot(verb string) {
	md.foot = []string{"enter", verb, "d", "exact folders", "esc", "cancel"}
}

func (m Model) toolsModalKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	md := m.tl.modal
	key := k.String()
	if md.picker != nil && !md.loading {
		return m.pickerKey(k)
	}
	switch {
	case key == "esc" || (md.done && key == "enter"):
		m.tl.modal = nil
		return m, nil
	case md.loading:
		return m, nil
	case key == "down" || key == "j":
		md.scroll++
		return m, nil
	case key == "up" || key == "k":
		md.scroll = max(0, md.scroll-1)
		return m, nil
	case key == "d" && len(md.paths) > 0:
		md.showPaths = !md.showPaths
		return m, nil
	}
	if act, ok := md.actions[key]; ok {
		return act(m)
	}
	return m, nil
}

// ---- reading a plan ----

func (m Model) assignTarget(on bool) protocol.ToolAssignment {
	if on {
		return protocol.ToolAssignment{Scope: "global"}
	}
	return protocol.ToolAssignment{Scope: "uninstalled"}
}

// machineTarget is assignTarget for one machine. With a single machine there
// is nothing to limit, so the assignment stays "every machine".
func (m Model) machineTarget(on bool, machine toolsdiff.Machine) protocol.ToolAssignment {
	a := m.assignTarget(on)
	if len(m.tl.cat.Machines) > 1 {
		a.Machines = []string{machine.ID}
	}
	return a
}

// assignJob is one skill to change: import first when it is not in the store.
type assignJob struct {
	name   string
	item   string
	imp    *toolsdiff.Copy
	target protocol.ToolAssignment
	// where names the machine the change is limited to, or "" for every machine.
	where string
}

func (j assignJob) off() bool { return j.target.Scope == "uninstalled" }

// openRowPlan is a or u on the selected cell of the All tools screen.
func (m Model) openRowPlan(on bool) (tea.Model, tea.Cmd) {
	r, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	if _, ok := m.toolsSource(); !ok {
		return m, nil
	}
	ms := m.tl.cat.Machines
	col := m.tl.col
	switch {
	case r.Origin == toolsdiff.OriginRepo:
		if on {
			m.toolsToast("it is already on for " + r.Repo + " through the repo")
			return m, nil
		}
		return m.openRepoRemove(r.Repo, r.Name, repoCopies(r))
	case r.Kind == protocol.InventoryMCP && r.Origin == toolsdiff.OriginMachines:
		if !on {
			m.toolsToast("this server is set up by hand: send it through the gateway first (a), then remove the hand-made entry")
			return m, nil
		}
		return m.openMCPImport(r.Name)
	}
	if col >= len(ms) {
		return m.openProjectPicker(r, on)
	}
	cell := r.Cells[col]
	if on && cell.State == toolsdiff.CellOn && r.Origin != toolsdiff.OriginMachines && r.Origin != toolsdiff.OriginLocal {
		m.toolsToast(r.Name + " is already on for " + ms[col].Name)
		return m, nil
	}
	if !on && cell.State == toolsdiff.CellOff {
		m.toolsToast(r.Name + " is already off for " + ms[col].Name)
		return m, nil
	}
	job, err := m.jobFor(r, m.machineTarget(on, ms[col]))
	if err != "" {
		m.toolsToast(err)
		return m, nil
	}
	verb := "turn on"
	title := "Turn on " + r.Name + " on " + ms[col].Name
	if !on {
		verb = "turn off"
		title = "Turn off " + r.Name + " on " + ms[col].Name
	}
	note := ""
	if len(job.target.Machines) > 0 {
		job.where = ms[col].Name
		note = "only on " + ms[col].Name + ": other machines are not changed"
	}
	return m.openAssign(title, verb, []assignJob{job}, []string{note})
}

// jobFor builds the job for a row. A skill that agent-net does not manage is
// imported first.
func (m Model) jobFor(r toolsdiff.Row, target protocol.ToolAssignment) (assignJob, string) {
	j := assignJob{name: r.Name, target: target}
	// A local copy (in a project folder, not committed) is handled like a copy
	// found on a machine: import it first, then plan.
	if r.Origin == toolsdiff.OriginMachines || r.Origin == toolsdiff.OriginLocal {
		c, ok := firstCopy(r)
		if !ok {
			return j, "no copy of " + r.Name + " is online to import"
		}
		j.item, j.imp = c.Item, &c
		return j, ""
	}
	j.item = r.Item.ID
	return j, ""
}

func firstCopy(r toolsdiff.Row) (toolsdiff.Copy, bool) {
	for _, v := range r.Variants {
		for _, l := range v.Locations {
			if r.Origin == toolsdiff.OriginLocal {
				if l.Scope == protocol.InventoryProject && !l.Tracked && l.State == protocol.InventoryOK && (l.GraphNode == r.Repo || l.Project == r.Repo) {
					return toolsdiff.Copy{Item: v.ID, NodeID: l.NodeID, Machine: l.Machine, Location: l.Location, Hash: v.Hash, Name: v.Name}, true
				}
				continue
			}
			if l.Scope == protocol.InventoryUser && l.State == protocol.InventoryOK {
				return toolsdiff.Copy{Item: v.ID, NodeID: l.NodeID, Machine: l.Machine, Location: l.Location, Hash: v.Hash, Name: v.Name}, true
			}
		}
	}
	return toolsdiff.Copy{}, false
}

func repoCopies(r toolsdiff.Row) []toolsdiff.Copy {
	var out []toolsdiff.Copy
	for _, v := range r.Variants {
		for _, l := range v.Locations {
			if l.Scope == protocol.InventoryProject && l.Tracked && (l.GraphNode == r.Repo || l.Project == r.Repo) {
				checkout := l.ProjectRoot
				if checkout == "" {
					checkout = checkoutForSkillLocation(l.Location, v.Name)
				}
				out = append(out, toolsdiff.Copy{Item: v.ID, NodeID: l.NodeID, Machine: l.Machine, Location: l.Location, Checkout: checkout, Hash: v.Hash, Name: v.Name})
			}
		}
	}
	return out
}

func checkoutForSkillLocation(location, name string) string {
	for _, root := range []string{".agents/skills", ".claude/skills", ".opencode/skills", ".pi/skills"} {
		suffix := filepath.Join(root, name)
		clean := filepath.Clean(location)
		if strings.HasSuffix(clean, string(filepath.Separator)+suffix) {
			return strings.TrimSuffix(clean, string(filepath.Separator)+suffix)
		}
	}
	return ""
}

// openAssign shows the plan for one or more jobs. A single job for a skill that
// is already in the store shows the gateway's own plan; the others show what
// will happen and plan each step when enter is pressed.
func (m Model) openAssign(title, verb string, jobs []assignJob, notes []string) (tea.Model, tea.Cmd) {
	ts, _ := m.toolsSource()
	md := m.openModal(title)
	md.planFoot(verb)
	extra := func() []string {
		var out []string
		for _, n := range notes {
			if n != "" {
				out = append(out, tsDim.Render("  "+n))
			}
		}
		return out
	}
	if len(jobs) == 1 && jobs[0].imp == nil {
		job := jobs[0]
		md.loading = true
		id := md.id
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 2*Timeout)
			defer cancel()
			plan, err := ts.ToolsPlan(ctx, protocol.ToolPlanRequest{Item: job.item, Target: job.target})
			return toolsModalMsg{id: id, update: func(md *toolsModal) {
				md.loading = false
				if err != nil {
					md.lines = []string{tsErr.Render("! " + sanitize(err.Error()))}
					md.foot = []string{"esc", "close"}
					return
				}
				md.lines = append(planLines(plan, job), extra()...)
				md.paths = planPaths(plan)
				md.actions["enter"] = func(m Model) (Model, tea.Cmd) { return m.applyPlan(plan.ID, job.name) }
			}}
		}
	}
	md.lines = jobLines(jobs)
	md.lines = append(md.lines, extra()...)
	md.paths = jobPaths(jobs)
	md.actions["enter"] = func(m Model) (Model, tea.Cmd) { return m.runJobs(jobs) }
	return m, nil
}

func jobLines(jobs []assignJob) []string {
	var out []string
	imports := 0
	for _, j := range jobs {
		if j.imp != nil {
			imports++
		}
	}
	if imports > 0 {
		out = append(out, tsGood.Render("+ ")+nounCount(imports, "skill", "skills")+" go into the agent-net store as new versions (the files stay where they are)")
	}
	off := jobs[0].off()
	names := make([]string, 0, len(jobs))
	for _, j := range jobs {
		names = append(names, j.name)
	}
	scope := "on every machine"
	if t := jobs[0].target; t.Scope == "projects" {
		scope = "for " + strings.Join(t.Projects, ", ")
	}
	if w := jobs[0].where; w != "" {
		scope = "on " + w + " only"
	}
	if off {
		where := "everywhere"
		if w := jobs[0].where; w != "" {
			where = "on " + w + " only"
		}
		out = append(out, tsErr.Render("- ")+listNames(names, 6)+" turned off "+where+" (backed up first, removed only if unchanged)")
	} else {
		out = append(out, tsGood.Render("+ ")+listNames(names, 6)+" "+scope)
	}
	out = append(out, tsDim.Render("  the gateway makes the exact plan for each one when you press enter"))
	return out
}

func jobPaths(jobs []assignJob) []string {
	var out []string
	for _, j := range jobs {
		if j.imp != nil {
			out = append(out, j.imp.Machine+"  "+j.imp.Location)
		}
	}
	if len(out) == 0 {
		out = []string{"the folders are listed by the gateway's plan when you press enter"}
	}
	return out
}

func listNames(names []string, max int) string {
	if len(names) <= max {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:max], ", ") + fmt.Sprintf(" and %d more", len(names)-max)
}

// planLines tells the plan in terms of machines and projects.
func planLines(plan protocol.ToolPlan, job assignJob) []string {
	type key struct{ machine, project string }
	count := map[key]int{}
	var order []key
	var conflicts, offline []string
	seenC := map[string]bool{}
	for _, t := range plan.Targets {
		if t.Conflict != "" {
			line := t.Machine
			if line == "" {
				line = "plan"
			}
			line += ": " + t.Conflict
			if t.State == "offline" {
				if !hasStr(offline, t.Machine) {
					offline = append(offline, t.Machine)
				}
				continue
			}
			if !seenC[line] {
				seenC[line] = true
				conflicts = append(conflicts, line)
			}
			continue
		}
		if t.NodeID == "" {
			continue
		}
		k := key{t.Machine, t.Project}
		if count[k] == 0 {
			order = append(order, k)
		}
		count[k]++
	}
	sign := tsGood.Render("+ ")
	if plan.Assignment.Scope == "uninstalled" {
		sign = tsErr.Render("- ")
	}
	var out []string
	for _, k := range order {
		what := job.name
		if k.project != "" {
			what += " for " + k.project
		} else if plan.Assignment.Scope == "global" {
			what += " for every project"
		}
		out = append(out, sign+tsText.Render(k.machine+": "+what))
	}
	for _, m := range offline {
		out = append(out, tsWarn.Render("◐ ")+m+" is offline: it applies when the machine is back")
	}
	for _, c := range conflicts {
		out = append(out, tsWarn.Render("! ")+sanitize(c))
	}
	if len(out) == 0 {
		out = append(out, tsDim.Render("nothing to change"))
	}
	return out
}

func planPaths(plan protocol.ToolPlan) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range plan.Targets {
		if t.Destination == "" {
			continue
		}
		line := t.Machine + "  " + t.Destination
		if !seen[line] {
			seen[line] = true
			out = append(out, line)
		}
	}
	return out
}

// ---- applying ----

func outcomeLines(name string, res protocol.ToolApplyResult) []string {
	var ok, pending int
	var out []string
	for _, t := range res.Targets {
		switch t.State {
		case "ok", "applied":
			ok++
		case "pending":
			pending++
			who := t.Machine
			if who == "" {
				who = "a machine"
			}
			out = append(out, tsWarn.Render("◐ ")+name+" on "+who+" is pending: "+orStr(t.Reason, "it applies when the machine is back"))
		case "conflict":
			out = append(out, tsErr.Render("! ")+name+" on "+t.Machine+" has a conflict: "+sanitize(t.Reason)+". Fix that, then plan the change again.")
		default:
			out = append(out, tsErr.Render("! ")+name+" on "+t.Machine+" failed: "+sanitize(t.Reason)+". Check the machine, then plan the change again.")
		}
	}
	head := tsGood.Render("✓ ") + name + ": " + nounCount(ok, "target done", "targets done")
	if pending > 0 {
		head += ", " + fmt.Sprint(pending) + " pending"
	}
	return append([]string{head}, out...)
}

func orStr(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return sanitize(s)
}

func (m Model) applyPlan(planID, name string) (Model, tea.Cmd) {
	ts, _ := m.toolsSource()
	md := m.tl.modal
	md.loading = true
	md.lines = append(md.lines, "", tsDim.Render("applying..."))
	id := md.id
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*Timeout)
		defer cancel()
		res, err := ts.ToolsApply(ctx, planID)
		return toolsModalMsg{id: id, reload: true, update: func(md *toolsModal) {
			md.loading, md.done, md.paths, md.picker = false, true, nil, nil
			md.foot = []string{"enter", "close"}
			if err != nil {
				md.lines = []string{tsErr.Render("! " + sanitize(err.Error()))}
				return
			}
			md.title = "Applied: " + name
			md.lines = outcomeLines(name, res)
		}}
	}
}

// runJobs imports (when needed), plans and applies each job in turn.
func (m Model) runJobs(jobs []assignJob) (Model, tea.Cmd) {
	ts, _ := m.toolsSource()
	md := m.tl.modal
	md.loading = true
	md.lines = append(md.lines, "", tsDim.Render("applying..."))
	id := md.id
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time10min)
		defer cancel()
		var lines []string
		fails := 0
		for _, j := range jobs {
			if j.imp != nil {
				err := ts.ToolsImport(ctx, protocol.ToolImport{Item: j.imp.Item, Location: protocol.ToolImportLocation{NodeID: j.imp.NodeID, Location: j.imp.Location}})
				if err != nil {
					fails++
					lines = append(lines, tsErr.Render("! ")+j.name+": could not import: "+sanitize(err.Error()))
					continue
				}
			}
			plan, err := ts.ToolsPlan(ctx, protocol.ToolPlanRequest{Item: j.item, Target: j.target})
			if err != nil {
				fails++
				lines = append(lines, tsErr.Render("! ")+j.name+": "+sanitize(err.Error()))
				continue
			}
			res, err := ts.ToolsApply(ctx, plan.ID)
			if err != nil {
				fails++
				lines = append(lines, tsErr.Render("! ")+j.name+": "+sanitize(err.Error()))
				continue
			}
			lines = append(lines, outcomeLines(j.name, res)...)
		}
		return toolsModalMsg{id: id, reload: true, update: func(md *toolsModal) {
			md.loading, md.done, md.paths = false, true, nil
			md.foot = []string{"enter", "close"}
			md.title = fmt.Sprintf("Applied: %s", nounCount(len(jobs), "tool", "tools"))
			md.lines = tidyResult(lines, 14)
		}}
	}
}

// tidyResult keeps the first lines of a long result and counts the rest.
func tidyResult(lines []string, max int) []string {
	if len(lines) <= max {
		return lines
	}
	return append(lines[:max:max], tsDim.Render(fmt.Sprintf("  and %d more lines", len(lines)-max)))
}

// ---- project picker ----

type projPicker struct {
	all       []string
	sel       map[string]bool
	filter    string
	filtering bool
	cur       int
	remove    bool
	row       toolsdiff.Row
	current   []string // projects the tool is on for now
}

func (p *projPicker) shown() []string {
	q := strings.ToLower(p.filter)
	var out []string
	for _, n := range p.all {
		if q == "" || strings.Contains(strings.ToLower(n), q) {
			out = append(out, n)
		}
	}
	return out
}

func (m Model) openProjectPicker(r toolsdiff.Row, on bool) (tea.Model, tea.Cmd) {
	if r.Kind == protocol.InventoryMCP && r.Origin == toolsdiff.OriginGateway {
		m.toolsToast("MCP server scope can be changed with the CTO: ask it to plan the projects")
		return m, nil
	}
	var cur []string
	for _, p := range r.Projects {
		cur = append(cur, p.Name)
	}
	p := &projPicker{all: m.toolsProjects(), sel: map[string]bool{}, remove: !on, row: r, current: cur}
	if on {
		for _, n := range cur {
			p.sel[n] = true
		}
	} else if len(cur) == 0 {
		m.toolsToast(r.Name + " is not on for any project")
		return m, nil
	}
	if len(p.all) == 0 {
		m.toolsToast("no projects yet: register a project first")
		return m, nil
	}
	title := "Turn on " + r.Name + " for projects"
	if !on {
		title = "Turn off " + r.Name + " for projects"
	}
	md := m.openModal(title)
	md.picker = p
	pickerFoot(md, p)
	return m, nil
}

func pickerFoot(md *toolsModal, p *projPicker) {
	if p.filtering {
		md.foot = []string{"enter", "keep filter", "esc", "clear filter"}
		return
	}
	verb := "plan"
	md.foot = []string{"space", "tick", "/", "filter", "enter", verb, "esc", "cancel"}
}

func (m Model) pickerKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	md := m.tl.modal
	p := md.picker
	if p.filtering {
		switch k.Type {
		case tea.KeyEsc:
			p.filter, p.filtering = "", false
		case tea.KeyEnter:
			p.filtering = false
		case tea.KeyBackspace, tea.KeyCtrlH:
			r := []rune(p.filter)
			if len(r) > 0 {
				p.filter = string(r[:len(r)-1])
			}
		case tea.KeyRunes, tea.KeySpace:
			p.filter += string(k.Runes)
		}
		p.cur = 0
		pickerFoot(md, p)
		return m, nil
	}
	shown := p.shown()
	switch k.String() {
	case "esc":
		m.tl.modal = nil
	case "j", "down":
		p.cur = min(p.cur+1, max(len(shown)-1, 0))
	case "k", "up":
		p.cur = max(p.cur-1, 0)
	case " ", "space", "x":
		if p.cur < len(shown) {
			n := shown[p.cur]
			p.sel[n] = !p.sel[n]
		}
	case "/":
		p.filtering = true
		pickerFoot(md, p)
	case "enter":
		return m.pickerConfirm()
	}
	return m, nil
}

func (m Model) pickerConfirm() (tea.Model, tea.Cmd) {
	md := m.tl.modal
	p := md.picker
	var chosen []string
	if p.remove {
		for _, n := range p.current {
			if !p.sel[n] {
				chosen = append(chosen, n)
			}
		}
	} else {
		for n, v := range p.sel {
			if v {
				chosen = append(chosen, n)
			}
		}
	}
	sort.Strings(chosen)
	target := protocol.ToolAssignment{Scope: "projects", Projects: chosen}
	if len(chosen) == 0 {
		if !p.remove {
			m.toolsToast("tick at least one project")
			return m, nil
		}
		target = protocol.ToolAssignment{Scope: "uninstalled"}
	}
	job, err := m.jobFor(p.row, target)
	if err != "" {
		m.toolsToast(err)
		m.tl.modal = nil
		return m, nil
	}
	title := "Turn on " + p.row.Name + " for projects"
	verb := "turn on"
	if p.remove {
		title, verb = "Turn off "+p.row.Name+" for projects", "turn off"
	}
	m.tl.modal = nil
	return m.openAssign(title, verb, []assignJob{job}, []string{"each project gets it on every machine; copies are kept out of git"})
}

// ---- repo copies ----

// openRepoRemove plans a repo skill removal through the audited tools workflow.
// The daemon creates a branch in a temporary worktree and leaves the checkout
// and default branch alone.
func (m Model) openRepoRemove(project, name string, copies []toolsdiff.Copy) (tea.Model, tea.Cmd) {
	ts, ok := m.toolsSource()
	if !ok {
		m.toolsToast("this daemon cannot plan repo skill removal")
		return m, nil
	}
	md := m.openModal("Remove " + name + " from the " + project + " repo")
	md.lines = []string{tsDim.Render("planning the branch and exact files to remove..."), tsDim.Render("  agent-net never commits to the default branch or pushes")}
	md.pathsTitle = "exact files"
	md.foot = []string{"b", "ask the agent", "esc", "cancel"}
	md.actions["b"] = func(m Model) (Model, tea.Cmd) { return m.askProjectAgent(project, name, copies) }
	var repoTargets []protocol.ToolRepoTarget
	seen := map[string]bool{}
	for _, c := range copies {
		if c.NodeID == "" || c.Checkout == "" {
			continue
		}
		key := c.NodeID + "\x00" + filepath.Clean(c.Checkout)
		if seen[key] {
			continue
		}
		seen[key] = true
		repoTargets = append(repoTargets, protocol.ToolRepoTarget{NodeID: c.NodeID, Checkout: c.Checkout})
	}
	if len(copies) == 0 || len(repoTargets) == 0 {
		md.lines = []string{tsErr.Render("! ") + "no registered project checkout is available for this repo skill"}
		return m, nil
	}
	md.loading = true
	id := md.id
	req := protocol.ToolPlanRequest{Item: copies[0].Item, Target: protocol.ToolAssignment{Scope: "projects", Projects: []string{project}}, Operation: "repo_remove", Project: project, RepoTargets: repoTargets}
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*Timeout)
		defer cancel()
		plan, err := ts.ToolsPlan(ctx, req)
		return toolsModalMsg{id: id, update: func(md *toolsModal) {
			md.loading = false
			if err != nil {
				md.lines = []string{tsErr.Render("! " + sanitize(err.Error()))}
				return
			}
			md.lines = repoRemovalPlanLines(plan, project, name)
			md.paths = repoRemovalFiles(plan)
			md.showPaths = true
			if len(plan.Conflicts) > 0 {
				md.foot = []string{"b", "ask the agent", "d", "exact files", "esc", "cancel"}
				return
			}
			md.foot = []string{"a", "create branch and commit", "b", "ask the agent", "d", "exact files", "esc", "cancel"}
			md.actions["a"] = func(m Model) (Model, tea.Cmd) { return m.applyRepoRemoval(plan.ID, project, name) }
		}}
	}
}

func repoRemovalPlanLines(plan protocol.ToolPlan, project, name string) []string {
	lines := []string{
		tsErr.Render("- ") + project + " repo: remove " + name,
		tsDim.Render("  agent-net creates a local branch and commit; it never pushes"),
		tsDim.Render("  b: ask " + project + "'s agent to do it and follow its charter"),
	}
	for _, target := range plan.Targets {
		lines = append(lines, tsAcc.Render("  branch ")+target.Branch+" from "+orStr(target.DefaultBranch, "unknown default branch")+" in "+orStr(target.Machine, project))
		if target.Dirty {
			lines = append(lines, tsWarn.Render("  uncommitted changes in this skill folder stay in the owner's checkout"))
		}
		if target.Conflict != "" {
			lines = append(lines, tsErr.Render("  "+sanitize(target.Conflict)))
		}
	}
	return lines
}

func repoRemovalFiles(plan protocol.ToolPlan) []string {
	var files []string
	for _, target := range plan.Targets {
		for _, file := range target.Files {
			files = append(files, target.Machine+"  "+file)
		}
	}
	return files
}

func (m Model) applyRepoRemoval(planID, project, name string) (Model, tea.Cmd) {
	ts, _ := m.toolsSource()
	md := m.tl.modal
	md.loading = true
	md.lines = append(md.lines, "", tsDim.Render("creating the branch and commit..."))
	id := md.id
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*Timeout)
		defer cancel()
		res, err := ts.ToolsApply(ctx, planID)
		return toolsModalMsg{id: id, reload: true, update: func(md *toolsModal) {
			md.loading, md.done, md.paths = false, true, nil
			md.foot = []string{"enter", "close"}
			if err != nil {
				md.lines = []string{tsErr.Render("! " + sanitize(err.Error()))}
				return
			}
			md.title = "Branch created: " + name
			md.lines = repoRemovalOutcomeLines(res, project)
		}}
	}
}

func repoRemovalOutcomeLines(res protocol.ToolApplyResult, project string) []string {
	var lines []string
	for _, target := range res.Targets {
		switch target.State {
		case "ok":
			lines = append(lines, tsGood.Render("✓ ")+"branch "+target.Branch+" created in "+orStr(target.Project, project)+": merge it when ready")
			if target.Commit != "" {
				lines = append(lines, tsDim.Render("  commit "+target.Commit))
			}
		case "pending":
			lines = append(lines, tsWarn.Render("◐ ")+"branch creation pending on "+orStr(target.Machine, project)+": "+orStr(target.Reason, "machine offline"))
		default:
			lines = append(lines, tsErr.Render("! ")+"branch creation failed in "+orStr(target.Project, project)+": "+orStr(target.Reason, "fix the conflict and plan again"))
		}
	}
	if len(lines) == 0 {
		lines = []string{tsErr.Render("! ") + "no branch result was reported"}
	}
	return lines
}

// projectAgent finds the session to ask: online, in the project, not the owner.
func (m Model) projectAgent(project string) (protocol.AgentView, bool) {
	var best protocol.AgentView
	found := false
	for _, a := range m.snap.Agents {
		if protocol.IsOwner(a.Harness) || !a.Online || a.Repo.Project != project {
			continue
		}
		if !found || (best.Worker != nil && a.Worker == nil) {
			best, found = a, true
		}
	}
	return best, found
}

func (m Model) askProjectAgent(project, name string, copies []toolsdiff.Copy) (Model, tea.Cmd) {
	a, ok := m.projectAgent(project)
	if !ok {
		m.tl.modal = nil
		m.toolsToast("no session is online in " + project + ": start one there, then ask again")
		return m, nil
	}
	var where []string
	for _, c := range copies {
		where = append(where, c.Location)
	}
	body := "The owner asks, from the agent-net Tools tab: please remove the skill " + name + " from this repo (" + strings.Join(where, ", ") +
		") on a new branch with one commit, and tell the owner the branch name. Do not commit to the default branch. Follow this project's charter."
	md := m.tl.modal
	md.loading = true
	md.lines = append(md.lines, "", tsDim.Render("sending..."))
	id := md.id
	src := m.src
	to, who := a.SessionID, firstNonEmpty(a.Profile.Name, a.SessionID)
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		_, err := src.SendAsOwner(ctx, to, body)
		return toolsModalMsg{id: id, update: func(md *toolsModal) {
			md.loading, md.done, md.paths = false, true, nil
			md.foot = []string{"enter", "close"}
			md.title = "Asked " + who
			if err != nil {
				md.lines = []string{tsErr.Render("! " + sanitize(err.Error()))}
				return
			}
			md.lines = []string{tsGood.Render("✓ ") + "asked " + who + " in " + project + " to remove " + name + " on a new branch", tsDim.Render("  its answer arrives as a message; nothing changed in the repo yet")}
		}}
	}
}

// ---- MCP servers ----

func (m Model) openMCPImport(name string) (tea.Model, tea.Cmd) {
	ts, _ := m.toolsSource()
	md := m.openModal("Send " + name + " through the gateway")
	md.loading = true
	md.planFoot("send it")
	id := md.id
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*Timeout)
		defer cancel()
		key, why := mcpCandidate(ctx, ts, name, false)
		var plan protocol.MCPImportPlan
		var err error
		if key != "" {
			plan, err = ts.MCPImportPlan(ctx, key)
		}
		return toolsModalMsg{id: id, update: func(md *toolsModal) {
			md.loading = false
			if key == "" || err != nil {
				if err != nil {
					why = err.Error()
				}
				md.lines = []string{tsErr.Render("! " + sanitize(why))}
				md.foot = []string{"esc", "close"}
				return
			}
			c := plan.Candidate
			md.lines = []string{
				tsGood.Render("+ ") + "gateway: MCP server " + name + " runs on " + c.Machine,
				tsDim.Render("  the hand-made entry stays as it is until you remove it"),
			}
			for _, cf := range plan.Conflicts {
				md.lines = append(md.lines, tsWarn.Render("! ")+sanitize(cf))
			}
			for _, g := range c.Gaps {
				md.lines = append(md.lines, tsWarn.Render("◐ ")+"the gateway does not provide: "+sanitize(g))
			}
			md.paths = []string{c.Machine + "  " + c.Location}
			md.actions["enter"] = func(m Model) (Model, tea.Cmd) { return m.applyMCPImport(plan.ID, name) }
		}}
	}
}

// mcpCandidate finds the key of the native entry of a server. imported selects
// entries that were already imported (to remove).
func mcpCandidate(ctx context.Context, ts ToolsSource, name string, imported bool) (key, why string) {
	cands, err := ts.MCPCandidates(ctx)
	if err != nil {
		return "", err.Error()
	}
	for _, c := range cands {
		n := firstNonEmpty(c.ServerName, c.Name)
		if (c.Name == name || n == name) && c.Imported == imported && (imported || c.Class == protocol.MCPImportable) {
			return c.Key, ""
		}
	}
	return "", "no entry of " + name + " can be moved"
}

func (m Model) applyMCPImport(planID, name string) (Model, tea.Cmd) {
	ts, _ := m.toolsSource()
	md := m.tl.modal
	md.loading = true
	md.lines = append(md.lines, "", tsDim.Render("testing the server and sending it..."))
	id := md.id
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time10min)
		defer cancel()
		res, err := ts.MCPImportApply(ctx, planID)
		return toolsModalMsg{id: id, reload: true, update: func(md *toolsModal) {
			md.loading, md.done, md.paths = false, true, nil
			md.foot = []string{"enter", "close"}
			switch {
			case err != nil:
				md.lines = []string{tsErr.Render("! " + sanitize(err.Error()))}
			case res.State == protocol.MCPImportOK:
				md.title = "Sent: " + name
				md.lines = []string{tsGood.Render("✓ ") + name + " is on the gateway (" + nounCount(len(res.Tools), "tool", "tools") + ")", tsDim.Render("  the hand-made entry is still there: remove it when you are happy")}
			default:
				md.lines = []string{tsErr.Render("! ") + name + " was not sent: " + orStr(res.Reason, "the test failed") + ". Nothing changed. Fix the cause, then try again."}
			}
		}}
	}
}

func (m Model) openMCPNativeRemove(name string) (tea.Model, tea.Cmd) {
	ts, _ := m.toolsSource()
	md := m.openModal("Remove the hand-made entry of " + name)
	md.loading = true
	md.planFoot("remove it")
	id := md.id
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*Timeout)
		defer cancel()
		key, why := mcpCandidate(ctx, ts, name, true)
		var plan protocol.MCPNativeRemovePlan
		var err error
		if key != "" {
			plan, err = ts.MCPNativeRemovePlan(ctx, key)
		}
		return toolsModalMsg{id: id, update: func(md *toolsModal) {
			md.loading = false
			if key == "" || err != nil {
				if err != nil {
					why = err.Error()
				}
				md.lines = []string{tsErr.Render("! " + sanitize(why))}
				md.foot = []string{"esc", "close"}
				return
			}
			c := plan.Candidate
			md.lines = []string{
				tsErr.Render("- ") + c.Machine + ": the entry " + c.Name + " in its harness config",
				tsDim.Render("  only that entry is cut out; the file is backed up on that machine first"),
			}
			for _, cf := range plan.Conflicts {
				md.lines = append(md.lines, tsWarn.Render("! ")+sanitize(cf))
			}
			for _, g := range plan.Gaps {
				md.lines = append(md.lines, tsWarn.Render("◐ ")+"the gateway does not provide: "+sanitize(g))
			}
			md.paths = []string{c.Machine + "  " + c.Location}
			md.actions["enter"] = func(m Model) (Model, tea.Cmd) { return m.applyMCPNativeRemove(plan.ID, name) }
		}}
	}
}

func (m Model) applyMCPNativeRemove(planID, name string) (Model, tea.Cmd) {
	ts, _ := m.toolsSource()
	md := m.tl.modal
	md.loading = true
	md.lines = append(md.lines, "", tsDim.Render("removing..."))
	id := md.id
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time10min)
		defer cancel()
		res, err := ts.MCPNativeRemoveApply(ctx, planID)
		return toolsModalMsg{id: id, reload: true, update: func(md *toolsModal) {
			md.loading, md.done, md.paths = false, true, nil
			md.foot = []string{"enter", "close"}
			switch {
			case err != nil:
				md.lines = []string{tsErr.Render("! " + sanitize(err.Error()))}
			case res.State == protocol.MCPNativeRemoved:
				md.title = "Removed: " + name
				md.lines = []string{tsGood.Render("✓ ") + "the hand-made entry of " + name + " is gone"}
				for _, n := range res.Notes {
					md.lines = append(md.lines, tsDim.Render("  "+sanitize(n)))
				}
			default:
				md.lines = []string{tsErr.Render("! ") + "not removed: " + orStr(res.Reason, "refused") + ". Nothing changed."}
			}
		}}
	}
}

// ---- differences ----

// openFix is enter on a difference: the plan of its one suggested fix.
func (m Model) openFix() (tea.Model, tea.Cmd) {
	ds := m.shownDiffs()
	if m.tl.row >= len(ds) {
		return m, nil
	}
	if _, ok := m.toolsSource(); !ok {
		return m, nil
	}
	d := ds[m.tl.row]
	title := strings.ToUpper(d.Fix[:1]) + d.Fix[1:]
	act := d.Action
	switch act.Kind {
	case toolsdiff.ActAssign:
		return m.openAssign(title, "apply", []assignJob{{name: act.Name, item: act.Item, target: protocol.ToolAssignment{Scope: "global"}}}, nil)
	case toolsdiff.ActChoose:
		return m.openChoose(title, act)
	case toolsdiff.ActRemoveRepo:
		return m.openRepoRemove(act.Project, act.Name, act.Copies)
	case toolsdiff.ActRemove:
		var imp *toolsdiff.Copy
		if len(act.Copies) > 0 {
			c := act.Copies[0]
			imp = &c
		}
		return m.openAssign(title, "remove", []assignJob{{name: act.Name, item: act.Item, imp: imp, target: protocol.ToolAssignment{Scope: "uninstalled"}}}, []string{"the copy is saved in the agent-net store first, so it can come back"})
	case toolsdiff.ActImportAll:
		jobs := make([]assignJob, 0, len(act.Copies))
		for _, c := range act.Copies {
			c := c
			jobs = append(jobs, assignJob{name: c.Name, item: c.Item, imp: &c, target: protocol.ToolAssignment{Scope: "global"}})
		}
		return m.openAssign(title, "import and turn on", jobs, []string{"or press x on this line to leave them as they are"})
	case toolsdiff.ActMCPImport:
		return m.openMCPImport(act.Name)
	case toolsdiff.ActMCPNativeRemove:
		return m.openMCPNativeRemove(act.Name)
	}
	m.toolsToast(d.Fix)
	return m, nil
}

// openChoose lets the owner keep one of several copies: a, b, c ...
func (m Model) openChoose(title string, act toolsdiff.Action) (tea.Model, tea.Cmd) {
	md := m.openModal(title)
	for i, c := range act.Copies {
		if i >= 6 {
			break
		}
		letter := string(rune('a' + i))
		c := c
		who := sanitize(c.Machine)
		if len(act.Copies) == 2 && act.Copies[0].Machine == act.Copies[1].Machine && c.Harness != "" {
			who += " (" + sanitize(c.Harness) + ")"
		}
		md.lines = append(md.lines, tsAcc.Render(letter+"  ")+"keep the copy on "+who+copySummary(c, act.Copies, m.now()))
		md.paths = append(md.paths, c.Machine+"  "+c.Location)
		md.foot = append(md.foot, letter, "keep "+sanitize(c.Machine))
		md.actions[letter] = func(m Model) (Model, tea.Cmd) {
			m.tl.modal = nil
			notes := comparisonNotes(act.Copies, c)
			notes = append(notes, "a different copy in the way shows as a conflict in the result")
			target := protocol.ToolAssignment{Scope: "global"}
			where := ""
			if len(act.Copies) == 2 && c.NodeID != "" && act.Copies[0].NodeID == c.NodeID && act.Copies[1].NodeID == c.NodeID && len(m.tl.cat.Machines) > 1 {
				target.Machines = []string{c.NodeID}
				where = c.Machine
			}
			model, cmd := m.openAssign("Keep "+act.Name+" "+c.Version(), "apply", []assignJob{{name: act.Name, item: c.Item, imp: &c, target: target, where: where}}, notes)
			return model.(Model), cmd
		}
	}
	if len(act.Copies) == 2 {
		md.lines = append(md.lines, chooseComparison(act.Copies[0], act.Copies[1])...)
	}
	if title == "Keep the newer one in both places" && len(act.Copies) == 2 {
		md.actions["enter"] = md.actions["a"]
		md.foot = append([]string{"enter", "keep newer"}, md.foot...)
	}
	destination := "  the one you keep goes into agent-net and to every machine"
	if len(act.Copies) == 2 && act.Copies[0].NodeID != "" && act.Copies[0].NodeID == act.Copies[1].NodeID && len(m.tl.cat.Machines) > 1 {
		destination = "  the one you keep goes into agent-net and to " + sanitize(act.Copies[0].Machine)
	}
	md.lines = append(md.lines, tsDim.Render(destination))
	md.foot = append(md.foot, "d", "exact folders", "esc", "cancel")
	return m, nil
}

// copySummary describes what was observed, without treating absent manifests as empty.
func copySummary(c toolsdiff.Copy, copies []toolsdiff.Copy, now time.Time) string {
	m := c.Manifest
	if m == nil || m.TooLarge {
		return " (file details unavailable)"
	}
	var size int64
	for _, f := range m.Files {
		size += f.Size
	}
	detail := fmt.Sprintf(" - %s, %d B", nounCount(len(m.Files), "file", "files"), size)
	if !m.Newest.IsZero() {
		age := now.Sub(m.Newest)
		when := "just now"
		if age >= 24*time.Hour {
			when = nounCount(int(age.Hours()/24), "day", "days") + " ago"
		} else if age >= time.Hour {
			when = nounCount(int(age.Hours()), "hour", "hours") + " ago"
		} else if age >= time.Minute {
			when = nounCount(int(age.Minutes()), "minute", "minutes") + " ago"
		}
		detail += ", changed " + when + ", " + m.Newest.Format("2 Jan")
		if len(copies) == 2 && copies[0].Manifest != nil && copies[1].Manifest != nil && !copies[0].Manifest.TooLarge && !copies[1].Manifest.TooLarge && !copies[0].Manifest.Newest.IsZero() && !copies[1].Manifest.Newest.IsZero() {
			other := copies[0].Manifest.Newest
			if m == copies[0].Manifest {
				other = copies[1].Manifest.Newest
			}
			if m.Newest.After(other) {
				detail += " (newer)"
			}
		}
	}
	return detail
}

func chooseComparison(a, b toolsdiff.Copy) []string {
	added, removed, changed, ok := toolsdiff.CompareFiles(a, b)
	if !ok {
		return []string{tsDim.Render("  file comparison unavailable or too large to compare")}
	}
	var parts []string
	for _, p := range changed {
		parts = append(parts, sanitize(p)+" changed")
	}
	for _, p := range added {
		parts = append(parts, sanitize(p)+" only on "+sanitize(b.Machine))
	}
	for _, p := range removed {
		parts = append(parts, sanitize(p)+" only on "+sanitize(a.Machine))
	}
	lines := []string{fmt.Sprintf("  %s differ: %s", nounCount(len(parts), "file", "files"), listNames(parts, 5))}
	if !a.Manifest.Newest.IsZero() && !b.Manifest.Newest.IsZero() {
		switch {
		case a.Manifest.Newest.After(b.Manifest.Newest) && len(added) == 0:
			return append(lines, "  suggest: keep "+sanitize(a.Machine)+"'s copy (newer, has every file)")
		case b.Manifest.Newest.After(a.Manifest.Newest) && len(removed) == 0:
			return append(lines, "  suggest: keep "+sanitize(b.Machine)+"'s copy (newer, has every file)")
		}
	}
	lines = append(lines, fmt.Sprintf("  %s lacks %s; %s lacks %s", sanitize(a.Machine), nounCount(len(added), "file", "files"), sanitize(b.Machine), nounCount(len(removed), "file", "files")))
	return lines
}

const time10min = 10 * time.Minute

func comparisonNotes(copies []toolsdiff.Copy, chosen toolsdiff.Copy) []string {
	var notes []string
	for _, other := range copies {
		if other.Item == chosen.Item && other.NodeID == chosen.NodeID && other.Location == chosen.Location {
			continue
		}
		added, removed, changed, ok := toolsdiff.CompareFiles(other, chosen)
		if !ok {
			notes = append(notes, "file comparison unavailable or too large to compare")
			continue
		}
		notes = append(notes, "Compared with "+other.Machine+" "+other.Location+":")
		for _, p := range added {
			notes = append(notes, "added: "+p)
		}
		for _, p := range removed {
			notes = append(notes, "removed: "+p)
		}
		for _, p := range changed {
			notes = append(notes, "changed: "+p)
		}
		if len(added)+len(removed)+len(changed) == 0 {
			notes = append(notes, "no file differences")
		}
	}
	return notes
}
