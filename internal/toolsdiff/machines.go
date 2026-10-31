package toolsdiff

import (
	"sort"

	"github.com/niparis/agent-net/internal/protocol"
)

// named returns items whose locations carry the machine's display name, so a
// renamed machine has one name everywhere. The input is not changed.
func named(items []protocol.ToolItem, ms []Machine) []protocol.ToolItem {
	out := make([]protocol.ToolItem, len(items))
	for i, it := range items {
		out[i] = it
		out[i].Locations = append([]protocol.ToolLocation(nil), it.Locations...)
		for j, l := range out[i].Locations {
			if k := machineIndex(ms, l.NodeID, ""); k >= 0 {
				out[i].Locations[j].Machine = ms[k].Name
			}
		}
	}
	return out
}

// MachinesOf turns the nodes of a snapshot into columns, by name.
func MachinesOf(nodes []protocol.NodeView) []Machine {
	var out []Machine
	for _, n := range nodes {
		name := n.Hostname
		if name == "" {
			name = n.NodeID
		}
		out = append(out, Machine{ID: n.NodeID, Name: name, Online: n.Online})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
