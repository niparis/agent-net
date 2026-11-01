package toolsdiff

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func mcpProjectLoc(node, machine, project, harness, name, definition string) protocol.ToolLocation {
	return protocol.ToolLocation{NodeID: node, Machine: machine, GraphNode: project, InventoryItem: protocol.InventoryItem{
		Harness: harness, Kind: protocol.InventoryMCP, Scope: protocol.InventoryProject, Project: project,
		ProjectRoot: "/src/" + project, Location: "/src/" + project + "/" + harness + "-config",
		Name: name, Definition: definition, State: protocol.InventoryOK,
	}}
}

func mcpCoverageItem(name string, locs ...protocol.ToolLocation) protocol.ToolItem {
	return protocol.ToolItem{ID: "mcp:" + name, Kind: protocol.InventoryMCP, Name: name, Hash: "h-" + name, Locations: locs, State: "ok"}
}

func researchWith(harnesses ...string) []Machine {
	return []Machine{{ID: "n_r", Name: "research", Online: true, Harnesses: harnesses}}
}

func findCoverage(rows []MCPCoverageRow, server, project string) *MCPCoverageRow {
	for i := range rows {
		if rows[i].Server == server && rows[i].Project == project {
			return &rows[i]
		}
	}
	return nil
}

func TestCoverageFindsGapWithinProject(t *testing.T) {
	items := []protocol.ToolItem{mcpCoverageItem("homelab-mcp", mcpProjectLoc("n_r", "research", "homelab", "claude", "homelab-mcp", "same"))}
	rows := MCPCoverage(items, researchWith("claude", "pi", "opencode"))
	row := findCoverage(rows, "homelab-mcp", "homelab")
	if row == nil {
		t.Fatalf("no coverage row: %+v", rows)
	}
	if row.Conflict {
		t.Fatalf("a single declaration must not be a conflict: %+v", row)
	}
	if strings.Join(row.Missing, ",") != "opencode,pi" {
		t.Fatalf("missing harnesses = %v", row.Missing)
	}
	v := row.Title(2)
	for _, want := range []string{"homelab-mcp", "homelab", "claude ✓", "pi ✗", "opencode ✗"} {
		if !strings.Contains(v, want) {
			t.Fatalf("title %q is missing %q", v, want)
		}
	}
	if row.Action.Kind != ActMCPCoverage || row.Action.Project != "homelab" || row.Action.NodeID != "n_r" {
		t.Fatalf("action = %+v", row.Action)
	}
}

func TestCoverageFindsConflict(t *testing.T) {
	items := []protocol.ToolItem{mcpCoverageItem("homelab-mcp",
		mcpProjectLoc("n_r", "research", "homelab", "claude", "homelab-mcp", "one"),
		mcpProjectLoc("n_r", "research", "homelab", "pi", "homelab-mcp", "two"))}
	rows := MCPCoverage(items, researchWith("claude", "pi", "opencode"))
	row := findCoverage(rows, "homelab-mcp", "homelab")
	if row == nil || !row.Conflict {
		t.Fatalf("conflict not found: %+v", rows)
	}
	if row.Action.Kind != ActNone {
		t.Fatalf("a conflict row must not offer an automatic write: %+v", row.Action)
	}
	v := row.Title(1)
	if !strings.Contains(v, "claude ✓") || !strings.Contains(v, "pi !") || !strings.Contains(v, "opencode ✗") {
		t.Fatalf("conflict title = %q", v)
	}
}

func TestCoverageNoFalseGapWhenEveryHarnessHasIt(t *testing.T) {
	items := []protocol.ToolItem{mcpCoverageItem("homelab-mcp",
		mcpProjectLoc("n_r", "research", "homelab", "claude", "homelab-mcp", "same"),
		mcpProjectLoc("n_r", "research", "homelab", "pi", "homelab-mcp", "same"),
		mcpProjectLoc("n_r", "research", "homelab", "opencode", "homelab-mcp", "same"))}
	rows := MCPCoverage(items, researchWith("claude", "pi", "opencode"))
	if row := findCoverage(rows, "homelab-mcp", "homelab"); row != nil {
		t.Fatalf("coverage row for a fully covered server: %+v", row)
	}
}

