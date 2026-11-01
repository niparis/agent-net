package tui

import (
	"strings"
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

func TestToolsShowRepoOnlyForTrackedCopiesAndLocalForOthers(t *testing.T) {
	m, _ := toolsAtScreen(120, 34, 1)
	m.tl.search = "" // show the whole list
	var repo, local *toolsdiff.Row
	for _, r := range m.tl.cat.Rows {
		r := r
		switch {
		case r.Name == "deploy-test-server":
			repo = &r
		case r.Name == "scratch-notes":
			local = &r
		}
	}
	if repo == nil || repo.Origin != toolsdiff.OriginRepo || repo.From != "repo sovl-clone" {
		t.Fatalf("committed copy: %+v", repo)
	}
	if local == nil || local.Origin != toolsdiff.OriginLocal || local.From != "local homelab" {
		t.Fatalf("uncommitted copy: %+v", local)
	}
	if !strings.Contains(m.toolStatus(*repo), "in the sovl-clone repo") || !strings.Contains(m.toolStatus(*local), "not managed") {
		t.Fatal("plain origin words missing")
	}

}

func TestToolsLocalCopyIsManagedLikeAUserCopy(t *testing.T) {
	m, src := toolsAtScreen(120, 34, 1)
	m = rowByNameFrom(m, "scratch-notes", "local")
	m = resolveTools(m.openRowPlan(true))
	if m.tl.modal == nil {
		t.Fatalf("a must open a plan: %q", m.tl.toast)
	}
	if v := plain(m); !strings.Contains(v, "agent-net store") || strings.Contains(v, "branch") {
		t.Errorf("a local copy goes into the store; there is no branch plan:\n%s", v)
	}
	m = pressKey(m, "enter")
	if len(src.imports) != 1 || len(src.plans) != 1 || len(src.applied) != 1 {
		t.Fatalf("imports %v plans %v applied %v", src.imports, src.plans, src.applied)
	}
	if src.imports[0].Location.NodeID != toolNodeR || !strings.Contains(src.imports[0].Location.Location, "scratch-notes") {
		t.Errorf("import %+v", src.imports[0])
	}
	if len(src.sent) != 0 {
		t.Errorf("a local copy must not ask an agent: %v", src.sent)
	}
}

func TestToolsLocalCopyTurnOffUsesTheAssignmentPlan(t *testing.T) {
	m, src := toolsAtScreen(120, 34, 1)
	m = rowByNameFrom(m, "scratch-notes", "local")
	// The first machine column is Nicolas-MBP; the copy lives on research.
	m.tl.col++
	m = resolveTools(m.openRowPlan(false))
	if m.tl.modal == nil {
		t.Fatalf("u must open a plan: %q", m.tl.toast)
	}
	if v := plain(m); strings.Contains(v, "branch") {
		t.Errorf("no branch plan for a local copy:\n%s", v)
	}
	m = pressKey(m, "enter")
	if len(src.plans) != 1 || src.plans[0].Target.Scope != "uninstalled" || len(src.sent) != 0 {
		t.Fatalf("plans %+v sent %v", src.plans, src.sent)
	}
}
