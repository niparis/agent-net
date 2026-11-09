package gateway

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// observePromptFile writes a command or agent file, reports it in the node's
// inventory next to a skill of the same name, and returns the item id and the
// bundle the node would send.
func observePromptFile(t *testing.T, g *Gateway, node, kind, name, body, location string) (string, []byte) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, name+".md")
	if err := os.WriteFile(file, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	bundle, err := inventory.PackFile(file)
	if err != nil {
		t.Fatal(err)
	}
	unpacked := t.TempDir()
	if err = inventory.UnpackSkill(bundle, unpacked); err != nil {
		t.Fatal(err)
	}
	hash, err := inventory.SkillHash(unpacked)
	if err != nil {
		t.Fatal(err)
	}
	items := []protocol.InventoryItem{
		{Harness: "claude", Kind: kind, Name: name, Hash: hash, State: "ok", Scope: "user", Location: location, Description: "does " + name},
		{Harness: "claude", Kind: "skill", Name: name, Hash: "skillhash", State: "ok", Scope: "user", Location: "/skills/" + name},
	}
	if err = g.store.SetInventory(node, protocol.InventoryReport{State: "ok", ScannedAt: time.Now(), Items: items}); err != nil {
		t.Fatal(err)
	}
	return `["` + kind + `","` + name + `","` + hash + `"]`, bundle
}

func TestImportCommandAndAgentFilesGetNumberedVersions(t *testing.T) {
	for _, kind := range []string{protocol.InventoryPrompt, protocol.InventoryAgent} {
		t.Run(kind, func(t *testing.T) {
			g, n, _ := toggleFixture(t)
			location := "/home/u/.claude/" + map[string]string{"prompt": "commands", "agent": "agents"}[kind] + "/review.md"
			id1, bundle1 := observePromptFile(t, g, n, kind, "review", "Review the diff.\n", location)
			toggleFakeNode(t, g, n, bundle1, nil)
			v1, err := g.importSkill(t.Context(), protocol.ToolImport{Item: id1, Location: protocol.ToolImportLocation{NodeID: n, Location: location}})
			if err != nil {
				t.Fatal(err)
			}
			if v1.Number != 1 {
				t.Fatalf("first version number %d, want 1", v1.Number)
			}
			// Importing the same content again returns the same version.
			again, err := g.importSkill(t.Context(), protocol.ToolImport{Item: id1, Location: protocol.ToolImportLocation{NodeID: n, Location: location}})
			if err != nil || again.Number != 1 || again.Commit != v1.Commit {
				t.Fatalf("reimport: %+v %v", again, err)
			}
			// An edited file is a new content hash and becomes v2.
			id2, bundle2 := observePromptFile(t, g, n, kind, "review", "Review the diff twice.\n", location)
			toggleFakeNode(t, g, n, bundle2, nil)
			v2, err := g.importSkill(t.Context(), protocol.ToolImport{Item: id2, Location: protocol.ToolImportLocation{NodeID: n, Location: location}})
			if err != nil {
				t.Fatal(err)
			}
			if v2.Number != 2 || id1 == id2 {
				t.Fatalf("second version number %d (ids %q %q), want 2", v2.Number, id1, id2)
			}
			// The store keeps both and exports the bundle of the first.
			exported, err := g.exportSkill(id1, v1.Hash)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if err = inventory.UnpackSkill(exported.Data, dir); err != nil {
				t.Fatal(err)
			}
			if b, err := os.ReadFile(filepath.Join(dir, "review.md")); err != nil || string(b) != "Review the diff.\n" {
				t.Fatalf("exported file %q err %v", b, err)
			}
			// A skill with the same name has its own numbering and is not touched.
			var skillVersions int
			if err = g.store.db.QueryRow(`SELECT COUNT(*) FROM skill_versions v JOIN tool_identities i ON i.id=v.item WHERE i.kind='skill'`).Scan(&skillVersions); err != nil || skillVersions != 0 {
				t.Fatalf("skill versions %d err %v", skillVersions, err)
			}
			items, err := g.store.ToolsItems(protocol.ToolsFilter{Kind: kind}, func(string) bool { return true })
			if err != nil {
				t.Fatal(err)
			}
			numbers := map[string]int{}
			for _, it := range items {
				numbers[it.ID] = it.VersionNo
				if it.Kind != kind || it.Name != "review" {
					t.Fatalf("unexpected item %+v", it)
				}
			}
			if numbers[id1] != 1 || numbers[id2] != 2 {
				t.Fatalf("item version numbers %v", numbers)
			}
			// The newest observation supplies description and token estimates.
			for _, it := range items {
				if it.ID == id2 && (it.Description != "does review" || it.StartTokens == 0) {
					t.Fatalf("missing description or tokens: %+v", it)
				}
			}
		})
	}
}

