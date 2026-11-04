package gateway

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func standingFixture(t *testing.T) (*Gateway, string) {
	t.Helper()
	g, n, id := toggleFixture(t)
	if _, err := g.store.AssignTool(id, protocol.ToolAssignment{Scope: "projects", Projects: []string{"one"}, Machines: []string{n}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	if err := g.store.SetToolNewProjects(protocol.ToolNewProjectsRequest{Item: id, On: true}, "owner", false); err != nil {
		t.Fatal(err)
	}
	return g, n
}

// A failed standing-choice plan must be tried once per new project, not on
// every session report.
func TestStandingChoiceFailureTriedOnce(t *testing.T) {
	g, n := standingFixture(t)
	if _, err := g.store.db.Exec(`INSERT INTO projects(node_id,path,name,added_at,checked_at) VALUES(?,?,?,0,0)`, n, "/repo/three", "three"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	g.onStandingPlan = func(string, []string) { calls++ }
	// The skill is not imported, so the plan refuses.
	if err := g.addNewSkillProjects(context.Background()); err == nil {
		t.Fatal("expected the first run to report the failure")
	}
	for i := 0; i < 3; i++ {
		if err := g.addNewSkillProjects(context.Background()); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if calls != 1 {
		t.Fatalf("planner called %d times, want 1", calls)
	}
}

// session.upsert must not wait for a standing choice that is being applied.
func TestSessionUpsertDoesNotWaitForStandingChoice(t *testing.T) {
	g, n := standingFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	var once, relOnce sync.Once
	g.onStandingPlan = func(string, []string) {
		once.Do(func() { close(started) })
		<-release
	}
	t.Cleanup(func() { relOnce.Do(func() { close(release) }) })
	upsert := func(cwd, project string) {
		raw, _ := json.Marshal(protocol.SessionUpsert{Session: protocol.SessionInfo{Harness: "claude", NativeID: "s-" + project, Cwd: cwd, Repo: protocol.RepoInfo{Root: cwd, Worktree: cwd, Project: project}}})
		done := make(chan error, 1)
		go func() { _, err := g.handle(context.Background(), &nodeConn{id: n}, "session.upsert", raw); done <- err }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("session.upsert waited for the standing choice")
		}
	}
	upsert("/repo/three", "three")
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("standing choice did not start")
	}
	upsert("/repo/four", "four")
	relOnce.Do(func() { close(release) })
}

func TestStandingChoiceSkipsStaleAssignmentProject(t *testing.T) {
	g, n, id := toggleFixture(t)
	if _, err := g.store.AssignTool(id, protocol.ToolAssignment{Scope: "projects", Projects: []string{"one"}, Machines: []string{n}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	if err := g.store.SetToolNewProjects(protocol.ToolNewProjectsRequest{Item: id, On: true}, "owner", false); err != nil {
		t.Fatal(err)
	}
	states, err := g.store.ToolProjectStates()
	if err != nil || len(states) != 1 || !states[0].Projects["one"] {
		t.Fatalf("missing observed project: %+v, %v", states, err)
	}
	if _, err := g.store.db.Exec(`DELETE FROM projects WHERE name='one'`); err != nil {
		t.Fatal(err)
	}
	if _, err := g.store.db.Exec(`INSERT INTO projects(node_id,path,name,added_at,checked_at) VALUES(?,?,?,0,0)`, n, "/repo/three", "three"); err != nil {
		t.Fatal(err)
	}
	var planned []string
	g.onStandingPlan = func(_ string, projects []string) { planned = append(planned, projects...) }
	err = g.addNewSkillProjects(t.Context())
	if err == nil || err.Error() == "project is not known" {
		t.Fatalf("expected import failure after skipping stale project, got %v", err)
	}
	if len(planned) != 1 || planned[0] != "three" {
		t.Fatalf("planned %v", planned)
	}
}

func TestStandingChoiceOnlyCopiesNewProjects(t *testing.T) {
	g, first, _ := toggleFixture(t)
	id, bundle := toggleBundle(t, g, first, "# Demo\nFixture.\n")
	second, _, err := g.store.CreateNode("second", "linux")
	if err != nil {
		t.Fatal(err)
	}
	var hash string
	if err = g.store.db.QueryRow(`SELECT hash FROM tool_identities WHERE id=?`, id).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	for _, node := range []string{first, second} {
		items := []protocol.InventoryItem{
			{Kind: "skill", Name: "demo", Hash: hash, State: "ok", Scope: "user", Location: "/home/skills/demo"},
			{Kind: "skill", Name: "demo", Hash: hash, State: "ok", Scope: "project", Project: "one", ProjectRoot: "/repo/one", Location: "/repo/one/.claude/skills/demo"},
			{Kind: "skill", Name: "demo", Hash: hash, State: "ok", Scope: "project", Project: "two", ProjectRoot: "/repo/two", Location: "/repo/two/.claude/skills/demo"},
		}
		if err = g.store.SetInventory(node, protocol.InventoryReport{State: "ok", Items: items}); err != nil {
			t.Fatal(err)
		}
	}
	repo, err := g.skillRepository()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.put(id, hash, bundle, first, "/home/skills/demo"); err != nil {
		t.Fatal(err)
	}
	if _, err = g.store.AssignTool(id, protocol.ToolAssignment{Scope: "projects", Projects: []string{"one", "two"}, Machines: []string{first, second}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	if err = g.store.SetToolNewProjects(protocol.ToolNewProjectsRequest{Item: id, On: true}, "owner", false); err != nil {
		t.Fatal(err)
	}
	if _, err = g.store.db.Exec(`INSERT INTO projects(node_id,path,name,added_at,checked_at) VALUES(?,?,?,0,0)`, first, "/repo/three", "three"); err != nil {
		t.Fatal(err)
	}
	if err = g.addNewSkillProjects(t.Context()); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err = g.store.db.QueryRow(`SELECT plan FROM tool_plans WHERE item=? AND applied=1 ORDER BY rowid DESC LIMIT 1`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var plan protocol.ToolPlan
	if err = json.Unmarshal([]byte(raw), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.StandingProjects) != 1 || plan.StandingProjects[0] != "three" {
		t.Fatalf("missing standing restriction: %+v", plan)
	}
	if len(plan.Targets) == 0 {
		t.Fatal("missing new project copies")
	}
	for _, target := range plan.Targets {
		if target.Action != "copy" || target.Project != "three" || target.NodeID != first {
			t.Fatalf("unexpected target: %+v", target)
		}
	}
}
