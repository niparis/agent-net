package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestRepoRemovalRejectsUntrackedProjectCopy(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	node, _, err := g.store.CreateNode("fixture", "linux")
	if err != nil {
		t.Fatal(err)
	}
	obs := protocol.InventoryItem{Kind: protocol.InventorySkill, Name: "demo", Hash: "abc", State: protocol.InventoryOK, Scope: protocol.InventoryProject, Project: "demo", ProjectRoot: "/repo", Location: "/repo/.claude/skills/demo", Tracked: false}
	if err = g.store.SetInventory(node, protocol.InventoryReport{State: protocol.InventoryOK, Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	if _, err = g.store.db.Exec(`INSERT INTO projects(node_id,path,name,added_at,checked_at) VALUES(?,?,?,0,0)`, node, "/repo", "demo"); err != nil {
		t.Fatal(err)
	}
	req := protocol.ToolPlanRequest{Item: `["skill","demo","abc"]`, Target: protocol.ToolAssignment{Scope: "projects", Projects: []string{"demo"}}, Operation: "repo_remove", Project: "demo", RepoTargets: []protocol.ToolRepoTarget{{NodeID: node, Checkout: "/repo"}}}
	if _, err = g.repoRemovalTargets(context.Background(), req); err == nil || !strings.Contains(err.Error(), "tracked") {
		t.Fatalf("repo removal accepted an untracked project copy: %v", err)
	}
	obs.Tracked = true
	if err = g.store.SetInventory(node, protocol.InventoryReport{State: protocol.InventoryOK, Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	targets, err := g.repoRemovalTargets(context.Background(), req)
	if err != nil || len(targets) != 1 || targets[0].Action != "repo_remove" {
		t.Fatalf("repo removal rejected a tracked project copy: %+v %v", targets, err)
	}
}

func TestToolAssignmentRevisions(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	item := protocol.InventoryItem{Kind: "skill", Name: "sample", Hash: "abc", State: "ok"}
	if err = s.SetInventory("machine", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{item}}); err != nil {
		t.Fatal(err)
	}
	id := `["skill","sample","abc"]`
	if _, err = s.db.Exec(`INSERT INTO projects(node_id,path,name,added_at,checked_at) VALUES('machine','/repo','demo',0,0)`); err != nil {
		t.Fatal(err)
	}
	a, err := s.AssignTool(id, protocol.ToolAssignment{Scope: "projects", Projects: []string{"demo"}}, "owner", "test")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.AssignTool(id, protocol.ToolAssignment{Scope: "uninstalled"}, "cto", "test")
	if err != nil {
		t.Fatal(err)
	}
	if a.Revision != 1 || b.Revision != 2 || b.Actor != "cto" || b.Assignment.Scope != "uninstalled" {
		t.Fatalf("revisions: %+v %+v", a, b)
	}
	if _, err = s.AssignTool(id, protocol.ToolAssignment{Scope: "projects", Projects: []string{"missing"}}, "owner", "test"); err == nil {
		t.Fatal("accepted unregistered project")
	}
	if _, err = s.AssignTool(id, protocol.ToolAssignment{Scope: "global"}, "agent", "test"); err == nil {
		t.Fatal("accepted agent actor")
	}
}

func TestToolPlanRejectsChangedObservation(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	item := protocol.InventoryItem{Kind: "skill", Name: "sample", Hash: "abc", State: "ok", Location: "/fixture"}
	if err = s.SetInventory("machine", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{item}}); err != nil {
		t.Fatal(err)
	}
	id := `["skill","sample","abc"]`
	plan, err := s.SaveToolPlan(id, protocol.ToolAssignment{Scope: "global"}, nil, "owner")
	if err != nil {
		t.Fatal(err)
	}
	item.Location = "/changed"
	if err = s.SetInventory("machine", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{item}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyToolPlan(plan.ID, "owner"); err == nil || !strings.Contains(err.Error(), "plan out of date") {
		t.Fatalf("expected stale plan: %v", err)
	}
	events, err := s.ToolHistory(id)
	if err != nil || len(events) != 2 || events[1].Result != "refused" {
		t.Fatalf("missing refused audit: %+v %v", events, err)
	}
}

func TestToolItemsShowDesiredAndPending(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	item := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok"}
	if err = s.SetInventory("node", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{item}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AssignTool(`["skill","demo","abc"]`, protocol.ToolAssignment{Scope: "global"}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	items, err := s.ToolsItems(protocol.ToolsFilter{}, func(string) bool { return true })
	if err != nil || len(items) != 1 || items[0].Desired == nil || items[0].Desired.Assignment.Scope != "global" || items[0].Applied != "pending" {
		t.Fatalf("desired: %+v %v", items, err)
	}
}

func TestToolPlanAndApplyAudit(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok"}
	if err = s.SetInventory("node", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	id := `["skill","demo","abc"]`
	targets := []protocol.ToolPlanTarget{{NodeID: "node", Harness: "codex", Destination: "/fixture", Action: "copy", State: "pending"}}
	p, err := s.SaveToolPlan(id, protocol.ToolAssignment{Scope: "global"}, targets, "cto")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyToolPlan(p.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	events, err := s.ToolHistory(id)
	if err != nil || len(events) != 2 || events[0].Actor != "cto" || events[1].Actor != "owner" || events[1].Revision != 1 || len(events[1].Targets) != 1 {
		t.Fatalf("history: %+v %v", events, err)
	}
}

func TestGatewayToolPlanRequiresOwnerSession(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	node, _, err := g.store.CreateNode("fixture", "linux")
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"item":"x","target":{"scope":"projects","projects":["demo"]},"operation":"repo_remove","project":"demo","repo_targets":[{"node_id":"node","checkout":"/repo"}],"actor":"owner"}`)
	if _, err = g.handle(context.Background(), &nodeConn{id: node}, "tools.plan", raw); err == nil || !strings.Contains(err.Error(), "owner authority") {
		t.Fatalf("gateway accepted unauthenticated owner: %v", err)
	}
	if _, err = g.handle(context.Background(), &nodeConn{id: node}, "tools.apply", []byte(`{"plan_id":"plan_missing","actor":"owner"}`)); err == nil || !strings.Contains(err.Error(), "owner authority") {
		t.Fatalf("gateway accepted unauthenticated apply: %v", err)
	}
}

func TestApplyReportsStaleWhenItemDisappears(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok"}
	if err = g.store.SetInventory("node", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	id := `["skill","demo","abc"]`
	p, err := g.planTool(context.Background(), protocol.ToolPlanRequest{Item: id, Target: protocol.ToolAssignment{Scope: "global"}}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err = g.store.SetInventory("node", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{}}); err != nil {
		t.Fatal(err)
	}
	if _, err = g.applyTool(context.Background(), p.ID, "owner"); err == nil || !strings.Contains(err.Error(), "plan out of date") {
		t.Fatalf("expected stale plan: %v", err)
	}
}

func TestGatewayToolHistoryRequiresOwnerSession(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	node, _, err := g.store.CreateNode("fixture", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.handle(context.Background(), &nodeConn{id: node}, "tools.history", []byte(`{"item":"x"}`)); err == nil {
		t.Fatal("gateway accepted unauthenticated history")
	}
}

func TestRejectedToolPlanIsAudited(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok"}
	if err = g.store.SetInventory("node", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	id := `["skill","demo","abc"]`
	if _, err = g.planTool(context.Background(), protocol.ToolPlanRequest{Item: id, Target: protocol.ToolAssignment{Scope: "invalid"}}, "owner"); err == nil {
		t.Fatal("invalid plan accepted")
	}
	history, err := g.store.ToolHistory(id)
	if err != nil || len(history) != 1 || history[0].Action != "plan" || history[0].Result != "refused" {
		t.Fatalf("history: %+v %v", history, err)
	}
}

func TestToolPlanReportsIncompleteScan(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok"}
	if err = g.store.SetInventory("node", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	if err = g.store.SetInventory("node", protocol.InventoryReport{State: "error"}); err != nil {
		t.Fatal(err)
	}
	plan, err := g.planTool(context.Background(), protocol.ToolPlanRequest{Item: `["skill","demo","abc"]`, Target: protocol.ToolAssignment{Scope: "uninstalled"}}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(plan)
	if !strings.Contains(string(b), "inventory scan incomplete") {
		t.Fatalf("incomplete scan not reported: %s", b)
	}
}

func TestPlanAuditRecordsBaseRevision(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok"}
	if err = s.SetInventory("node", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	id := `["skill","demo","abc"]`
	if _, err = s.AssignTool(id, protocol.ToolAssignment{Scope: "global"}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	plan, err := s.SaveToolPlan(id, protocol.ToolAssignment{Scope: "uninstalled"}, nil, "owner")
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.ToolHistory(id)
	if err != nil || plan.BaseRevision != 1 || len(events) != 1 || events[0].Revision != 1 {
		t.Fatalf("revision missing: %+v %+v %v", plan, events, err)
	}
}

func TestManagedToolRemainsListedWithoutLocations(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok"}
	if err = s.SetInventory("node", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	id := `["skill","demo","abc"]`
	if _, err = s.AssignTool(id, protocol.ToolAssignment{Scope: "uninstalled"}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetInventory("node", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{}}); err != nil {
		t.Fatal(err)
	}
	items, err := s.ToolsItems(protocol.ToolsFilter{}, func(string) bool { return false })
	if err != nil || len(items) != 1 || items[0].ID != id || items[0].Desired == nil {
		t.Fatalf("stored item missing: %+v %v", items, err)
	}
}

func TestToolPlanNamesStoredSourceCommit(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	node, _, err := g.store.CreateNode("fixture", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err = g.store.SetNodeHello(node, "fixture", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err = g.store.SetNodeHarnesses(node, []protocol.HarnessInfo{{Name: "codex"}}); err != nil {
		t.Fatal(err)
	}
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok"}
	if err = g.store.SetInventory(node, protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	id := `["skill","demo","abc"]`
	repo, err := g.skillRepository()
	if err != nil {
		t.Fatal(err)
	}
	v, err := repo.put(id, "abc", []byte("fixture"), node, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	p, err := g.planTool(context.Background(), protocol.ToolPlanRequest{Item: id, Target: protocol.ToolAssignment{Scope: "global"}}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range p.Targets {
		if target.Action == "copy" && strings.Contains(target.Source, v.Commit) && strings.Contains(target.Source, "skills.git") {
			return
		}
	}
	t.Fatalf("source commit missing: %+v", p.Targets)
}

func TestToolPlanIncludesUnseenEnrolledMachines(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	node, _, err := g.store.CreateNode("fixture", "linux")
	if err != nil {
		t.Fatal(err)
	}
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok"}
	if err = g.store.SetInventory(node, protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	p, err := g.planTool(context.Background(), protocol.ToolPlanRequest{Item: `["skill","demo","abc"]`, Target: protocol.ToolAssignment{Scope: "global"}}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range p.Targets {
		if target.NodeID == node && target.Conflict != "" {
			return
		}
	}
	t.Fatalf("missing unseen node: %+v", p.Targets)
}

func TestUninstallPlanShowsOfflineMachinesWithoutObservedCopies(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	node, _, err := g.store.CreateNode("offline", "linux")
	if err != nil {
		t.Fatal(err)
	}
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok"}
	if err = g.store.SetInventory("other", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	p, err := g.planTool(context.Background(), protocol.ToolPlanRequest{Item: `["skill","demo","abc"]`, Target: protocol.ToolAssignment{Scope: "uninstalled"}}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range p.Targets {
		if target.NodeID == node && target.State == "offline" {
			return
		}
	}
	t.Fatalf("offline machine missing: %+v", p.Targets)
}

func TestToolApplyRejectsCorruptStoredVersion(t *testing.T) {
	data := t.TempDir()
	g, err := New(Options{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	dir := t.TempDir()
	if err = os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("demo"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, err := inventory.SkillHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := inventory.PackSkill(dir)
	if err != nil {
		t.Fatal(err)
	}
	obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: hash, State: "ok"}
	if err = g.store.SetInventory("node", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
		t.Fatal(err)
	}
	idBytes, _ := json.Marshal([]string{"skill", "demo", hash})
	id := string(idBytes)
	repo, err := g.skillRepository()
	if err != nil {
		t.Fatal(err)
	}
	version, err := repo.put(id, hash, bundle, "node", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	p, err := g.planTool(context.Background(), protocol.ToolPlanRequest{Item: id, Target: protocol.ToolAssignment{Scope: "uninstalled"}}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	blob, err := repo.git(nil, "rev-parse", version.Commit+":"+skillPath(id))
	if err != nil {
		t.Fatal(err)
	}
	object := filepath.Join(data, "skills.git", "objects", blob[:2], blob[2:])
	if err = os.Chmod(object, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(object, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = g.applyTool(context.Background(), p.ID, "owner"); err == nil || !strings.Contains(err.Error(), "plan out of date") {
		t.Fatalf("corrupt version accepted: %v", err)
	}
}

func TestApplyRemovalRequiresImportedSkill(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		remaining, imported, wantError bool
	}{
		{name: "last copy", wantError: true},
		{name: "another assigned copy", remaining: true},
		{name: "imported", imported: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			obs := protocol.InventoryItem{Kind: "skill", Name: "demo", Hash: "abc", State: "ok", Scope: "user", Location: "/one"}
			if err = s.SetInventory("node", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{obs}}); err != nil {
				t.Fatal(err)
			}
			id := `["skill","demo","abc"]`
			assignment := protocol.ToolAssignment{Scope: "uninstalled"}
			if tc.remaining {
				if _, err = s.db.Exec(`INSERT INTO projects(node_id,path,name,added_at,checked_at) VALUES('other','/repo','demo',0,0)`); err != nil {
					t.Fatal(err)
				}
				other := obs
				other.Scope = "project"
				other.Project = "demo"
				other.ProjectRoot = "/repo"
				other.Location = "/repo/.agents/skills/demo"
				if err = s.SetInventory("other", protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{other}}); err != nil {
					t.Fatal(err)
				}
				assignment = protocol.ToolAssignment{Scope: "projects", Projects: []string{"demo"}}
			}
			if tc.imported {
				if _, err = s.db.Exec(`INSERT INTO skill_versions(item,hash,commit_id,node_id,location,imported_at) VALUES(?,?,?,?,?,?)`, id, "abc", "fixture", "node", "fixture", 0); err != nil {
					t.Fatal(err)
				}
			}
			p, err := s.SaveToolPlan(id, assignment, []protocol.ToolPlanTarget{{NodeID: "node", Source: "/one", Action: "remove"}}, "owner")
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.ApplyToolPlan(p.ID, "owner")
			if tc.wantError {
				if err == nil || err.Error() != "this is the last copy and it is not imported: import it first" {
					t.Fatalf("expected last-copy refusal: %v", err)
				}
			} else if err != nil {
				t.Fatalf("removal refused: %v", err)
			}
		})
	}
}
