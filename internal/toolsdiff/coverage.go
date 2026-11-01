package toolsdiff

import (
	"fmt"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// MCP coverage answers one question the machine grid does not: this project
// declares a server for some harnesses and not for others. A gap is a server
// the project already has, missing from an installed harness. A conflict is a
// same-name server whose declarations differ, which agent-net never replaces.

// States of one harness cell in a coverage row.
const (
	CoverageOn       = "on"
	CoverageMissing  = "missing"
	CoverageConflict = "conflict"
)

// MCPHarnessCell is one harness's state for one project server.
type MCPHarnessCell struct {
	Harness string `json:"harness"`
	State   string `json:"state"`
	Note    string `json:"note,omitempty"`
}

// MCPCoverageRow is one project-scoped server and the harnesses that have it.
type MCPCoverageRow struct {
	NodeID        string           `json:"node_id"`
	Machine       string           `json:"machine"`
	Project       string           `json:"project"`
	Server        string           `json:"server"`
	SourceHarness string           `json:"source_harness,omitempty"`
	SourcePath    string           `json:"source_path,omitempty"`
	Cells         []MCPHarnessCell `json:"cells"`
	Missing       []string         `json:"missing,omitempty"`
	Conflict      bool             `json:"conflict,omitempty"`
	Note          string           `json:"note,omitempty"`
	Action        Action           `json:"-"`
}

var coverageHarnessOrder = []string{"claude", "pi", "opencode", "codex"}

func harnessRank(h string) int {
	for i, v := range coverageHarnessOrder {
		if v == h {
			return i
		}
	}
	return len(coverageHarnessOrder)
}

// Title is the owner-facing line. With more than one machine it names the
// machine, because the same project can differ from machine to machine.
func (r MCPCoverageRow) Title(machineCount int) string {
	var cells []string
	for _, c := range r.Cells {
		switch c.State {
		case CoverageOn:
			cells = append(cells, c.Harness+" ✓")
		case CoverageConflict:
			cells = append(cells, c.Harness+" !")
		default:
			cells = append(cells, c.Harness+" ✗")
		}
	}
	line := r.Server + " · " + r.Project
	if len(cells) > 0 {
		line += " · " + strings.Join(cells, " ")
	}
	if r.Note != "" {
		line += " · " + r.Note
	}
	if machineCount > 1 {
		line += " · on " + r.Machine
	}
	return line
}

// coverageGroup collects the declarations of one server in one project on one
// machine.
type coverageGroup struct {
	node, machine, project, server string
	byHarness                      map[string][]protocol.ToolLocation
}

// MCPCoverage computes the rows for every project-scoped server the inventory
// sees. A machine without a harness list cannot be judged and is skipped.
func MCPCoverage(items []protocol.ToolItem, ms []Machine) []MCPCoverageRow {
	groups := map[string]*coverageGroup{}
	codexProjects := map[string]bool{}
	var order []string
	for _, it := range items {
		if it.Kind != protocol.InventoryMCP {
			continue
		}
		for _, l := range it.Locations {
			if l.Scope != protocol.InventoryProject || l.Project == "" || l.NodeID == "" {
				continue
			}
			if l.Harness == "codex" {
				codexProjects[l.NodeID+"\x00"+l.Project] = true
			}
			key := l.NodeID + "\x00" + l.Project + "\x00" + it.Name
			g := groups[key]
			if g == nil {
				g = &coverageGroup{node: l.NodeID, machine: l.Machine, project: l.Project, server: it.Name, byHarness: map[string][]protocol.ToolLocation{}}
				groups[key] = g
				order = append(order, key)
			}
			if g.machine == "" {
				g.machine = l.Machine
			}
			g.byHarness[l.Harness] = append(g.byHarness[l.Harness], l)
		}
	}
	installed := map[string]map[string]bool{}
	names := map[string]string{}
	for _, m := range ms {
		set := map[string]bool{}
		for _, h := range m.Harnesses {
			set[h] = true
		}
		installed[m.ID] = set
		names[m.ID] = m.Name
		for _, l := range items {
			for _, loc := range l.Locations {
				if loc.NodeID == m.ID && loc.Machine != "" {
					names[m.ID] = loc.Machine
				}
			}
		}
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	var out []MCPCoverageRow
	for _, key := range order {
		g := groups[key]
		set := installed[g.node]
		if len(set) == 0 {
			continue
		}
		codexCapable := codexProjects[g.node+"\x00"+g.project]
		if g.machine == "" {
			g.machine = names[g.node]
		}
		row := MCPCoverageRow{NodeID: g.node, Machine: g.machine, Project: g.project, Server: g.server}
		var present []string
		for h := range g.byHarness {
			if set[h] {
				present = append(present, h)
			}
		}
		sort.Slice(present, func(i, j int) bool {
			if harnessRank(present[i]) != harnessRank(present[j]) {
				return harnessRank(present[i]) < harnessRank(present[j])
			}
			return present[i] < present[j]
		})
		defs := map[string]bool{}
		for _, h := range present {
			for _, l := range g.byHarness[h] {
				defs[l.Definition] = true
			}
		}
		conflict := false
		if len(present) > 1 {
			if len(defs) > 1 {
				conflict = true
			}
			for d := range defs {
				if d == "" || d == "opaque" {
					conflict = true
				}
			}
		}
		row.Conflict = conflict
		if len(present) > 0 {
			row.SourceHarness = present[0]
			row.SourcePath = g.byHarness[row.SourceHarness][0].Location
		}
		codexGlobal := false
		// A global Codex entry is reported, not written: the project has no
		// Codex config to extend.
		if set["codex"] && !codexCapable {
			for _, it := range items {
				if it.Kind != protocol.InventoryMCP || it.Name != g.server {
					continue
				}
				for _, l := range it.Locations {
					if l.NodeID == g.node && l.Harness == "codex" && l.Scope == protocol.InventoryUser {
						codexGlobal = true
					}
				}
			}
		}
		harnesses := make([]string, 0, len(set))
		for h := range set {
			harnesses = append(harnesses, h)
		}
		sort.Slice(harnesses, func(i, j int) bool {
			if harnessRank(harnesses[i]) != harnessRank(harnesses[j]) {
				return harnessRank(harnesses[i]) < harnessRank(harnesses[j])
			}
			return harnesses[i] < harnesses[j]
		})
		have := map[string]bool{}
		for _, h := range present {
			have[h] = true
		}
		sourceDef := ""
		if row.SourceHarness != "" && len(g.byHarness[row.SourceHarness]) > 0 {
			sourceDef = g.byHarness[row.SourceHarness][0].Definition
		}
		for _, h := range harnesses {
			cell := MCPHarnessCell{Harness: h, State: CoverageMissing}
			if have[h] {
				switch {
				case !conflict:
					cell.State = CoverageOn
				case sourceDef == "" || sourceDef == "opaque":
					cell.State = CoverageConflict
				case len(g.byHarness[h]) > 0 && g.byHarness[h][0].Definition == sourceDef:
					cell.State = CoverageOn
				default:
					cell.State = CoverageConflict
				}
			} else if h == "codex" && !codexCapable {
				if codexGlobal {
					continue
				}
				continue
			} else {
				row.Missing = append(row.Missing, h)
			}
			row.Cells = append(row.Cells, cell)
		}
		if set["codex"] && !codexCapable && codexGlobal {
			row.Note = "codex: global only"
		}
		sort.Strings(row.Missing)
		if conflict {
			row.Action = Action{Kind: ActNone, Name: g.server, Project: g.project, NodeID: g.node, Machine: g.machine}
		} else if len(row.Missing) > 0 {
			row.Action = Action{Kind: ActMCPCoverage, Name: g.server, Project: g.project, NodeID: g.node, Machine: g.machine,
				Harnesses: append([]string(nil), row.Missing...), Source: row.SourceHarness}
		}
		if len(row.Missing) == 0 && !conflict && row.Note == "" {
			continue
		}
		out = append(out, row)
	}
	return out
}

// coverageDiffs turns coverage rows into the lines of the Differences screen.
func coverageDiffs(items []protocol.ToolItem, ms []Machine) []Difference {
	rows := MCPCoverage(items, ms)
	var out []Difference
	for _, r := range rows {
		title := r.Title(len(ms))
		if r.Conflict {
			out = append(out, Difference{
				Key:  "mcp-coverage-conflict:" + r.NodeID + ":" + r.Project + ":" + r.Server,
				Kind: KindMCPCoverageConflict, Severity: SevErr,
				Title:    title,
				Fix:      "compare the declarations; nothing is overwritten",
				Projects: []string{r.Project}, Machines: []string{r.Machine},
				Action: r.Action,
			})
			continue
		}
		if len(r.Missing) == 0 {
			continue
		}
		out = append(out, Difference{
			Key:  "mcp-coverage-gap:" + r.NodeID + ":" + r.Project + ":" + r.Server,
			Kind: KindMCPCoverageGap, Severity: SevWarn,
			Title:    title,
			Fix:      fmt.Sprintf("add it to %s for %s", strings.Join(r.Missing, " and "), r.Project),
			Projects: []string{r.Project}, Machines: []string{r.Machine},
			Action: r.Action,
		})
	}
	return out
}
