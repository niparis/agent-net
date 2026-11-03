package tui

import (
	"context"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolsdiff"
	"sort"
	"strings"
)

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
	seen := map[string]bool{}
	represented := map[string]bool{}
	for _, t := range plan.Targets {
		if t.Conflict == "" {
			continue
		}
		represented[t.Conflict] = true
		who := strings.TrimSpace(t.Machine + " " + t.Project)
		line := t.Conflict
		if who != "" {
			line = who + ": " + line
		}
		if !seen[line] {
			seen[line] = true
			lines = append(lines, tsWarn.Render("! ")+sanitize(line))
		}
	}
	for _, c := range plan.Conflicts {
		if c != "" && !represented[c] && !seen[c] {
			seen[c] = true
			lines = append(lines, tsWarn.Render("! ")+sanitize(c))
		}
	}
	if len(plan.Skipped) > 0 {
		groups := map[string][]string{}
		var machines []string
		seenSkip := map[string]bool{}
		for _, skip := range plan.Skipped {
			reason := skip.Reason
			if reason == "destination contains a symlink" {
				reason = "its skill folder is a symlink"
			}
			item := sanitize(skip.Project) + " (" + sanitize(reason) + ")"
			key := skip.Machine + "\x00" + item
			if seenSkip[key] {
				continue
			}
			seenSkip[key] = true
			if _, ok := groups[skip.Machine]; !ok {
				machines = append(machines, skip.Machine)
			}
			groups[skip.Machine] = append(groups[skip.Machine], item)
		}
		for _, machine := range machines {
			line := "Skipped, cannot hold project skills: " + strings.Join(groups[machine], ", ")
			if len(machines) > 1 {
				line = sanitize(machine) + ": " + line
			}
			lines = append(lines, line)
		}
	}
	move := assignmentMove(name, current, plan.Assignment)
	if move != "" {
		lines = append(lines, move)
	}
	if len(plan.Assignment.Machines) == 1 {
		lines = append(lines, "It applies on one machine only; the other machines keep what they have.")
	}
	copies := map[string]map[string]bool{}
	for _, target := range plan.Targets {
		if target.Action != "copy" || target.Conflict != "" || target.Project == "" {
			continue
		}
		if copies[target.Machine] == nil {
			copies[target.Machine] = map[string]bool{}
		}
		copies[target.Machine][target.Project] = true
	}
	var machines []string
	for machine := range copies {
		machines = append(machines, machine)
	}
	sort.Strings(machines)
	for _, machine := range machines {
		lines = append(lines, "Copies to "+nounCount(len(copies[machine]), "project", "projects")+" on "+sanitize(machine)+".")
	}
	for _, t := range plan.Targets {
		if t.Action == "remove" && t.Source != "" && t.Conflict == "" {
			line := "Moves " + shortSkillPath(t.Source) + " on " + sanitize(t.Machine) + " to agent-net's backup."
			if !seen[line] {
				seen[line] = true
				lines = append(lines, line)
			}
		}
	}
	if !planCanApply(plan) {
		why := "no eligible target was found"
		if len(plan.Conflicts) > 0 {
			why = "fix the conflicts above, then try again"
		}
		for _, t := range plan.Targets {
			if t.Conflict != "" {
				why = "fix the conflicts above, then try again"
				break
			}
		}
		lines = append(lines, tsErr.Render("Nothing changed: "+why+"."))
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

func shortSkillPath(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) > 3 && (parts[1] == "home" || parts[1] == "Users") {
		return sanitize("~/" + strings.Join(parts[3:], "/"))
	}
	return sanitize(path)
}

func (m Model) agentItemID(r toolsdiff.Row) string {
	for _, v := range r.Variants {
		if v.Desired != nil {
			return v.ID
		}
	}
	return r.Item.ID
}

