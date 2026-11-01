package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolsdiff"
)

// toolScope is shared by filtering and actions, so a filtered action cannot
// accidentally target a different project or machine.
type toolScope struct{ projects, machines, words []string }

func (m Model) toolScope() toolScope {
	var s toolScope
	for _, word := range strings.Fields(m.tl.search) {
		key, value, has := strings.Cut(word, ":")
		if !has {
			s.words = append(s.words, strings.ToLower(word))
			continue
		}
		switch strings.ToLower(key) {
		case "node", "project":
			// A graph component is the network project, including all its checkouts.
			s.projects = append(s.projects, value)
		case "machine":
			s.machines = append(s.machines, value)
		default:
			s.words = append(s.words, strings.ToLower(word))
		}
	}
	return s
}
func (m Model) toolMatches(r toolsdiff.Row) bool {
	s := m.toolScope()
	for _, word := range s.words {
		if !strings.Contains(strings.ToLower(r.Name+" "+r.Kind+" "+m.toolStatus(r)), word) {
			return false
		}
	}
	for _, machine := range s.machines {
		found := false
		for i, mc := range m.tl.cat.Machines {
			if strings.EqualFold(machine, mc.Name) || machine == mc.ID {
				found = i < len(r.Cells) && r.Cells[i].State != toolsdiff.CellOff
			}
		}
		if !found {
			return false
		}
	}
	// Include available tools in project scope as well as active ones. The
	// status says off in this scope, and Enter can turn one on directly.
	return true
}
func (m Model) toolAttention(r toolsdiff.Row) bool {
	for _, d := range m.tl.cat.Diffs {
		if d.Name == r.Name && !d.Info && !m.tl.accepted[d.Key] {
			return true
		}
	}
	return r.Note != ""
}
func (m Model) toolStatus(r toolsdiff.Row) string {
	if r.Kind == protocol.InventorySkill && r.Item.Desired == nil && len(r.Item.Locations) == 0 {
		return "in the store, off everywhere"
	}
	for _, d := range m.tl.cat.Diffs {
		if d.Name == r.Name && d.Kind == toolsdiff.KindTwoCopies {
			return strings.TrimPrefix(strings.TrimPrefix(d.Title, r.Name+" has "), r.Name+" ")
		}
	}
	if r.Origin == toolsdiff.OriginRepo {
		return "in the " + r.Repo + " repo"
	}
	if r.Kind == protocol.InventoryMCP && r.Item.Federated {
		health := "healthy"
		if !r.Item.HostOnline {
			health = "host offline"
		}
		return "on " + r.Item.Host + ", " + health
	}
	if len(m.toolScope().projects) > 0 {
		for _, scope := range m.toolScope().projects {
			on := false
			if r.Item.Desired != nil {
				a := r.Item.Desired.Assignment
				on = a.Scope == "global" || (a.Scope == "projects" && hasStr(a.Projects, scope))
			}
			for _, p := range r.Projects {
				on = on || p.Name == scope
			}
			if !on {
				return "off for " + scope + "; available to turn on"
			}
		}
		return "on for " + strings.Join(m.toolScope().projects, ", ")
	}
	var on, versions []string
	for i, c := range r.Cells {
		if i >= len(m.tl.cat.Machines) {
			break
		}
		mc := m.tl.cat.Machines[i].Name
		if c.State != toolsdiff.CellOff {
			on = append(on, mc)
		}
		if c.State == toolsdiff.CellOther {
			versions = append(versions, c.Label+" on "+mc)
		} else if c.State != toolsdiff.CellOff && r.Version != "" {
			versions = append(versions, r.Version+" on "+mc)
		}
	}
	for _, c := range r.Cells {
		if c.State == toolsdiff.CellOther {
			return strings.Join(versions, ", ")
		}
	}
	if r.Origin == toolsdiff.OriginMachines {
		return "found on " + nounCount(len(on), "machine", "machines") + ", not managed"
	}
	if r.Origin == toolsdiff.OriginLocal {
		return "local folder in " + r.Repo + ", not managed"
	}
	if len(r.Projects) > 0 {
		var ps []string
		for _, p := range r.Projects {
			ps = append(ps, p.Name)
		}
		return "on for " + strings.Join(ps, ", ")
	}
	if len(on) == len(r.Cells) && len(on) > 0 {
		if r.Note != "" {
			return "on everywhere; " + r.Note
		}
		return "on everywhere"
	}
	if len(on) == 0 {
		return "off everywhere"
	}
	return "on " + strings.Join(on, ", ") + " only"
}
func (m Model) toolListView(w, h int) []string {
	title := "Tools"
	if m.tl.search != "" || m.tl.searching {
		title += " / " + sanitize(m.tl.search) + searchCursor(m.tl.searching)
	}
	nameW := 32
	statusW := max(12, w-nameW-13)
	body := []string{"  " + cell("NAME", nameW) + cell("KIND", 7) + "STATUS"}
	if !m.tl.loaded {
		body = append(body, orStr(m.tl.err, "reading the tools..."))
	}
	rows := m.shownRows()
	room := max(h-4, 1)
	// Wrap status at word boundaries, keeping all columns aligned. Never cut a
	// status in the middle of a word, even at 80 columns.
	var entries [][]string
	for i, r := range rows {
		flag := "  "
		if m.toolAttention(r) {
			flag = "! "
		}
		kind := "skill"
		if r.Kind == protocol.InventoryMCP {
			kind = "MCP"
		}
		status := wrap(sanitize(m.toolStatus(r)), statusW)
		line := flag + cell(r.Name, nameW-1) + " " + cell(kind, 7) + status[0]
		entry := []string{line}
		for _, s := range status[1:] {
			entry = append(entry, strings.Repeat(" ", nameW+9)+s)
		}
		if i == m.tl.row {
			for j := range entry {
				entry[j] = selectedRow(padStyled(entry[j], w-4))
			}
		}
		entries = append(entries, entry)
	}
	start := min(m.tl.scroll, max(len(rows)-1, 0))
	if m.tl.row < start {
		start = m.tl.row
	}
	for {
		used := 0
		for i := start; i <= m.tl.row && i < len(entries); i++ {
			used += len(entries[i])
		}
		if used <= room || start >= m.tl.row {
			break
		}
		start++
	}
	for i := start; i < len(entries); i++ {
		if len(body)-1+len(entries[i]) > room {
			break
		}
		body = append(body, entries[i]...)
	}
	for len(body) < room+1 {
		body = append(body, "")
	}
	note := fmt.Sprintf("%d tools · ! needs attention · enter opens actions", len(rows))
	if toast := m.activeToast(); toast != "" {
		note = toast
	}
	body = append(body, tsDim.Render(note))
	return panel(title, body, w, roleAccent)
}

