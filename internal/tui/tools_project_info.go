package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolsdiff"
)

func (m Model) agentSkillCost(r toolsdiff.Row) int64 {
	cost := r.Item.StartTokens
	for _, it := range m.tl.items {
		if it.Kind == r.Kind && it.Name == r.Name {
			cost = max(cost, it.StartTokens)
		}
	}
	return cost
}

func (m Model) agentMachineName(id string) string {
	for _, n := range m.snap.Nodes {
		if n.NodeID == id {
			return orStr(n.Hostname, id)
		}
	}
	return id
}

func (m Model) agentInfoView(w int) []string {
	title := "Project information"
	var body []string
	add := func(s string) { body = append(body, wordsLines(s, w-4)...) }
	project := m.agentProject()
	if m.tl.skillsFocus && len(m.agentSkills()) > 0 {
		title = "Skill information"
		rows := m.agentSkills()
		r := rows[min(m.tl.skillRow, len(rows)-1)]
		add(sanitize(r.Name) + " · " + tokenCountText(m.agentSkillCost(r)) + " per session")
		desc := wordsLines(sanitize(r.Item.Description), w-4)
		if len(desc) > 2 {
			desc = desc[:2]
			desc[1] = clip(desc[1], max(0, w-5)) + "…"
		}
		body = append(body, desc...)
		n := 0
		for _, p := range m.toolsProjects() {
			if on, _ := m.agentSkillState(rowKey(r.Kind, r.Name), p); on {
				n++
			}
		}
		add(fmt.Sprintf("on in %d of %d projects", n, len(m.toolsProjects())))
		machines := map[string]bool{}
		newProjects := r.Item.Desired != nil && r.Item.Desired.Assignment.Scope == "global"
		for _, v := range r.Variants {
			for _, l := range v.Locations {
				machines[m.agentMachineName(l.NodeID)] = true
				if r.Item.Desired == nil && l.Scope == protocol.InventoryUser {
					newProjects = true
				}
			}
		}
		for _, st := range m.tl.states {
			if st.Item == m.agentItemID(r) {
				newProjects = st.NewProjects
				for _, id := range st.Machines {
					machines[m.agentMachineName(id)] = true
				}
			}
		}
		var names []string
		for name := range machines {
			if name != "" {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		hashes := map[string]bool{}
		for _, it := range m.tl.items {
			if it.Kind == r.Kind && it.Name == r.Name {
				hashes[it.Hash] = true
			}
		}
		if len(hashes) > 1 {
			add(fmt.Sprintf("%d versions: %s differ", len(hashes), strings.Join(names, " and ")))
		}
		add("Machines: " + orStr(strings.Join(names, ", "), "none observed"))
		add("New projects: " + onOff(newProjects))
		if why := m.skillLockReason(r, project); why != "" {
			add(why)
		} else if r.Origin == toolsdiff.OriginMachines || r.Origin == toolsdiff.OriginLocal {
			add(m.outsideToolStatus(r))
		} else if r.Origin == toolsdiff.OriginRepo {
			add("In the " + r.Repo + " repo (always on). Change this skill in the repo.")
		}
	} else {
		add(orStr(project, "No project selected"))
		var machines []string
		for _, p := range m.projectRows() {
			if p.Name == project {
				name := orStr(p.Hostname, m.agentMachineName(p.NodeID))
				if !hasStr(machines, name) {
					machines = append(machines, name)
				}
			}
		}
		sort.Strings(machines)
		add("Machines: " + orStr(strings.Join(machines, ", "), "no known checkout"))
		n, c, _, _ := m.projectCost(project)
		add(nounCount(n, "skill", "skills") + " on · " + tokenCountText(c) + " per session")
		for _, reason := range m.tl.projectReasons[project] {
			add("Cannot hold project skills: " + reason)
		}
		if len(machines) == 0 {
			add("Cannot hold project skills: no known checkout")
		}
	}
	return panel(title, body, w, roleAccent)
}