func (m Model) agentAssignment(r toolsdiff.Row) protocol.ToolAssignment {
	if r.Item.Desired != nil {
		return r.Item.Desired.Assignment
	}
	var projects []string
	for _, p := range m.toolsProjects() {
		if on, _ := m.agentSkillState(r.Name, p); on {
			projects = append(projects, p)
		}
	}
	return protocol.ToolAssignment{Scope: "projects", Projects: projects}
}

// reviewAgentChanges obtains every plan before confirmation. Applying one
// skill does not invalidate a plan for another skill.
func (m Model) reviewAgentChanges(project string) (tea.Model, tea.Cmd) {
	jobs, why := m.agentJobs(project)
	if why != "" {
		m.toolsToast(why)
		return m, nil
	}
	ts, _ := m.toolsSource()
	md := m.openModal("Review " + nounCount(len(jobs), "change", "changes") + " for " + project)
	md.loading = true
	md.planFoot("apply")
	id := md.id
	rows := m.agentSkillsUnfiltered()
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*Timeout)
		defer cancel()
		var plans []protocol.ToolPlan
		names := map[string]string{}
		reasons := map[string][]string{project: nil}
		var lines, paths []string
		for _, j := range jobs {
			plan, err := ts.ToolsToggle(ctx, protocol.ToolToggleRequest{Item: j.item, Project: project, On: j.agentOn})
			if err != nil {
				lines = append(lines, "Nothing changed: "+sanitize(err.Error()))
				continue
			}
			for _, skip := range plan.Skipped {
				reason := sanitize(skip.Machine + ": " + skip.Reason)
				if !hasStr(reasons[skip.Project], reason) {
					reasons[skip.Project] = append(reasons[skip.Project], reason)
				}
			}
			var current protocol.ToolAssignment
			for _, r := range rows {
				if r.Name == j.name {
					current = m.agentAssignment(r)
					break
				}
			}
			lines = append(lines, togglePlanLines(plan, current, j.name, project, j.agentOn)...)
			paths = append(paths, planPaths(plan)...)
			if planCanApply(plan) {
				plans = append(plans, plan)
				names[plan.ID] = j.name
			}
		}
		return toolsModalMsg{id: id, projectReasons: reasons, update: func(md *toolsModal) {
			md.loading = false
			md.lines = lines
			md.paths = paths
			if len(plans) == 0 {
				md.title = "Nothing changed"
				md.foot = []string{"esc", "close"}
				return
			}
			md.actions["enter"] = func(m Model) (Model, tea.Cmd) {
				for _, name := range names {
					delete(m.tl.agentPending[project], name)
				}
				return m.applyAgentPlans(plans)
			}
		}}
	}
}

func (m Model) applyAgentPlans(plans []protocol.ToolPlan) (Model, tea.Cmd) {
	ts, _ := m.toolsSource()
	md := m.tl.modal
	md.loading = true
	id := md.id
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time10min)
		defer cancel()
		applied := 0
		var lines, reasons []string
		for _, plan := range plans {
			res, err := ts.ToolsApply(ctx, plan.ID)
			if err != nil {
				reason := sanitize(err.Error())
				reasons = append(reasons, reason)
				lines = append(lines, "Nothing changed: "+reason)
				continue
			}
			for _, t := range res.Targets {
				if t.State == "ok" || t.State == "applied" {
					applied++
				} else {
					reasons = append(reasons, orStr(t.Reason, t.State))
				}
			}
			name := plan.Item
			for _, it := range m.tl.items {
				if it.ID == plan.Item {
					name = it.Name
					break
				}
			}
			lines = append(lines, outcomeLines(name, res)...)
		}
		return toolsModalMsg{id: id, reload: applied > 0, update: func(md *toolsModal) {
			md.loading = false
			md.done = true
			md.paths = nil
			md.foot = []string{"enter", "close"}
			if applied == 0 {
				md.title = "Nothing changed"
				md.lines = []string{"Nothing changed: " + orStr(strings.Join(reasons, "; "), "no target was changed")}
				return
			}
			md.title = "Applied: " + nounCount(applied, "target", "targets")
			md.lines = lines
		}}
	}
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