type toolAction struct {
	label string
	run   func(Model) (tea.Model, tea.Cmd)
}
type toolMenu struct {
	actions []toolAction
	cur     int
}

func toolMenuLines(p *toolMenu) []string {
	var out []string
	for i, a := range p.actions {
		lead := "  "
		if i == p.cur {
			lead = "> "
		}
		out = append(out, lead+a.label)
	}
	return out
}
func (m Model) openToolMenu() (tea.Model, tea.Cmd) {
	r, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	menu := &toolMenu{}
	add := func(label string, f func(Model) (tea.Model, tea.Cmd)) {
		menu.actions = append(menu.actions, toolAction{label, f})
	}
	add("Read it", func(m Model) (tea.Model, tea.Cmd) { return m.readTool(r) })
	if r.Origin != toolsdiff.OriginRepo && (r.Kind != protocol.InventoryMCP || r.Item.Federated) {
		add("Turn on for...", func(m Model) (tea.Model, tea.Cmd) { return m.toolAssignScope(r, true) })
		add("Turn off for...", func(m Model) (tea.Model, tea.Cmd) { return m.toolAssignScope(r, false) })
	}
	add("Remove everywhere", func(m Model) (tea.Model, tea.Cmd) { return m.removeTool(r) })
	hasCompare := false
	for _, d := range m.tl.cat.Diffs {
		if d.Name == r.Name && d.Action.Kind == toolsdiff.ActChoose {
			hasCompare = true
			act := d.Action
			add("Compare the copies / Keep this one", func(m Model) (tea.Model, tea.Cmd) { return m.openChoose("Compare, then keep one", act) })
			break
		}
	}
	if !hasCompare && r.Kind == protocol.InventorySkill && m.toolAttention(r) {
		hashes := map[string]bool{}
		seen := map[string]bool{}
		var copies []toolsdiff.Copy
		for _, variant := range r.Variants {
			for _, loc := range variant.Locations {
				if loc.Scope != protocol.InventoryUser || loc.State != protocol.InventoryOK || loc.Source != protocol.InventoryNative {
					continue
				}
				key := loc.NodeID + "\x00" + loc.Location
				if seen[key] {
					continue
				}
				seen[key] = true
				hashes[variant.Hash] = true
				label := ""
				if variant.VersionNo > 0 {
					label = fmt.Sprintf("v%d", variant.VersionNo)
				}
				copies = append(copies, toolsdiff.Copy{Name: r.Name, Item: variant.ID, NodeID: loc.NodeID, Machine: loc.Machine, Location: loc.Location, Hash: variant.Hash, Harness: loc.Harness, Label: label, Manifest: loc.Manifest})
			}
		}
		if len(hashes) > 1 {
			act := toolsdiff.Action{Kind: toolsdiff.ActChoose, Name: r.Name, Copies: copies}
			add("Compare the copies / Keep this one", func(m Model) (tea.Model, tea.Cmd) { return m.openChoose("Compare, then keep one", act) })
		}
	}
	if r.Kind == protocol.InventoryMCP && r.Item.Federated {
		add("Test it", func(m Model) (tea.Model, tea.Cmd) { return m.mcpToolAction("test", r.Name) })
	}
	if r.Origin == toolsdiff.OriginMachines || r.Origin == toolsdiff.OriginLocal {
		add("Import it", func(m Model) (tea.Model, tea.Cmd) {
			if r.Kind == protocol.InventoryMCP {
				return m.openMCPImport(r.Name)
			}
			return m.importTool(r)
		})
	}
	md := m.openModal(r.Name + " actions")
	md.menu = menu
	md.foot = []string{"j/k", "move", "enter", "choose", "esc", "back"}
	return m, nil
}
func (m Model) toolMenuKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.tl.modal.menu
	switch k.String() {
	case "esc":
		m.tl.modal = nil
	case "j", "down":
		p.cur = min(p.cur+1, len(p.actions)-1)
	case "k", "up":
		p.cur = max(p.cur-1, 0)
	case "enter":
		return p.actions[p.cur].run(m)
	}
	return m, nil
}
func (m Model) toolAssignScope(r toolsdiff.Row, on bool) (tea.Model, tea.Cmd) {
	s := m.toolScope()
	if len(s.projects) == 0 && len(s.machines) == 0 {
		return m.openTargetPicker(r, on)
	}
	target := m.assignTarget(on)
	if len(s.projects) > 0 {
		projects := append([]string(nil), s.projects...)
		if !on {
			projects = nil
			if r.Item.Desired != nil {
				for _, p := range r.Item.Desired.Assignment.Projects {
					if !hasStr(s.projects, p) {
						projects = append(projects, p)
					}
				}
			}
			if r.Item.Desired != nil && r.Item.Desired.Assignment.Scope == "global" {
				for _, p := range m.toolsProjects() {
					if !hasStr(s.projects, p) {
						projects = append(projects, p)
					}
				}
			}
		}
		if len(projects) > 0 {
			target.Scope = "projects"
			target.Projects = projects
		}
	}
	for _, name := range s.machines {
		for _, mc := range m.tl.cat.Machines {
			if strings.EqualFold(name, mc.Name) || name == mc.ID {
				target.Machines = append(target.Machines, mc.ID)
			}
		}
	}
	job, err := m.jobFor(r, target)
	if err != "" {
		m.toolsToast(err)
		return m, nil
	}
	verb := "Turn on "
	if !on {
		verb = "Turn off "
	}
	return m.openAssign(verb+r.Name, "apply", []assignJob{job}, nil)
}
func (m Model) openTargetPicker(r toolsdiff.Row, on bool) (tea.Model, tea.Cmd) {
	p := &projPicker{sel: map[string]bool{}, row: r, remove: !on, targets: true}
	if r.Kind != protocol.InventoryMCP {
		for _, mc := range m.tl.cat.Machines {
			p.all = append(p.all, "Machine: "+mc.Name)
		}
	}
	for _, project := range m.toolsProjects() {
		p.all = append(p.all, "Project: "+project)
	}
	title := "Turn on "
	if !on {
		title = "Turn off "
	}
	md := m.openModal(title + r.Name + " for...")
	md.picker = p
	pickerFoot(md, p)
	return m, nil
}
func (m Model) targetPickerConfirm() (tea.Model, tea.Cmd) {
	p := m.tl.modal.picker
	var chosen []string
	for s, ticked := range p.sel {
		if ticked {
			chosen = append(chosen, s)
		}
	}
	sort.Strings(chosen)
	if len(chosen) == 0 {
		m.toolsToast("Tick at least one machine or project, then press enter.")
		return m, nil
	}
	var jobs []assignJob
	var projects []string
	for _, name := range chosen {
		if project, ok := strings.CutPrefix(name, "Project: "); ok {
			projects = append(projects, project)
			continue
		}
		for _, mc := range m.tl.cat.Machines {
			if name == "Machine: "+mc.Name {
				job, err := m.jobFor(p.row, m.machineTarget(!p.remove, mc))
				if err != "" {
					m.toolsToast(err)
					return m, nil
				}
				job.where = mc.Name
				jobs = append(jobs, job)
			}
		}
	}
	if len(projects) > 0 {
		target := protocol.ToolAssignment{Scope: "projects", Projects: projects}
		if p.remove {
			target.Projects = nil
			current := p.row.Item.Desired
			if current != nil && current.Assignment.Scope == "global" {
				for _, name := range m.toolsProjects() {
					if !hasStr(projects, name) {
						target.Projects = append(target.Projects, name)
					}
				}
			} else {
				for _, ps := range p.row.Projects {
					if !hasStr(projects, ps.Name) {
						target.Projects = append(target.Projects, ps.Name)
					}
				}
			}
			if len(target.Projects) == 0 {
				target.Scope = "uninstalled"
			}
		}
		job, err := m.jobFor(p.row, target)
		if err != "" {
			m.toolsToast(err)
			return m, nil
		}
		jobs = append(jobs, job)
	}
	verb := "Turn on "
	if p.remove {
		verb = "Turn off "
	}
	md := m.tl.modal
	md.title = verb + p.row.Name
	md.picker = nil
	md.lines = jobLines(jobs)
	return m.runJobs(jobs)
}
func (m Model) readTool(r toolsdiff.Row) (tea.Model, tea.Cmd) {
	md := m.openModal("Read " + r.Name)
	md.foot = []string{"j/k", "scroll", "esc", "back"}
	md.loading = true
	id := md.id
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*Timeout)
		defer cancel()
		var text string
		var err error
		if r.Kind == protocol.InventoryMCP {
			if src, ok := m.src.(toolsMCPSource); ok {
				var servers []protocol.MCPServer
				servers, err = src.ToolsMCPList(ctx)
				for _, srv := range servers {
					if srv.Name == r.Name {
						var b []byte
						b, err = json.MarshalIndent(srv, "", "  ")
						text = string(b)
					}
				}
				if err == nil && text == "" {
					if ts, ok := m.toolsSource(); ok {
						var candidates []protocol.MCPImportCandidate
						candidates, err = ts.MCPCandidates(ctx)
						for _, candidate := range candidates {
							if candidate.Name == r.Name {
								var config any = candidate
								if candidate.Class == protocol.MCPImportable {
									var plan protocol.MCPImportPlan
									plan, err = ts.MCPImportPlan(ctx, candidate.Key)
									config = plan.Server
								}
								if err == nil {
									var b []byte
									b, err = json.MarshalIndent(config, "", "  ")
									text = string(b)
								}
								break
							}
						}
					}
					if err == nil && text == "" {
						err = fmt.Errorf("no current server config is available")
					}
				}
			} else {
				err = fmt.Errorf("MCP config preview unavailable")
			}
		} else if reader, ok := m.src.(skillFileReader); ok {
			loc := protocol.ToolImportLocation{}
			itemID := r.Item.ID
			for _, v := range r.Variants {
				for _, l := range v.Locations {
					if l.State == protocol.InventoryOK {
						loc = protocol.ToolImportLocation{NodeID: l.NodeID, Location: l.Location}
						itemID = v.ID
						break
					}
				}
				if loc.Location != "" {
					break
				}
			}
			text, err = reader.ToolsReadFile(ctx, protocol.SkillTextRequest{Item: itemID, Location: loc, Path: "SKILL.md"})
		} else {
			err = fmt.Errorf("skill preview unavailable")
		}
		return toolsModalMsg{id: id, update: func(md *toolsModal) {
			md.loading = false
			if err != nil {
				md.lines = []string{err.Error()}
			} else {
				for _, line := range strings.Split(text, "\n") {
					md.lines = append(md.lines, sanitize(line))
				}
			}
		}}
	}
}
func (m Model) removeTool(r toolsdiff.Row) (tea.Model, tea.Cmd) {
	if r.Origin == toolsdiff.OriginRepo {
		return m.openRepoRemove(r.Repo, r.Name, repoCopies(r))
	}
	md := m.openModal("Remove " + r.Name + " everywhere?")
	md.lines = []string{"Remove installed copies everywhere. Verified skill copies are backed up; the stored version remains available."}
	md.foot = []string{"enter", "confirm removal", "esc", "cancel"}
	md.actions["enter"] = func(m Model) (Model, tea.Cmd) {
		if r.Kind == protocol.InventoryMCP {
			model, cmd := m.mcpToolAction("remove", r.Name)
			return model.(Model), cmd
		}
		job, err := m.jobFor(r, protocol.ToolAssignment{Scope: "uninstalled"})
		if err != "" {
			m.toolsToast(err)
			return m, nil
		}
		// A merged row can contain several content versions. Back up all
		// exact observed user copies, preserving a canonical version first.
		if len(r.Variants) > 1 {
			choice := &protocol.ToolCopyChoice{RemoveAll: true}
			for _, variant := range r.Variants {
				for _, loc := range variant.Locations {
					if loc.Scope != protocol.InventoryUser || loc.Source != protocol.InventoryNative || loc.State != protocol.InventoryOK {
						continue
					}
					if choice.Chosen.Item == "" {
						choice.Chosen = protocol.ToolImport{Item: variant.ID, Location: protocol.ToolImportLocation{NodeID: loc.NodeID, Location: loc.Location}}
					}
					choice.Copies = append(choice.Copies, protocol.ToolSkillScan{NodeID: loc.NodeID, Name: r.Name, Location: loc.Location, Hash: variant.Hash})
				}
			}
			if len(choice.Copies) > 0 {
				job.choice = choice
			}
		}
		// Confirmation authorizes the import, guarded removal and assignment.
		return m.runJobs([]assignJob{job})
	}
	return m, nil
}
func (m Model) importTool(r toolsdiff.Row) (tea.Model, tea.Cmd) {
	c, ok := firstCopy(r)
	if !ok {
		m.toolsToast("no readable copy to import")
		return m, nil
	}
	md := m.openModal("Import " + r.Name)
	md.loading = true
	md.foot = []string{"esc", "back"}
	id := md.id
	src, _ := m.toolsSource()
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*Timeout)
		defer cancel()
		err := src.ToolsImport(ctx, protocol.ToolImport{Item: c.Item, Location: protocol.ToolImportLocation{NodeID: c.NodeID, Location: c.Location}})
		return toolsModalMsg{id: id, reload: err == nil, update: func(md *toolsModal) {
			md.loading = false
			md.done = true
			md.lines = []string{"Imported into the store."}
			if err != nil {
				md.lines = []string{err.Error()}
			}
			md.foot = []string{"enter", "close", "esc", "back"}
		}}
	}
}

