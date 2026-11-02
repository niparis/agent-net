package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	udiff "github.com/aymanbagabas/go-udiff"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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
	menu         *toolMenu
	form         *toolAddForm
	id           int
	title        string
	lines        []string
	paths        []string
	pathsTitle   string
	showPaths    bool
	scroll       int
	loading      bool
	done         bool
	actions      map[string]func(Model) (Model, tea.Cmd)
	foot         []string // key and label pairs
	compareFiles []string
	compareIndex int
	back         *toolsModal
	picker       *projPicker
	choose       *chooseState
}

// chooseState is the compare plan's own data. It survives a background detail
// fetch so the panel can be drawn again when an answer arrives.
type chooseState struct {
	act          toolsdiff.Action
	items        []protocol.ToolItem
	fetching     map[int]bool
	errors       map[int]string
	online       map[string]bool
	now          time.Time
	summary      string
	summaryState string
}

// toolsModalMsg changes the open modal when a background call ends.
type toolsModalMsg struct {
	id         int
	update     func(md *toolsModal)
	reload     bool
	onboarding bool
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
	md.foot = []string{"enter/y", verb, "d", "exact folders", "esc/n", "cancel"}
}

func (m Model) toolsModalKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	md := m.tl.modal
	if md.menu != nil {
		return m.toolMenuKey(k)
	}
	if md.form != nil {
		return m.toolAddKey(k)
	}
	key := k.String()
	if md.picker != nil && !md.loading {
		return m.pickerKey(k)
	}
	action := keyAction(contextToolsPlan, key)
	// A plan asks yes or no: Enter and y run it, Esc and n close it.
	if action == "apply" {
		if _, ok := md.actions["enter"]; ok && !md.loading {
			key = "enter"
		}
	}
	if action == "cancel" && !md.loading {
		key = "esc"
	}
	switch {
	case action == "help":
		m.openHelp()
		return m, nil
	case action == "cancel" && key == "esc" && md.back != nil:
		m.tl.modal = md.back
		return m, nil
	case (action == "cancel" && key == "esc") || (md.done && action == "apply"):
		m.tl.modal = nil
		return m, nil
	case md.loading:
		return m, nil
	case action == actDown:
		md.scroll++
		return m, nil
	case action == actUp:
		md.scroll = max(0, md.scroll-1)
		return m, nil
	case action == "exact folders" && len(md.paths) > 0:
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
	choice *protocol.ToolCopyChoice
	name   string
	item   string
	imp    *toolsdiff.Copy
	target protocol.ToolAssignment
	// where names the machine the change is limited to, or "" for every machine.
	where string
}

