package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolsdiff"
)

// Save every distinct outside version. Importing leaves all native copies
// untouched, including different copies with the same name on two machines.
func (m Model) outsideSkillCopies() []toolsdiff.Copy {
	var copies []toolsdiff.Copy
	seen := map[string]bool{}
	for _, it := range m.tl.items {
		if it.Kind != protocol.InventorySkill || it.Desired != nil || it.VersionNo > 0 || seen[it.ID] {
			continue
		}
		for _, loc := range it.Locations {
			if loc.State != protocol.InventoryOK || loc.Source != protocol.InventoryNative || !(loc.Scope == protocol.InventoryUser || loc.Scope == protocol.InventoryProject && !loc.Tracked) {
				continue
			}
			copies = append(copies, toolsdiff.Copy{Item: it.ID, Name: it.Name, Hash: it.Hash, NodeID: loc.NodeID, Machine: loc.Machine, Location: loc.Location})
			seen[it.ID] = true
			break
		}
	}
	sort.Slice(copies, func(i, j int) bool {
		if copies[i].Name != copies[j].Name {
			return copies[i].Name < copies[j].Name
		}
		return copies[i].Item < copies[j].Item
	})
	return copies
}
func (m Model) outsideSkillsCount() int {
	names := map[string]bool{}
	for _, c := range m.outsideSkillCopies() {
		names[c.Name] = true
	}
	return len(names)
}
func (m Model) importAllSkills() (tea.Model, tea.Cmd) {
	var jobs []assignJob
	for _, c := range m.outsideSkillCopies() {
		c := c
		jobs = append(jobs, assignJob{name: c.Name, item: c.Item, imp: &c, importOnly: true})
	}
	if len(jobs) == 0 {
		m.toolsToast("No skills outside agent-net to import.")
		return m, nil
	}
	return m.openAssign(fmt.Sprintf("Import all · %d skills", m.outsideSkillsCount()), "import", jobs, []string{"Backups kept in the store. Nothing is turned off.", "Every installed copy stays where it is, on for the same sessions."})
}

func (m Model) outsideToolStatus(r toolsdiff.Row) string {
	var machines []string
	for i, c := range r.Cells {
		if i < len(m.tl.cat.Machines) && c.State != toolsdiff.CellOff {
			machines = append(machines, m.tl.cat.Machines[i].Name)
		}
	}
	return "installed on " + strings.Join(machines, ", ") + ", outside agent-net (on for every session there)"
}