type toolsMCPSource interface {
	ToolsMCPAdd(context.Context, protocol.MCPAdd) error
	ToolsMCPRemove(context.Context, string) error
	ToolsMCPTest(context.Context, string) error
	ToolsMCPList(context.Context) ([]protocol.MCPServer, error)
}

func (m Model) mcpToolAction(action, name string) (tea.Model, tea.Cmd) {
	src, ok := m.src.(toolsMCPSource)
	if !ok {
		m.toolsToast("MCP actions unavailable")
		return m, nil
	}
	md := m.openModal(action + " " + name)
	md.loading = true
	id := md.id
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*Timeout)
		defer cancel()
		var err error
		if action == "remove" {
			err = src.ToolsMCPRemove(ctx, name)
		} else {
			err = src.ToolsMCPTest(ctx, name)
		}
		return toolsModalMsg{id: id, reload: err == nil, update: func(md *toolsModal) {
			md.loading = false
			md.done = true
			md.lines = []string{action + " completed"}
			if err != nil {
				md.lines = []string{err.Error()}
			}
			md.foot = []string{"enter", "close", "esc", "back"}
		}}
	}
}

type toolAddForm struct {
	kind     string
	values   []string
	field    int
	machines []toolsdiff.Machine
	machine  int
}

func (m Model) openToolAdd() (tea.Model, tea.Cmd) {
	menu := &toolMenu{actions: []toolAction{
		{"MCP server", func(m Model) (tea.Model, tea.Cmd) { return m.openToolAddForm("MCP server") }},
		{"Skill from a folder", func(m Model) (tea.Model, tea.Cmd) { return m.openToolAddForm("Skill from a folder") }},
	}}
	md := m.openModal("Add a tool")
	md.menu = menu
	md.foot = []string{"j/k", "move", "enter", "choose", "esc", "back"}
	return m, nil
}
func (m Model) openToolAddForm(kind string) (tea.Model, tea.Cmd) {
	md := m.openModal("Add " + kind)
	n := 5
	if kind != "MCP server" {
		n = 1
	}
	md.form = &toolAddForm{kind: kind, values: make([]string, n), machines: m.tl.cat.Machines}
	md.foot = []string{"tab", "next field", "up/down", "field", "left/right", "machine", "enter", "add", "esc", "cancel"}
	return m, nil
}
func toolAddLines(f *toolAddForm) []string {
	labels := []string{"Name", "Command (or URL)", "Args (JSON array)", "Env variable names", "Header sources (JSON)"}
	if f.kind != "MCP server" {
		labels = []string{"Folder path"}
	}
	out := []string{"Secrets: use env variable names or file paths only."}
	for i, label := range labels {
		lead := "  "
		if i == f.field {
			lead = "> "
		}
		out = append(out, lead+label+": "+sanitize(f.values[i]))
	}
	machine := "no machine available"
	if len(f.machines) > 0 {
		machine = f.machines[f.machine].Name
	}
	lead := "  "
	if f.field == len(f.values) {
		lead = "> "
	}
	out = append(out, lead+"Machine: "+machine)
	return out
}
func (m Model) toolAddKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.tl.modal.form
	n := len(f.values)
	switch k.String() {
	case "esc":
		m.tl.modal = nil
	case "tab", "down":
		f.field = (f.field + 1) % (n + 1)
	case "shift+tab", "up":
		f.field = (f.field + n) % (n + 1)
	case "left", "right":
		if f.field == n && len(f.machines) > 0 {
			step := 1
			if k.String() == "left" {
				step = len(f.machines) - 1
			}
			f.machine = (f.machine + step) % len(f.machines)
		}
	case "enter":
		return m.submitToolAdd()
	case "backspace", "ctrl+h":
		if f.field < n {
			r := []rune(f.values[f.field])
			if len(r) > 0 {
				f.values[f.field] = string(r[:len(r)-1])
			}
		}
	default:
		if f.field < n && (k.Type == tea.KeyRunes || k.Type == tea.KeySpace) {
			f.values[f.field] += string(k.Runes)
		}
	}
	return m, nil
}
func (m Model) submitToolAdd() (tea.Model, tea.Cmd) {
	md := m.tl.modal
	f := md.form
	if len(f.machines) == 0 {
		m.toolsToast("Choose an online machine first.")
		return m, nil
	}
	mc := f.machines[f.machine]
	var run func(context.Context) error
	if f.kind == "MCP server" {
		server := protocol.MCPServer{Name: strings.TrimSpace(f.values[0]), Transport: protocol.MCPStdio, Command: strings.TrimSpace(f.values[1]), Env: strings.Fields(f.values[3])}
		if strings.HasPrefix(server.Command, "https://") || strings.HasPrefix(server.Command, "http://") {
			server.Transport = protocol.MCPHTTP
			server.URL = server.Command
			server.Command = ""
		}
		if f.values[2] != "" {
			if err := json.Unmarshal([]byte(f.values[2]), &server.Args); err != nil {
				m.toolsToast("Args must be a JSON array of strings.")
				return m, nil
			}
		}
		if f.values[4] != "" {
			if err := json.Unmarshal([]byte(f.values[4]), &server.Headers); err != nil {
				m.toolsToast("Headers must name env or file sources in a JSON array.")
				return m, nil
			}
		}
		if err := server.Validate(); err != nil {
			m.toolsToast(err.Error())
			return m, nil
		}
		src, ok := m.src.(toolsMCPSource)
		if !ok {
			m.toolsToast("MCP actions unavailable")
			return m, nil
		}
		run = func(ctx context.Context) error {
			return src.ToolsMCPAdd(ctx, protocol.MCPAdd{Server: server, On: mc.ID})
		}
	} else {
		src, ok := m.src.(interface {
			ToolsImportFolder(context.Context, protocol.ToolImportLocation) error
		})
		if !ok {
			m.toolsToast("folder import unavailable")
			return m, nil
		}
		path := strings.TrimSpace(f.values[0])
		if path == "" {
			m.toolsToast("Enter a folder path.")
			return m, nil
		}
		run = func(ctx context.Context) error {
			return src.ToolsImportFolder(ctx, protocol.ToolImportLocation{NodeID: mc.ID, Location: path})
		}
	}
	md.form = nil
	md.loading = true
	md.lines = []string{"Adding..."}
	md.foot = []string{"esc", "back"}
	id := md.id
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*Timeout)
		defer cancel()
		err := run(ctx)
		return toolsModalMsg{id: id, reload: err == nil, update: func(md *toolsModal) {
			md.loading = false
			md.done = true
			md.lines = []string{"Added."}
			if err != nil {
				md.lines = []string{err.Error()}
			}
			md.foot = []string{"enter", "close", "esc", "back"}
		}}
	}
}

func (m Model) startNodeTools(name string) (tea.Model, tea.Cmd) {
	m.tl.search = "node:" + name
	m.tl.row, m.tl.scroll = 0, 0
	return m.startTools()
}
func (m Model) nodeToolsLine(name string) string {
	skills, servers := 0, 0
	scoped := m
	scoped.tl.search = ""
	for _, r := range scoped.shownRows() {
		on := r.Item.Scope == "global"
		if r.Item.Federated && r.Item.Scope == "" {
			on = true
		}
		if r.Item.Desired != nil {
			a := r.Item.Desired.Assignment
			on = a.Scope == "global" || (a.Scope == "projects" && hasStr(a.Projects, name))
		}
		for _, p := range r.Projects {
			on = on || p.Name == name
		}
		if r.Origin == toolsdiff.OriginMachines {
			for _, c := range r.Cells {
				on = on || c.State != toolsdiff.CellOff
			}
		}
		if on {
			if r.Kind == protocol.InventoryMCP {
				servers++
			} else {
				skills++
			}
		}
	}
	return fmt.Sprintf("Tools: %d skills, %d MCP servers · t open", skills, servers)
}
