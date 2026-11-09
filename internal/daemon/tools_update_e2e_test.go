package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

func writeHomeSkill(t *testing.T, home, name, text string) string {
	t.Helper()
	dir := filepath.Join(home, ".claude/skills", name)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// skillItemWhere polls tools_items until a skill item with this name and
// content text is listed, and returns it.
func skillItemWhere(t *testing.T, cli *localClient, name string, ok func(protocol.ToolItem) bool) protocol.ToolItem {
	t.Helper()
	var found protocol.ToolItem
	eventually(t, "skill item "+name, func() bool {
		raw, err := cli.call("tools_items", map[string]any{"kind": "skill"})
		if err != nil {
			return false
		}
		var p protocol.ToolsPage
		if json.Unmarshal(raw, &p) != nil {
			return false
		}
		for _, v := range p.Items {
			if v.Name == name && ok(v) {
				found = v
				return true
			}
		}
		return false
	})
	return found
}

func importLoc(item protocol.ToolItem, loc protocol.ToolLocation) map[string]any {
	return map[string]any{"item": item.ID, "location": map[string]string{"node_id": loc.NodeID, "location": loc.Location}}
}

// A skill is deployed to machine A, edited there, saved as v2, and machine B
// (which still has v1) is updated. A changed copy is never overwritten.
func TestChangedCopyIsSavedAndOtherCopiesUpdated(t *testing.T) {
	t.Parallel()
	homeA, homeB := t.TempDir(), t.TempDir()
	dirA := writeHomeSkill(t, homeA, "demo", "version one\n")
	dirB := writeHomeSkill(t, homeB, "demo", "version one\n")
	v1hash, err := inventory.SkillHash(dirA)
	if err != nil {
		t.Fatal(err)
	}
	g := startGateway(t, t.TempDir(), "")
	// Machine A's daemon deployed v1: record it, then edit the copy.
	stateA := t.TempDir()
	manifest, _ := json.Marshal([]skillManifestEntry{{Item: "deployed", Path: dirA, Hash: v1hash, Revision: 1, Owned: true}})
	if err := os.WriteFile(filepath.Join(stateA, "skill-manifest.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirA, "SKILL.md"), []byte("version one, improved\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.InventoryHome = homeA; c.StateDir = stateA }})
	b := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.InventoryHome = homeB }})
	cli := cliClient(t, a)

	// The owner sees A's copy as changed here, from the content it deployed.
	edited := skillItemWhere(t, cli, "demo", func(it protocol.ToolItem) bool {
		return it.Hash != v1hash && len(it.Locations) > 0 && it.Locations[0].ChangedHere
	})
	changedLoc := edited.Locations[0]
	if changedLoc.DeployedHash != v1hash || changedLoc.Location != dirA {
		t.Fatalf("changed copy: %+v", changedLoc)
	}
	// B's copy is plain v1 content, not marked.
	v1 := skillItemWhere(t, cli, "demo", func(it protocol.ToolItem) bool { return it.Hash == v1hash })
	if v1.Locations[0].ChangedHere || v1.Locations[0].NodeID == changedLoc.NodeID {
		t.Fatalf("unedited copy marked or on the wrong machine: %+v", v1.Locations)
	}
	// Saving an unedited copy is refused: there is nothing new in it.
	if _, err := cli.call("tools_save_changed", importLoc(v1, v1.Locations[0])); err == nil || !strings.Contains(err.Error(), "not changed here") {
		t.Fatalf("saved an unedited copy: %v", err)
	}
	// Store v1 from B so that a changed copy has a version to come from.
	cli.mustCall("tools_import", importLoc(v1, v1.Locations[0]))
	edited = skillItemWhere(t, cli, "demo", func(it protocol.ToolItem) bool {
		return it.Hash != v1hash && len(it.Locations) > 0 && it.Locations[0].DeployedNo == 1
	})

	// Before it is saved, the changed copy is kept as it is, in any update.
	var plan protocol.ToolUpdatePlan
	if err := json.Unmarshal(cli.mustCall("tools_update_plan", map[string]any{"item": v1.ID}), &plan); err != nil {
		t.Fatal(err)
	}
	kept := false
	for _, c := range plan.Copies {
		if c.Location == dirA && c.State == protocol.UpdateKept && strings.Contains(c.Reason, "changed here") {
			kept = true
		}
	}
	if !kept {
		t.Fatalf("changed copy not kept: %+v", plan.Copies)
	}
	if _, err := cli.call("tools_update_copies", map[string]any{"item": v1.ID, "copies": []map[string]string{{"node_id": changedLoc.NodeID, "location": dirA}}}); err == nil {
		t.Fatal("update accepted a changed copy")
	}
	if got, _ := os.ReadFile(filepath.Join(dirA, "SKILL.md")); string(got) != "version one, improved\n" {
		t.Fatalf("changed copy was overwritten: %q", got)
	}

	// Save it: it becomes version 2, provenance points at machine A.
	cli.mustCall("tools_save_changed", importLoc(edited, edited.Locations[0]))
	var versions struct {
		Versions []struct {
			Number   int    `json:"number"`
			NodeID   string `json:"node_id"`
			Location string `json:"location"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(cli.mustCall("tools_versions", map[string]any{"item": edited.ID}), &versions); err != nil || len(versions.Versions) != 1 || versions.Versions[0].Number != 2 || versions.Versions[0].Location != dirA {
		t.Fatalf("saved version: %+v %v", versions, err)
	}

	// The plan now replaces B's v1 copy with v2 and offers nothing for A.
	plan = protocol.ToolUpdatePlan{}
	if err := json.Unmarshal(cli.mustCall("tools_update_plan", map[string]any{"item": edited.ID}), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.No != 2 || len(plan.Copies) != 1 || plan.Copies[0].Location != dirB || plan.Copies[0].State != protocol.UpdateWillReplace || plan.Copies[0].FromNo != 1 {
		t.Fatalf("plan: %+v", plan)
	}
	if got, _ := os.ReadFile(filepath.Join(dirB, "SKILL.md")); string(got) != "version one\n" {
		t.Fatalf("planning changed B: %q", got)
	}
	// A copy that is not in the plan is refused, and nothing is changed.
	if _, err := cli.call("tools_update_copies", map[string]any{"item": edited.ID, "copies": []map[string]string{{"node_id": plan.Copies[0].NodeID, "location": filepath.Join(homeB, "elsewhere")}}}); err == nil {
		t.Fatal("update accepted a copy outside the plan")
	}
	var done protocol.ToolUpdatePlan
	if err := json.Unmarshal(cli.mustCall("tools_update_copies", map[string]any{"item": edited.ID, "copies": []map[string]string{{"node_id": plan.Copies[0].NodeID, "location": dirB}}}), &done); err != nil {
		t.Fatal(err)
	}
	if len(done.Copies) != 1 || done.Copies[0].State != protocol.UpdateApplied {
		t.Fatalf("update result: %+v", done)
	}
	if got, _ := os.ReadFile(filepath.Join(dirB, "SKILL.md")); string(got) != "version one, improved\n" {
		t.Fatalf("B not updated: %q", got)
	}
	if backups, _ := filepath.Glob(filepath.Join(b.cfg.StateDir, "skill-backups", "*", "*", "*")); len(backups) == 0 {
		t.Fatal("B's old copy was not backed up")
	}
}
