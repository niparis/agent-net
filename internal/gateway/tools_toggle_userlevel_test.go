package gateway

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A user-level skill serves every project. Switching it off in one project
// must copy it into the other projects and remove the user-level copy, in the
// same plan the owner confirms.
func TestToolToggleUserLevelOffOneProjectConverts(t *testing.T) {
	g, n, _ := toggleFixture(t)
	id, bundle := toggleBundle(t, g, n, "# Demo\nFixture.\n")
	toggleFakeNode(t, g, n, bundle, nil)

	plan, err := g.toggleTool(t.Context(), protocol.ToolToggleRequest{Item: id, Project: "one", On: false}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Assignment.Scope != "projects" || strings.Join(plan.Assignment.Projects, ",") != "two" {
		t.Fatalf("assignment: %+v", plan.Assignment)
	}
	copied, removed := false, false
	for _, target := range plan.Targets {
		if target.Project == "two" && target.Action == "copy" && target.Conflict == "" {
			copied = true
		}
		if target.Project == "" && target.Action == "remove" && target.Source == "/skills/demo" && target.Conflict == "" {
			removed = true
		}
	}
	if !copied || !removed {
		t.Fatalf("conversion targets missing: copied=%v removed=%v %+v", copied, removed, plan.Targets)
	}
	if len(plan.Conflicts) > 0 {
		t.Fatalf("conversion has conflicts: %+v", plan.Conflicts)
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err = g.applyTool(ctx, plan.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	var scope, projects string
	err = g.store.db.QueryRow(`SELECT json_extract(assignment,'$.scope'), COALESCE(json_extract(assignment,'$.projects'),'') FROM tool_revisions WHERE item=? ORDER BY revision DESC LIMIT 1`, id).Scan(&scope, &projects)
	if err != nil || scope != "projects" || !strings.Contains(projects, "two") {
		t.Fatalf("revision %q %q: %v", scope, projects, err)
	}
}

// The last project off makes the skill uninstalled; the user-level copy must
// still be part of the plan.
func TestToolToggleUserLevelLastProjectGoesUninstalled(t *testing.T) {
	g, n, _ := toggleFixture(t)
	id, bundle := toggleBundle(t, g, n, "# Demo\nFixture.\n")
	toggleFakeNode(t, g, n, bundle, nil)
	if _, err := g.store.db.Exec(`DELETE FROM projects WHERE name='two'`); err != nil {
		t.Fatal(err)
	}
	plan, err := g.toggleTool(t.Context(), protocol.ToolToggleRequest{Item: id, Project: "one", On: false}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Assignment.Scope != "uninstalled" {
		t.Fatalf("assignment: %+v", plan.Assignment)
	}
	removed := false
	for _, target := range plan.Targets {
		if target.Action == "remove" && target.Project == "" && target.Source == "/skills/demo" {
			removed = true
		}
	}
	if !removed {
		t.Fatalf("user-level removal missing: %+v", plan.Targets)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err = g.applyTool(ctx, plan.ID, "owner"); err != nil {
		t.Fatal(err)
	}
}

// A user-level skill is already on everywhere. Switching it on in one project
// must not plan a redundant project copy.
func TestToolToggleUserLevelOnAlreadyOnIsNoOp(t *testing.T) {
	g, n, _ := toggleFixture(t)
	id, bundle := toggleBundle(t, g, n, "# Demo\nFixture.\n")
	toggleFakeNode(t, g, n, bundle, nil)
	plan, err := g.toggleTool(t.Context(), protocol.ToolToggleRequest{Item: id, Project: "one", On: true}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Assignment.Scope != "global" {
		t.Fatalf("assignment: %+v", plan.Assignment)
	}
	if len(plan.Targets) != 0 {
		t.Fatalf("redundant copy targets: %+v", plan.Targets)
	}
}

// Two content versions of the same name take the group path. The group keeps
// the assignment of the item the owner toggled, and its conversion targets
// apply together.
func TestToolToggleGroupConversionKeepsAssignment(t *testing.T) {
	g, a, _ := toggleFixture(t)
	aid, ab := toggleBundle(t, g, a, "# Demo\nVersion A.\n")
	b, _, err := g.store.CreateNode("second", "linux")
	if err != nil {
		t.Fatal(err)
	}
	bid, bb := toggleBundle(t, g, b, "# Demo\nVersion B.\n")
	toggleFakeNode(t, g, a, ab, nil)
	toggleFakeNode(t, g, b, bb, nil)

	plan, err := g.toggleTool(t.Context(), protocol.ToolToggleRequest{Item: aid, Project: "one", On: false}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Operation != "skill_group" || plan.Assignment.Scope != "projects" || strings.Join(plan.Assignment.Projects, ",") != "two" {
		t.Fatalf("group plan: %+v", plan)
	}
	if len(plan.Targets) == 0 {
		t.Fatalf("group conversion has no targets")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err = g.applyTool(ctx, plan.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{aid, bid} {
		var scope string
		if err = g.store.db.QueryRow(`SELECT json_extract(assignment,'$.scope') FROM tool_revisions WHERE item=? ORDER BY revision DESC LIMIT 1`, id).Scan(&scope); err != nil || scope != "projects" {
			t.Fatalf("revision %s: %q %v", id, scope, err)
		}
	}
}