func (j assignJob) off() bool { return j.target.Scope == "uninstalled" }

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
	if len(jobs) > 1 {
		var out []string
		for _, job := range jobs {
			lines := jobLines([]assignJob{job})
			out = append(out, lines[:len(lines)-1]...)
		}
		return append(out, tsDim.Render("  the gateway makes the exact plan for each one when you press enter"))
	}
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
		applied := false
		for _, target := range res.Targets {
			if target.State == "ok" {
				applied = true
				break
			}
		}
		return toolsModalMsg{id: id, reload: true, onboarding: err == nil && applied, update: func(md *toolsModal) {
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
			if j.choice != nil {
				keeper, ok := m.src.(interface {
					ToolsKeepCopy(context.Context, protocol.ToolCopyChoice) error
				})
				if !ok {
					lines = append(lines, "copy replacement unavailable on this daemon")
					continue
				}
				if err := keeper.ToolsKeepCopy(ctx, *j.choice); err != nil {
					lines = append(lines, j.name+": "+sanitize(err.Error()))
					continue
				}
			}
			if j.imp != nil && j.choice == nil {
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
	targets   bool
	all       []string
	sel       map[string]bool
	filter    string
	filtering bool
	cur       int
	remove    bool
	row       toolsdiff.Row
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

func pickerFoot(md *toolsModal, p *projPicker) {
	if p.filtering {
		md.foot = []string{"enter", "keep filter", "esc", "clear filter"}
		return
	}
	verb := "plan"
	if p.targets {
		verb = "apply"
	}
	md.foot = []string{"j/k", "move", "space", "tick", "/", "filter", "enter", verb, "esc", "cancel"}
}

func (m Model) pickerKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	md := m.tl.modal
	p := md.picker
	if p.filtering {
		switch k.Type {
		case tea.KeyEsc:
			if p.filter != "" {
				p.filter = ""
			} else {
				p.filtering = false
			}
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
	switch keyAction(contextToolsPicker, k.String()) {
	case "help":
		m.openHelp()
	case "cancel":
		m.tl.modal = nil
	case actDown:
		p.cur = min(p.cur+1, max(len(shown)-1, 0))
	case actUp:
		p.cur = max(p.cur-1, 0)
	case "tick":
		if p.cur < len(shown) {
			n := shown[p.cur]
			p.sel[n] = !p.sel[n]
		}
	case "filter":
		p.filtering = true
		pickerFoot(md, p)
	case "apply":
		return m.targetPickerConfirm()
	}
	return m, nil
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

// openMCPCoverage plans adding one project server to the harnesses that do
// not have it. The plan is read again from the machine that holds the project.
func (m Model) openMCPCoverage(title string, act toolsdiff.Action) (tea.Model, tea.Cmd) {
	ts, _ := m.toolsSource()
	md := m.openModal(title)
	md.loading = true
	md.planFoot("apply")
	id := md.id
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*Timeout)
		defer cancel()
		plan, err := ts.MCPCoveragePlan(ctx, protocol.MCPCoverageRequest{
			NodeID: act.NodeID, Machine: act.Machine, Project: act.Project, Server: act.Name,
			SourceHarness: act.Source, Harnesses: act.Harnesses,
		})
		return toolsModalMsg{id: id, update: func(md *toolsModal) {
			md.loading = false
			if err != nil {
				md.lines = []string{tsErr.Render("! " + sanitize(err.Error()))}
				md.foot = []string{"esc", "close"}
				return
			}
			md.title = "Add " + plan.Server + " for " + plan.Project
			if plan.State != "pending" {
				md.lines = []string{tsErr.Render("! ") + "cannot add it: " + orStr(strings.Join(plan.Conflicts, "; "), "the plan is not ready")}
				md.foot = []string{"esc", "close"}
				md.paths = planPathsForCoverage(plan)
				return
			}
			md.lines = []string{tsGood.Render("+ ") + plan.Server + " for " + plan.Project + " on " + plan.Machine}
			md.lines = append(md.lines, tsDim.Render("  writes these files, then reads each one back:"))
			for _, t := range plan.Targets {
				md.lines = append(md.lines, tsText.Render("  "+t.Harness+"  "+t.Path))
				for _, p := range t.Preview[min(1, len(t.Preview)):] {
					md.lines = append(md.lines, tsDim.Render("  "+sanitize(p)))
				}
			}
			md.lines = append(md.lines, tsFaint.Render("  environment values stay references: ${NAME} is never copied"))
			md.paths = planPathsForCoverage(plan)
			md.actions["enter"] = func(m Model) (Model, tea.Cmd) { return m.applyMCPCoverage(plan.ID, plan.Server) }
		}}
	}
}

func planPathsForCoverage(plan protocol.MCPCoveragePlan) []string {
	var out []string
	for _, t := range plan.Targets {
		out = append(out, t.Harness+"  "+t.Path)
	}
	return out
}

func (m Model) applyMCPCoverage(planID, name string) (Model, tea.Cmd) {
	ts, _ := m.toolsSource()
	md := m.tl.modal
	md.loading = true
	md.lines = append(md.lines, "", tsDim.Render("writing..."))
	id := md.id
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time10min)
		defer cancel()
		res, err := ts.MCPCoverageApply(ctx, planID)
		return toolsModalMsg{id: id, reload: true, update: func(md *toolsModal) {
			md.loading, md.done, md.paths = false, true, nil
			switch {
			case err != nil:
				md.lines = []string{tsErr.Render("! " + sanitize(err.Error()))}
				md.foot = []string{"enter", "close"}
			case res.State == "applied":
				var done []string
				for _, t := range res.Targets {
					if t.State == "applied" {
						done = append(done, t.Harness)
					}
				}
				md.title = "Added " + name
				md.lines = []string{tsGood.Render("✓ ") + name + " is now in " + strings.Join(done, ", ")}
				md.foot = []string{"enter", "close", "u", "undo"}
				md.actions["u"] = func(m Model) (Model, tea.Cmd) { return m.undoMCPCoverage(planID, name) }
			default:
				var why []string
				for _, t := range res.Targets {
					if t.Conflict != "" {
						why = append(why, t.Harness+": "+t.Conflict)
					}
				}
				md.title = "Not added: " + name
				md.lines = []string{tsErr.Render("! ") + "nothing changed: " + orStr(strings.Join(why, "; "), "refused")}
				md.foot = []string{"enter", "close"}
			}
		}}
	}
}

func (m Model) undoMCPCoverage(planID, name string) (Model, tea.Cmd) {
	ts, _ := m.toolsSource()
	md := m.tl.modal
	md.loading = true
	id := md.id
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time10min)
		defer cancel()
		res, err := ts.MCPCoverageUndo(ctx, planID)
		return toolsModalMsg{id: id, reload: true, update: func(md *toolsModal) {
			md.loading, md.done = false, true
			md.foot = []string{"enter", "close"}
			if err != nil || res.State != "applied" {
				why := ""
				if err != nil {
					why = err.Error()
				}
				for _, t := range res.Targets {
					why = orStr(why, t.Conflict)
				}
				md.title = "Not undone: " + name
				md.lines = []string{tsErr.Render("! ") + orStr(why, "nothing changed")}
				return
			}
			md.title = "Undone: " + name
			md.lines = []string{tsGood.Render("✓ ") + "the files are back as they were"}
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
		return m.openAssign(title, "import and turn on", jobs, []string{"press a on the difference row to leave the copies as they are"})
	case toolsdiff.ActMCPImport:
		return m.openMCPImport(act.Name)
	case toolsdiff.ActMCPNativeRemove:
		return m.openMCPNativeRemove(act.Name)
	case toolsdiff.ActMCPCoverage:
		return m.openMCPCoverage(title, act)
	}
	m.toolsToast(d.Fix)
	return m, nil
}

// openChoose lets the owner keep one of several copies: a, b, c ...
func (m Model) openChoose(title string, act toolsdiff.Action) (tea.Model, tea.Cmd) {
	_ = title
	md := m.openModal("Compare, then keep one")
	online := map[string]bool{}
	for _, machine := range m.tl.cat.Machines {
		online[machine.ID] = machine.Online
	}
	st := &chooseState{act: act, items: m.tl.items, fetching: map[int]bool{}, errors: map[int]string{}, online: online, now: m.now()}
	md.choose = st

	// A model summary is optional; a source without it still shows the files.
	var summaryReader skillFileReader
	var summaryWriter skillSummarizer
	var summaryA, summaryB toolsdiff.Copy
	var summaryKey string
	var summaryCache map[string]string
	wantSummary := false
	if len(act.Copies) == 2 {
		a, b := act.Copies[0], act.Copies[1]
		reader, readOK := m.src.(skillFileReader)
		summarizer, summaryOK := m.src.(skillSummarizer)
		if readOK && summaryOK && a.Hash != "" && b.Hash != "" {
			if m.tl.summaries == nil {
				m.tl.summaries = map[string]string{}
			}
			summaryReader, summaryWriter = reader, summarizer
			summaryA, summaryB = a, b
			summaryCache = m.tl.summaries
			summaryKey = a.Name + "\x00" + a.Hash + "\x00" + b.Hash
			if cached := summaryCache[summaryKey]; cached != "" {
				st.summary, st.summaryState = cached, "done"
			} else {
				st.summaryState = "loading"
				wantSummary = true
			}
		}
	}
	if st.summaryState == "" {
		st.summaryState = "unavailable"
	}

	var fetch []int
	for i, c := range act.Copies {
		if i >= 6 || c.Manifest != nil || c.NodeID == "" || !st.isOnline(c) {
			continue
		}
		st.fetching[i] = true
		fetch = append(fetch, i)
	}
	renderChoose(md)

	ts, hasTools := m.toolsSource()
	if !hasTools && len(fetch) > 0 {
		for _, i := range fetch {
			delete(st.fetching, i)
			st.errors[i] = "this daemon cannot read file details"
		}
		fetch = nil
		renderChoose(md)
	}
	if len(fetch) == 0 && !wantSummary {
		return m, nil
	}
	id := md.id
	copies := append([]toolsdiff.Copy(nil), act.Copies...)
	return m, func() tea.Msg {
		type answer struct {
			index   int
			details protocol.ToolSkillDetails
			err     error
		}
		answers := make([]answer, 0, len(fetch))
		if len(fetch) > 0 {
			ctx, cancel := context.WithTimeout(context.Background(), 2*Timeout)
			for _, i := range fetch {
				c := copies[i]
				name := c.Name
				if name == "" {
					name = act.Name
				}
				details, err := ts.ToolsSkillDetails(ctx, protocol.ToolSkillScan{NodeID: c.NodeID, Name: name, Location: c.Location, Hash: c.Hash})
				answers = append(answers, answer{index: i, details: details, err: err})
			}
			cancel()
		}
		var summary string
		var summaryErr error
		if wantSummary {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			read := func(c toolsdiff.Copy) (string, error) {
				return summaryReader.ToolsReadFile(ctx, protocol.SkillTextRequest{Item: c.Item, Location: protocol.ToolImportLocation{NodeID: c.NodeID, Location: c.Location}, Path: "SKILL.md"})
			}
			first, err := read(summaryA)
			var second string
			if err == nil {
				second, err = read(summaryB)
			}
			if err == nil {
				summary, err = summaryWriter.ToolsSummary(ctx, protocol.SkillSummaryRequest{AHash: summaryA.Hash, BHash: summaryB.Hash, A: first, B: second})
			}
			summaryErr = err
			cancel()
		}
		return toolsModalMsg{id: id, update: func(md *toolsModal) {
			if md.choose != st {
				return
			}
			for _, a := range answers {
				delete(st.fetching, a.index)
				if a.err != nil {
					st.errors[a.index] = sanitize(a.err.Error())
					continue
				}
				st.act.Copies[a.index].Manifest = a.details.Manifest
				if !a.details.ScannedAt.IsZero() {
					st.act.Copies[a.index].Seen = a.details.ScannedAt
				}
			}
			if wantSummary {
				if summaryErr != nil || summary == "" {
					st.summaryState = "unavailable"
				} else {
					st.summary = summary
					st.summaryState = "done"
					summaryCache[summaryKey] = summary
				}
			}
			renderChoose(md)
		}}
	}
}

func (st *chooseState) isOnline(c toolsdiff.Copy) bool {
	online, ok := st.online[c.NodeID]
	return !ok || online
}

// sideLine says why one side has no file details. A missing manifest is not
// left blank: the machine and the reason are named.
func (st *chooseState) sideLine(i int, c toolsdiff.Copy) string {
	who := sanitize(c.Machine)
	if who == "" {
		who = "this machine"
	}
	known := knownCopy(c, st.now)
	if !st.isOnline(c) {
		return who + " is offline: no details" + known
	}
	if st.fetching[i] {
		return "fetching details from " + who + "..."
	}
	if err := st.errors[i]; err != "" {
		return who + ": no details (" + err + ")" + known
	}
	return "no file details from " + who + " yet: its scan has no file list" + known
}

func knownCopy(c toolsdiff.Copy, now time.Time) string {
	var parts []string
	if version := c.Version(); version != "" {
		parts = append(parts, "version "+version)
	}
	if !c.Seen.IsZero() {
		parts = append(parts, "last seen "+whenText(c.Seen, now))
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func whenText(t, now time.Time) string {
	age := now.Sub(t)
	when := "just now"
	switch {
	case age >= 24*time.Hour:
		when = nounCount(int(age.Hours()/24), "day", "days") + " ago"
	case age >= time.Hour:
		when = nounCount(int(age.Hours()), "hour", "hours") + " ago"
	case age >= time.Minute:
		when = nounCount(int(age.Minutes()), "minute", "minutes") + " ago"
	}
	return when + ", " + t.Format("2 Jan")
}

// renderChoose fills the compare modal from its chooseState. The box itself is
// laid out in modalBox, which knows the terminal width. It runs again when a
// detail or summary answer arrives, without losing the keys the owner can
// press.
func renderChoose(md *toolsModal) {
	st := md.choose
	if st == nil {
		return
	}
	act := st.act
	md.lines = nil
	md.paths = nil
	md.foot = nil
	md.compareFiles = nil
	md.actions = map[string]func(Model) (Model, tea.Cmd){}
	md.title = compareTitle(act)
	md.lines = compareBoxLines(st, act.Copies, 112)

	if len(act.Copies) == 2 {
		added, removed, changed, ok := toolsdiff.CompareFiles(act.Copies[0], act.Copies[1])
		if ok {
			files := append(append(append([]string(nil), changed...), added...), removed...)
			sort.Strings(files)
			md.compareFiles = files
		}
	}
	for i, c := range act.Copies {
		if i >= 6 {
			break
		}
		letter := string(rune('a' + i))
		c := c
		label := "keep " + letter
		md.foot = append(md.foot, letter, label)
		md.actions[letter] = func(m Model) (Model, tea.Cmd) {
			m.tl.modal = nil
			notes := comparisonNotes(act.Copies, c)
			notes = append(notes, "other observed user copies are backed up before installing the chosen copy")
			choice := &protocol.ToolCopyChoice{Chosen: protocol.ToolImport{Item: c.Item, Location: protocol.ToolImportLocation{NodeID: c.NodeID, Location: c.Location}}}
			for _, copy := range act.Copies {
				choice.Copies = append(choice.Copies, protocol.ToolSkillScan{NodeID: copy.NodeID, Name: act.Name, Location: copy.Location, Hash: copy.Hash})
			}
			target := protocol.ToolAssignment{Scope: "global"}
			where := ""
			if len(act.Copies) == 2 && c.NodeID != "" && act.Copies[0].NodeID == c.NodeID && act.Copies[1].NodeID == c.NodeID && len(m.tl.cat.Machines) > 1 {
				target.Machines = []string{c.NodeID}
				where = c.Machine
			}
			name := c.Harness
			if name == "" {
				name = c.Machine
			}
			model, cmd := m.openAssign("Keep "+act.Name+" from "+c.Machine+" ("+name+")", "apply", []assignJob{{name: act.Name, item: c.Item, imp: &c, choice: choice, target: target, where: where}}, notes)
			return model.(Model), cmd
		}
	}
	if len(md.compareFiles) > 0 {
		md.foot = append(md.foot, "v", "full diff")
		md.actions["v"] = func(m Model) (Model, tea.Cmd) { return m.openSkillDiff(act.Copies[0], act.Copies[1]) }
	}
	md.foot = append(md.foot, "esc", "back")
}

// compareTitle names the skill and where its copies are.
func compareTitle(act toolsdiff.Action) string {
	name := sanitize(act.Name)
	if name == "" {
		name = "this skill"
	}
	return name + ": 2 different copies on " + compareMachines(act.Copies)
}

func compareMachines(copies []toolsdiff.Copy) string {
	var out []string
	for _, c := range copies {
		who := sanitize(c.Machine)
		if who == "" {
			who = "this machine"
		}
		if !hasStr(out, who) {
			out = append(out, who)
		}
	}
	if len(out) == 0 {
		return "this machine"
	}
	return strings.Join(out, " and ")
}

// compareCopyName is the column heading for one copy.
func compareCopyName(c toolsdiff.Copy, i int) string {
	switch {
	case c.Harness != "":
		return c.Harness + "'s copy"
	case c.Machine != "":
		return c.Machine + "'s copy"
	}
	return "copy " + string(rune('a'+i))
}

// usedBy says how many harness setups use the same copy. One setup is named by
// its harness rather than counted.
func (st *chooseState) usedBy(c toolsdiff.Copy) string {
	setups := 0
	var harnesses []string
	for _, item := range st.items {
		if item.Hash != c.Hash || item.Name != c.Name {
			continue
		}
		for _, loc := range item.Locations {
			setups++
			if loc.Harness != "" && !hasStr(harnesses, loc.Harness) {
				harnesses = append(harnesses, loc.Harness)
			}
		}
	}
	if setups == 0 {
		if c.Harness != "" {
			return c.Harness + " only"
		}
		return "one harness setup"
	}
	if setups == 1 {
		if len(harnesses) == 1 {
			return harnesses[0] + " only"
		}
		return "one harness setup"
	}
	return fmt.Sprintf("%d harness setups", setups)
}

// changedText is the date of the newest file change, or why it is unknown.
func (st *chooseState) changedText(i int, c toolsdiff.Copy) string {
	if c.Manifest != nil && !c.Manifest.Newest.IsZero() {
		return c.Manifest.Newest.Format("2 Jan")
	}
	if c.Manifest != nil {
		return "unknown"
	}
	return st.sideLine(i, c)
}

// compareFiles keeps the file list short: whole names only, about 40 columns.
func compareFiles(files []string) string {
	const limit = 40
	out := ""
	for _, f := range files {
		f = sanitize(f)
		next := f
		if out != "" {
			next = out + ", " + f
		}
		if ansi.StringWidth(next) > limit {
			if out == "" {
				out = f
			}
			break
		}
		out = next
	}
	return out
}

// compareBoxLines lays the two copies side by side. Long values wrap inside
// their column, so nothing overflows the box at 80 columns.
func compareBoxLines(st *chooseState, copies []toolsdiff.Copy, inner int) []string {
	if len(copies) != 2 || inner < 30 {
		return compareOneByOneLines(st, copies)
	}
	const labelWidth = 9
	colW := (inner - labelWidth - 4) / 2
	if colW < 12 {
		colW = 12
	}
	a, b := copies[0], copies[1]

	type row struct {
		label string
		left  []string
		right []string
	}
	var rows []row

	changed := row{label: "changed", left: wrap(st.changedText(0, a), colW), right: wrap(st.changedText(1, b), colW)}
	rows = append(rows, changed)
	rows = append(rows, row{label: "used by", left: wrap(st.usedBy(a), colW), right: wrap(st.usedBy(b), colW)})

	added, removed, changedFiles, ok := toolsdiff.CompareFiles(a, b)
	var leftFiles, rightFiles []string
	switch {
	case !ok:
		leftFiles = []string{"no file list"}
		rightFiles = []string{"no file list"}
	case len(added)+len(removed)+len(changedFiles) == 0:
		leftFiles = []string{"same files"}
		rightFiles = []string{"same files"}
	default:
		leftFiles = append(append([]string(nil), changedFiles...), removed...)
		rightFiles = append(append([]string(nil), changedFiles...), added...)
		sort.Strings(leftFiles)
		sort.Strings(rightFiles)
	}
	rows = append(rows, row{label: "files", left: wrap(compareFiles(leftFiles), colW), right: wrap(compareFiles(rightFiles), colW)})

	if st.summaryState == "loading" {
		rows = append(rows, row{label: "in short", left: wrap("summarising the difference...", colW), right: []string{""}})
	} else if st.summaryState == "done" && strings.TrimSpace(st.summary) != "" {
		inA, inB := splitSummary(st.summary)
		rows = append(rows, row{label: "in short", left: wrap(sanitize(inA), colW), right: wrap(sanitize(inB), colW)})
	}

	var out []string
	headA := wrap("a  "+compareCopyName(a, 0), colW)
	headB := wrap("b  "+compareCopyName(b, 1), colW)
	for i := 0; i < max(len(headA), len(headB)); i++ {
		l, r := "", ""
		if i < len(headA) {
			l = headA[i]
		}
		if i < len(headB) {
			r = headB[i]
		}
		out = append(out, "  "+strings.Repeat(" ", labelWidth)+tsAccB.Render(padRight(l, colW))+"  "+tsAccB.Render(r))
	}
	for _, r := range rows {
		n := max(len(r.left), len(r.right))
		for i := 0; i < n; i++ {
			label := ""
			if i == 0 {
				label = r.label
			}
			left := ""
			if i < len(r.left) {
				left = r.left[i]
			}
			right := ""
			if i < len(r.right) {
				right = r.right[i]
			}
			out = append(out, "  "+tsDim.Render(padRight(label, labelWidth))+padRight(left, colW)+"  "+right)
		}
	}
	out = append(out, "")
	for _, line := range wrap(compareDestination(copies), inner-2) {
		out = append(out, "  "+line)
	}
	return out
}

// padRight pads to w columns and never cuts.
func padRight(s string, w int) string {
	if n := w - ansi.StringWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// splitSummary divides the model's comparison into the two copies. Some
// summaries describe only the difference; then the whole text sits on the left.
func splitSummary(summary string) (string, string) {
	summary = strings.TrimSpace(summary)
	if at := strings.Index(summary, "; "); at >= 0 {
		return strings.TrimSpace(summary[:at]), strings.TrimSpace(summary[at+2:])
	}
	return summary, ""
}

// compareDestination says where the kept copy goes and for how long the other
// copy is kept. 30 days matches the daemon's backup retention.
func compareDestination(copies []toolsdiff.Copy) string {
	where := "on every machine"
	if len(copies) == 2 && copies[0].NodeID != "" && copies[0].NodeID == copies[1].NodeID {
		where = "on " + sanitize(copies[0].Machine)
	}
	return "The copy you keep goes to every harness " + where + ". The other is backed up for 30 days."
}

// compareOneByOneLines is the fallback for more than two copies, where a
// side-by-side box has no room.
func compareOneByOneLines(st *chooseState, copies []toolsdiff.Copy) []string {
	var out []string
	for i, c := range copies {
		if i >= 6 {
			break
		}
		letter := string(rune('a' + i))
		out = append(out, tsAcc.Render(letter+"  ")+"keep the copy on "+sanitize(c.Machine)+" ("+compareCopyName(c, i)+")")
		out = append(out, "     changed "+st.changedText(i, c)+"; used by "+st.usedBy(c))
	}
	out = append(out, "")
	out = append(out, compareDestination(copies))
	return out
}

type skillSummarizer interface {
	ToolsSummary(context.Context, protocol.SkillSummaryRequest) (string, error)
}

type skillFileReader interface {
	ToolsReadFile(context.Context, protocol.SkillTextRequest) (string, error)
}

func (m Model) openSkillDiff(a, b toolsdiff.Copy) (Model, tea.Cmd) {
	previous := m.tl.modal
	path := previous.compareFiles[previous.compareIndex]
	reader, ok := m.src.(skillFileReader)
	if !ok {
		m.toolsToast("file preview is unavailable")
		return m, nil
	}
	md := m.openModal("Diff: " + sanitize(path))
	md.back = previous
	md.loading = true
	md.foot = []string{"up/down", "scroll", "esc", "back"}
	id := md.id
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*Timeout)
		defer cancel()
		read := func(c toolsdiff.Copy) (string, error) {
			return reader.ToolsReadFile(ctx, protocol.SkillTextRequest{Item: c.Item, Location: protocol.ToolImportLocation{NodeID: c.NodeID, Location: c.Location}, Path: path})
		}
		before, err := read(a)
		var after string
		if err == nil {
			after, err = read(b)
		}
		var lines []string
		if err != nil {
			lines = []string{tsErr.Render("! " + sanitize(err.Error()))}
		} else {
			diff := unifiedSkillDiff(sanitize(a.Machine)+"/"+sanitize(path), sanitize(b.Machine)+"/"+sanitize(path), before, after)
			for _, line := range strings.Split(strings.TrimSuffix(diff, "\n"), "\n") {
				lines = append(lines, sanitize(line))
			}
			if diff == "" {
				lines = []string{"The text matches now. The scanned fingerprint changed; file permissions or a newer edit can explain it."}
			}
		}
		return toolsModalMsg{id: id, update: func(md *toolsModal) { md.loading = false; md.lines = lines }}
	}
}

// unifiedSkillDiff avoids quadratic diff work on larger previews while still
// showing a valid unified replacement of both files.
func unifiedSkillDiff(a, b, before, after string) string {
	if before == after {
		return ""
	}
	if len(before)+len(after) <= 8192 {
		return udiff.Unified(a, b, before, after)
	}
	count := func(s string) int {
		n := strings.Count(s, "\n")
		if len(s) > 0 && !strings.HasSuffix(s, "\n") {
			n++
		}
		return n
	}
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n@@ -%d,%d +%d,%d @@\n", a, b, min(1, count(before)), count(before), min(1, count(after)), count(after))
	write := func(prefix string, s string) {
		if s == "" {
			return
		}
		for _, line := range strings.SplitAfter(s, "\n") {
			if line == "" {
				continue
			}
			out.WriteString(prefix)
			out.WriteString(line)
			if !strings.HasSuffix(line, "\n") {
				out.WriteString("\n\\ No newline at end of file\n")
			}
		}
	}
	write("-", before)
	write("+", after)
	return out.String()
}

// noDetailsLine names the machines and says why there is nothing to compare,
// instead of the old line that left the owner guessing.
func noDetailsLine(a, b toolsdiff.Copy) string {
	var names []string
	for _, c := range []toolsdiff.Copy{a, b} {
		who := sanitize(c.Machine)
		if who == "" {
			who = "this machine"
		}
		if !hasStr(names, who) {
			names = append(names, who)
		}
	}
	return tsDim.Render("  no file details from " + strings.Join(names, " and ") + " yet: neither copy has sent a file list")
}

func chooseComparison(a, b toolsdiff.Copy) []string {
	label := func(c toolsdiff.Copy) string {
		if a.Machine == b.Machine && a.Harness != "" && b.Harness != "" && a.Harness != b.Harness {
			return sanitize(c.Harness)
		}
		return sanitize(c.Machine)
	}
	added, removed, changed, ok := toolsdiff.CompareFiles(a, b)
	if !ok {
		if a.Manifest == nil || b.Manifest == nil {
			return []string{noDetailsLine(a, b)}
		}
		return []string{tsDim.Render("  no clear winner: read the comparison"), tsDim.Render("  a copy is too large to compare")}
	}
	var parts []string
	for _, p := range changed {
		parts = append(parts, sanitize(p)+" changed")
	}
	for _, p := range added {
		parts = append(parts, sanitize(p)+" only on "+label(b))
	}
	for _, p := range removed {
		parts = append(parts, sanitize(p)+" only on "+label(a))
	}
	lines := []string{fmt.Sprintf("  %s differ:", nounCount(len(parts), "file", "files"))}
	for _, part := range parts[:min(len(parts), 5)] {
		lines = append(lines, "    "+part)
	}
	if len(parts) > 5 {
		lines = append(lines, fmt.Sprintf("    and %d more", len(parts)-5))
	}
	known := true
	for _, c := range []toolsdiff.Copy{a, b} {
		for _, f := range c.Manifest.Files {
			if f.Fingerprint == "" {
				known = false
			}
		}
	}
	switch {
	case known && len(removed) > 0 && len(added) == 0 && len(changed) == 0:
		lines = append(lines, "  keep "+label(a)+": it has every file in the other copy and more")
	case known && len(added) > 0 && len(removed) == 0 && len(changed) == 0:
		lines = append(lines, "  keep "+label(b)+": it has every file in the other copy and more")
	default:
		lines = append(lines, "  no clear winner: read the comparison")
	}
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
