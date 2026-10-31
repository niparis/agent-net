package daemon

import (
	"context"
	"encoding/json"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolsdiff"
)

// maxCTODifferences keeps the answer small for a large network. The rest is
// counted, not listed.
const maxCTODifferences = 60

type ctoDifference struct {
	toolsdiff.Difference
	Accepted bool `json:"accepted,omitempty"`
}

// allToolItems reads every page of tools_items.
func (d *Daemon) allToolItems(ctx context.Context) ([]protocol.ToolItem, error) {
	var items []protocol.ToolItem
	offset := 0
	for {
		data, err := d.call(ctx, "query.tools_items", protocol.ToolsFilter{Offset: offset})
		if err != nil {
			return nil, err
		}
		var page protocol.ToolsPage
		if err = json.Unmarshal(data, &page); err != nil {
			return nil, err
		}
		items = append(items, page.Items...)
		if page.Next <= offset {
			return items, nil
		}
		offset = page.Next
	}
}

// toolDifferences is the CTO read tool tools_differences: the same differences
// the Tools tab shows on its first screen, each with one suggested fix. It
// changes nothing.
func (d *Daemon) toolDifferences(ctx context.Context) (any, error) {
	items, err := d.allToolItems(ctx)
	if err != nil {
		return nil, err
	}
	var machines []toolsdiff.Machine
	if data, err := d.call(ctx, "query.nodes", nil); err == nil {
		var nodes protocol.NodesInfo
		if json.Unmarshal(data, &nodes) == nil {
			machines = toolsdiff.MachinesOf(nodes.Nodes)
		}
	}
	accepted := map[string]bool{}
	if data, err := d.call(ctx, "query.tools_accepted", struct{}{}); err == nil {
		var keys []string
		if json.Unmarshal(data, &keys) == nil {
			for _, k := range keys {
				accepted[k] = true
			}
		}
	}
	diffs := toolsdiff.Differences(items, machines)
	out := make([]ctoDifference, 0, len(diffs))
	for _, df := range diffs {
		if len(out) == maxCTODifferences {
			break
		}
		out = append(out, ctoDifference{Difference: df, Accepted: accepted[df.Key]})
	}
	return map[string]any{
		"differences": out,
		"to_look_at":  toolsdiff.Counts(diffs, accepted),
		"not_listed":  len(diffs) - len(out),
		"note":        "Each difference has one suggested fix. Show the plan with tools_plan or mcp_import and wait for the owner before any apply.",
	}, nil
}
