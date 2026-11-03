package tui

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/toolsdiff"
)

func TestRepoRemovalCopiesOnlyIncludeTrackedProjectLocations(t *testing.T) {
	tracked := protocol.ToolLocation{NodeID: "node-a", InventoryItem: protocol.InventoryItem{ProjectRoot: "/repo", Scope: protocol.InventoryProject, Project: "demo", Tracked: true}}
	local := protocol.ToolLocation{NodeID: "node-a", InventoryItem: protocol.InventoryItem{ProjectRoot: "/repo", Scope: protocol.InventoryProject, Project: "demo", Tracked: false}}
	row := toolsdiff.Row{Repo: "demo", Variants: []protocol.ToolItem{{ID: "skill-id", Name: "skill", Locations: []protocol.ToolLocation{local, tracked}}}}

	copies := repoCopies(row)
	if len(copies) != 1 || copies[0].Checkout != "/repo" {
		t.Fatalf("repo removal copies = %+v, want only the tracked checkout", copies)
	}
}
