package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/protocol"
)

func coverageLoc(node, machine, project, harness, name, definition string) protocol.ToolLocation {
	return protocol.ToolLocation{NodeID: node, Machine: machine, GraphNode: project, InventoryItem: protocol.InventoryItem{
		Harness: harness, Kind: protocol.InventoryMCP, Scope: protocol.InventoryProject, Project: project,
		ProjectRoot: "/src/" + project, Location: "/src/" + project + "/" + harness + "-config",
		Name: name, Definition: definition, State: protocol.InventoryOK,
	}}
}

func coverageItem(name string, locs ...protocol.ToolLocation) protocol.ToolItem {
	return protocol.ToolItem{ID: "mcp:" + name, Kind: protocol.InventoryMCP, Name: name, Hash: "h-" + name, Locations: locs, State: "ok"}
}

func coverageToolsModel(w, h int) (Model, *fakeTools) {
	base := &fakeSource{snap: fakeSnapshot(), events: fakeEvents()}
	base.snap.Nodes = []protocol.NodeView{{NodeID: "n_r", Hostname: "research", Online: true,
		Harnesses: []protocol.HarnessInfo{{Name: "claude"}, {Name: "pi"}, {Name: "opencode"}}}}
	base.snap.Projects = []protocol.ProjectView{{Name: "homelab", NodeID: "n_r", Path: "/src/homelab", Source: protocol.SourceRegistered}}
	items := []protocol.ToolItem{
		coverageItem("conflict-mcp",
			coverageLoc("n_r", "research", "homelab", "claude", "conflict-mcp", "one"),
			coverageLoc("n_r", "research", "homelab", "pi", "conflict-mcp", "two")),
		coverageItem("homelab-mcp", coverageLoc("n_r", "research", "homelab", "claude", "homelab-mcp", "same")),
	}
	src := &fakeTools{fakeSource: base, items: items}
	m := New(src)
	m.loc = time.UTC
	m.now = func() time.Time { return t0 }
	m = send(m, tea.WindowSizeMsg{Width: w, Height: h})
	m = refresh(m, src)
	m = openTools(m)
	return m, src
}

func coverageRowIndex(m Model, prefix string) int {
	for i, d := range m.shownDiffs() {
		if strings.HasPrefix(d.Key, prefix) {
			return i
		}
	}
	return -1
}

func TestToolsMCPCoverageRowsAndPlan(t *testing.T) {
	m, src := coverageToolsModel(120, 34)
	var lines []string
	for _, d := range m.shownDiffs() {
		lines = append(lines, d.Title)
	}
	v := strings.Join(lines, "\n")
	if !strings.Contains(v, "homelab-mcp · homelab · claude ✓ pi ✗ opencode ✗") {
		t.Fatalf("coverage row is missing:\n%s", v)
	}
	if !strings.Contains(v, "conflict-mcp · homelab · claude ✓ pi !") {
		t.Fatalf("conflict row is missing:\n%s", v)
	}
	gap := coverageRowIndex(m, "mcp-coverage-gap:")
	if gap < 0 {
		t.Fatalf("no coverage gap difference: %+v", m.shownDiffs())
	}
	m.tl.row = gap
	m = enterToolsPlan(m)
	v = plain(m)
	for _, want := range []string{"Add homelab-mcp for homelab", "writes these files", ".pi/mcp.json", "opencode.json", "environment values stay references"} {
		if !strings.Contains(v, want) {
			t.Fatalf("plan preview is missing %q:\n%s", want, v)
		}
	}
	if len(src.coveragePlans) != 1 || strings.Join(src.coveragePlans[0].Harnesses, ",") != "opencode,pi" {
		t.Fatalf("plan request = %+v", src.coveragePlans)
	}
	m = enterToolsPlan(m)
	if len(src.coverageApplied) != 1 {
		t.Fatalf("plan was not applied: %+v", src.coverageApplied)
	}
	if !strings.Contains(plain(m), "homelab-mcp is now in") {
		t.Fatalf("no applied result:\n%s", plain(m))
	}
	m = pressKey(m, "u")
	if len(src.coverageUndone) != 1 {
		t.Fatalf("undo was not called: %+v", src.coverageUndone)
	}
}

func TestToolsMCPCoverageGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		m, _ := coverageToolsModel(size[0], size[1])
		golden(t, fmt.Sprintf("tools_mcp_coverage_%dx%d", size[0], size[1]), m.testView())

		gap := coverageRowIndex(m, "mcp-coverage-gap:")
		if gap < 0 {
			t.Fatal("no coverage gap difference")
		}
		m.tl.row = gap
		m = enterToolsPlan(m)
		golden(t, fmt.Sprintf("tools_mcp_coverage_plan_%dx%d", size[0], size[1]), m.testView())
	}
}

func TestToolsMCPCoverageConflictHasNoAutomaticFix(t *testing.T) {
	m, src := coverageToolsModel(120, 34)
	row := coverageRowIndex(m, "mcp-coverage-conflict:")
	if row < 0 {
		t.Fatal("no conflict difference")
	}
	m.tl.row = row
	m = enterToolsPlan(m)
	if len(src.coveragePlans) != 0 {
		t.Fatalf("a conflict opened a write plan: %+v", src.coveragePlans)
	}
	if !strings.Contains(plain(m), "compare the declarations") {
		t.Fatalf("conflict fix is not explained:\n%s", plain(m))
	}
}