func TestReadStoredCommandText(t *testing.T) {
	g, n, _ := toggleFixture(t)
	location := "/home/u/.claude/commands/review.md"
	id, bundle := observePromptFile(t, g, n, protocol.InventoryPrompt, "review", "Review the diff.\n", location)
	toggleFakeNode(t, g, n, bundle, nil)
	if _, err := g.importSkill(t.Context(), protocol.ToolImport{Item: id, Location: protocol.ToolImportLocation{NodeID: n, Location: location}}); err != nil {
		t.Fatal(err)
	}
	got, err := g.readSkillText(t.Context(), protocol.SkillTextRequest{Item: id, Path: "review.md"})
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := got.(map[string]string); !ok || m["text"] != "Review the diff.\n" {
		t.Fatalf("stored text %#v", got)
	}
}

// Switching a global command off in one project imports it on demand, plans a
// copy of the file into every other project and a removal of the user copy. A
// skill with the same name is a different item and is not part of the plan.
func TestToggleCommandPlansFileCopiesPerProject(t *testing.T) {
	for _, kind := range []string{protocol.InventoryPrompt, protocol.InventoryAgent} {
		t.Run(kind, func(t *testing.T) {
			g, n, _ := toggleFixture(t)
			folder := map[string]string{"prompt": "commands", "agent": "agents"}[kind]
			location := "/home/u/.claude/" + folder + "/review.md"
			id, bundle := observePromptFile(t, g, n, kind, "review", "Review the diff.\n", location)
			var probes []protocol.ToolPlanProbe
			toggleFakeNodeProbe(t, g, n, bundle, nil, func(p protocol.ToolPlanProbe) protocol.ToolPlanProbeResult {
				probes = append(probes, p)
				if p.Source != "" {
					return protocol.ToolPlanProbeResult{}
				}
				return protocol.ToolPlanProbeResult{Destination: filepath.Join(p.Checkout, p.Root, protocol.BundleEntryName(p.Kind, p.Name)), Fingerprint: "fixture"}
			})
			plan, err := g.toggleTool(t.Context(), protocol.ToolToggleRequest{Item: id, Project: "one", On: false}, "owner")
			if err != nil {
				t.Fatal(err)
			}
			if plan.Assignment.Scope != "projects" || len(plan.Assignment.Projects) != 1 || plan.Assignment.Projects[0] != "two" {
				t.Fatalf("assignment %+v", plan.Assignment)
			}
			var copyTarget, removeTarget *protocol.ToolPlanTarget
			for i := range plan.Targets {
				switch plan.Targets[i].Action {
				case "copy":
					copyTarget = &plan.Targets[i]
				case "remove":
					removeTarget = &plan.Targets[i]
				}
			}
			if copyTarget == nil || copyTarget.Project != "two" || copyTarget.Harness != "claude" || copyTarget.Root != ".claude/"+folder ||
				copyTarget.Destination != "/repo/two/.claude/"+folder+"/review.md" || copyTarget.Conflict != "" {
				t.Fatalf("copy target %+v in %+v", copyTarget, plan.Targets)
			}
			if removeTarget == nil || removeTarget.Source != location || removeTarget.Conflict != "" {
				t.Fatalf("remove target %+v in %+v", removeTarget, plan.Targets)
			}
			// Only the claude folder is probed, and every copy probe says it is a file kind.
			for _, p := range probes {
				if p.Source == "" && (p.Kind != kind || p.Root != ".claude/"+folder || p.Harness != "claude") {
					t.Fatalf("probe %+v", p)
				}
			}
			var versions int
			if err = g.store.db.QueryRow(`SELECT COUNT(*) FROM skill_versions WHERE item=?`, id).Scan(&versions); err != nil || versions != 1 {
				t.Fatalf("versions %d %v", versions, err)
			}
			// The skill that has the same name was neither imported nor planned.
			for _, target := range plan.Targets {
				if target.Source == "/skills/review" {
					t.Fatalf("skill is in the plan: %+v", target)
				}
			}
		})
	}
}
