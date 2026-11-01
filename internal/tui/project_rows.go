package tui

import (
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// projectRows lists the known projects once per name and machine.
func (m Model) projectRows() []protocol.ProjectView {
	seen := map[string]bool{}
	var out []protocol.ProjectView
	for _, p := range m.snap.Projects {
		if p.Hostname == "" { // the machine list has it too
			for _, n := range m.snap.Nodes {
				if n.NodeID == p.NodeID {
					p.Hostname = n.Hostname
				}
			}
		}
		k := strings.ToLower(p.Name) + "\x00" + p.NodeID
		if !seen[k] {
			seen[k] = true
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name); a != b {
			return a < b
		}
		return out[i].Hostname < out[j].Hostname
	})
	return out
}

// resolveProject splits "name" or "name@machine" and finds the machine that
// holds the project, when there is exactly one.
func (m Model) resolveProject(text string) (name, machine string, node *protocol.NodeView) {
	name = strings.TrimSpace(text)
	if i := strings.LastIndex(name, "@"); i > 0 {
		machine, name = strings.TrimSpace(name[i+1:]), strings.TrimSpace(name[:i])
	}
	var hit *protocol.ProjectView
	n := 0
	for _, p := range m.projectRows() {
		if strings.EqualFold(p.Name, name) && (machine == "" || strings.EqualFold(p.Hostname, machine) || p.NodeID == machine) {
			hit = &p
			n++
		}
	}
	if n != 1 {
		return name, machine, nil
	}
	for i := range m.snap.Nodes {
		if m.snap.Nodes[i].NodeID == hit.NodeID {
			return name, hit.Hostname, &m.snap.Nodes[i]
		}
	}
	return name, hit.Hostname, nil
}

// candidate is one completion choice with a dim note.
type candidate struct {
	value string
	note  string
}

// projectCandidates lists projects that match text, for completion in the
// new-session form. A name on two machines is offered as name@machine.
func (m Model) projectCandidates(text string, exact bool) []candidate {
	rows := m.projectRows()
	machines := map[string]int{}
	for _, p := range rows {
		machines[strings.ToLower(p.Name)]++
	}
	text = strings.ToLower(strings.TrimSpace(text))
	var out []candidate
	for _, p := range rows {
		name := p.Name
		value := name
		if machines[strings.ToLower(name)] > 1 {
			value = name + "@" + p.Hostname
		}
		hay := strings.ToLower(value)
		if exact && strings.ToLower(name) != text && hay != text {
			continue
		}
		if !exact && !strings.Contains(hay, text) {
			continue
		}
		src := p.Source
		if src == "" {
			src = protocol.SourceSeen
		}
		out = append(out, candidate{value: value, note: src + " on " + p.Hostname})
	}
	return out
}