func TestCoverageNeverMixesProjects(t *testing.T) {
	items := []protocol.ToolItem{mcpCoverageItem("homelab-mcp",
		mcpProjectLoc("n_r", "research", "homelab", "claude", "homelab-mcp", "same"),
		mcpProjectLoc("n_r", "research", "homelab-portal", "pi", "homelab-mcp", "other"))}
	rows := MCPCoverage(items, researchWith("claude", "pi"))
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if row := findCoverage(rows, "homelab-mcp", "homelab"); row == nil || row.Conflict {
		t.Fatalf("homelab row = %+v", row)
	}
	if row := findCoverage(rows, "homelab-mcp", "homelab-portal"); row == nil || row.Conflict || len(row.Missing) != 1 || row.Missing[0] != "claude" {
		t.Fatalf("homelab-portal row = %+v", row)
	}
}

func TestCoverageShowsCodexGlobalOnlyWhenItHasNoProjectConfig(t *testing.T) {
	global := protocol.ToolLocation{NodeID: "n_r", Machine: "research", InventoryItem: protocol.InventoryItem{
		Harness: "codex", Kind: protocol.InventoryMCP, Scope: protocol.InventoryUser, Name: "homelab-mcp", Definition: "same"}}
	items := []protocol.ToolItem{mcpCoverageItem("homelab-mcp",
		mcpProjectLoc("n_r", "research", "homelab", "claude", "homelab-mcp", "same"), global)}
	rows := MCPCoverage(items, researchWith("claude", "codex"))
	row := findCoverage(rows, "homelab-mcp", "homelab")
	if row == nil {
		t.Fatal("no coverage row")
	}
	if row.Note != "codex: global only" {
		t.Fatalf("note = %q", row.Note)
	}
	if len(row.Missing) != 0 {
		t.Fatalf("codex without project config must not be a write target: %v", row.Missing)
	}
}

func TestCoverageUsesCodexProjectConfigWhenItExists(t *testing.T) {
	items := []protocol.ToolItem{
		mcpCoverageItem("homelab-mcp", mcpProjectLoc("n_r", "research", "homelab", "claude", "homelab-mcp", "same")),
		mcpCoverageItem("codex-other", mcpProjectLoc("n_r", "research", "homelab", "codex", "codex-other", "other")),
	}
	rows := MCPCoverage(items, researchWith("claude", "codex"))
	row := findCoverage(rows, "homelab-mcp", "homelab")
	if row == nil {
		t.Fatal("no coverage row")
	}
	if row.Note != "" {
		t.Fatalf("codex has a project config, so there is no global-only note: %q", row.Note)
	}
	if strings.Join(row.Missing, ",") != "codex" {
		t.Fatalf("codex must be a write target: %v", row.Missing)
	}
}

func TestCoverageAppearsAsDifferences(t *testing.T) {
	items := []protocol.ToolItem{mcpCoverageItem("homelab-mcp", mcpProjectLoc("n_r", "research", "homelab", "claude", "homelab-mcp", "same"))}
	ds := Differences(items, researchWith("claude", "pi"))
	var gap, conflict *Difference
	for i := range ds {
		switch ds[i].Kind {
		case KindMCPCoverageGap:
			gap = &ds[i]
		case KindMCPCoverageConflict:
			conflict = &ds[i]
		}
	}
	if gap == nil {
		t.Fatalf("no coverage difference: %+v", ds)
	}
	if conflict != nil {
		t.Fatalf("spurious coverage conflict: %+v", conflict)
	}

	items = append(items, mcpCoverageItem("homelab-mcp", mcpProjectLoc("n_r", "research", "homelab", "pi", "homelab-mcp", "other")))
	ds = Differences(items, researchWith("claude", "pi"))
	gap, conflict = nil, nil
	for i := range ds {
		switch ds[i].Kind {
		case KindMCPCoverageGap:
			gap = &ds[i]
		case KindMCPCoverageConflict:
			conflict = &ds[i]
		}
	}
	if conflict == nil {
		t.Fatalf("no coverage conflict: %+v", ds)
	}
	if gap != nil {
		t.Fatalf("a conflict must not also be a gap: %+v", gap)
	}
}
