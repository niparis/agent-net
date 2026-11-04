package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestToolToggleVersionsAcrossMachines(t *testing.T) {
	g, a, _ := toggleFixture(t)
	aid, ab := toggleBundle(t, g, a, "# Demo\nVersion A.\n")
	b, _, err := g.store.CreateNode("second", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	bid, bb := toggleBundle(t, g, b, "# Demo\nVersion B.\n")
	for _, node := range []string{a, b} {
		reports, e := g.store.Inventories()
		if e != nil {
			t.Fatal(e)
		}
		for _, report := range reports {
			if report.NodeID != node {
				continue
			}
			items := append([]protocol.InventoryItem(nil), report.Items...)
			copy := items[0]
			copy.Scope, copy.Project, copy.ProjectRoot, copy.Location = "project", "one", "/repo/one", "/repo/one/.claude/skills/demo"
			items = append(items, copy)
			if e = g.store.SetInventory(node, protocol.InventoryReport{State: "ok", Items: items}); e != nil {
				t.Fatal(e)
			}
		}
	}
	toggleFakeNode(t, g, a, ab, nil)
	toggleFakeNode(t, g, b, bb, nil)
	p, err := g.toggleTool(t.Context(), protocol.ToolToggleRequest{Item: bid, Project: "one", On: false}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	removed := map[string]bool{}
	for _, target := range p.Targets {
		if target.Action == "remove" {
			removed[target.NodeID] = true
		}
	}
	if !removed[a] || !removed[b] {
		t.Fatalf("missing version removal: %+v", p.Targets)
	}
	res, err := g.applyTool(t.Context(), p.ID, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Targets) != len(p.Targets) {
		t.Fatalf("apply targets: %+v", res)
	}
	for _, id := range []string{aid, bid} {
		var revision int
		if err = g.store.db.QueryRow(`SELECT MAX(revision) FROM tool_revisions WHERE item=?`, id).Scan(&revision); err != nil || revision != 1 {
			t.Fatalf("revision %s: %d %v", id, revision, err)
		}
		_, err = g.store.db.Exec(`INSERT INTO tool_results(item,revision,target_index,node_id,state,reason) SELECT ?,1,CAST(t.key AS INTEGER),json_extract(t.value,'$.node_id'),'applied','' FROM tool_plans p,json_each(p.plan,'$.targets') t WHERE p.item=? AND p.applied=1 AND json_extract(p.plan,'$.operation')!='skill_group'`, id, id)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, node := range []string{a, b} {
		feed, e := g.store.ToolTargets(node)
		if e != nil || len(feed) == 0 {
			t.Fatalf("apply feed %s: %+v %v", node, feed, e)
		}
	}
	_, err = g.undoTool(t.Context(), p.ID, "owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range []string{a, b} {
		feed, e := g.store.ToolTargets(node)
		restored := false
		for _, target := range feed {
			restored = restored || target.Target.Action == "undo_remove"
		}
		if e != nil || !restored {
			t.Fatalf("undo feed %s: %+v %v", node, feed, e)
		}
	}
	for _, id := range []string{aid, bid} {
		var scope string
		if err = g.store.db.QueryRow(`SELECT json_extract(assignment,'$.scope') FROM tool_revisions WHERE item=? ORDER BY revision DESC LIMIT 1`, id).Scan(&scope); err != nil || scope != "global" {
			t.Fatalf("undo %s: %s %v", id, scope, err)
		}
	}
}

func TestToolSkillCopyPrefersCheckoutThenMachineThenNewest(t *testing.T) {
	g, a, _ := toggleFixture(t)
	aid, ab := toggleBundle(t, g, a, "# Demo\nUser copy.\n")
	b, _, err := g.store.CreateNode("second", "linux")
	if err != nil {
		t.Fatal(err)
	}
	bid, bb := toggleBundle(t, g, b, "# Demo\nNewer copy.\n")
	toggleFakeNode(t, g, a, ab, nil)
	toggleFakeNode(t, g, b, bb, nil)
	if err = g.ensureSkillImported(t.Context(), aid); err != nil {
		t.Fatal(err)
	}
	if err = g.ensureSkillImported(t.Context(), bid); err != nil {
		t.Fatal(err)
	}
	if !g.skillCopyIdentity(aid, a, "one") || !g.skillCopyIdentity(bid, "absent", "one") {
		t.Fatal("machine copy or newest fallback lost")
	}
	var hash string
	_ = g.store.db.QueryRow(`SELECT hash FROM tool_identities WHERE id=?`, bid).Scan(&hash)
	var ahash string
	_ = g.store.db.QueryRow(`SELECT hash FROM tool_identities WHERE id=?`, aid).Scan(&ahash)
	err = g.store.SetInventory(a, protocol.InventoryReport{State: "ok", Items: []protocol.InventoryItem{
		{Kind: "skill", Name: "demo", Hash: ahash, Scope: "user", State: "ok", Location: "/skills/demo"},
		{Kind: "skill", Name: "demo", Hash: hash, Scope: "project", Project: "one", ProjectRoot: "/repo/one", State: "ok", Location: "/repo/one/.agents/skills/demo"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !g.skillCopyIdentity(bid, a, "one") || !g.skillCopyIdentity(aid, a, "two") {
		t.Fatal("checkout must take precedence over user copy")
	}
}

func TestToolToggleGroupStaleMemberRejectsAll(t *testing.T) {
	g, a, _ := toggleFixture(t)
	aid, ab := toggleBundle(t, g, a, "# Demo\nFirst.\n")
	b, _, err := g.store.CreateNode("second", "linux")
	if err != nil {
		t.Fatal(err)
	}
	bid, bb := toggleBundle(t, g, b, "# Demo\nSecond.\n")
	toggleFakeNode(t, g, a, ab, nil)
	toggleFakeNode(t, g, b, bb, nil)
	p, err := g.toggleTool(t.Context(), protocol.ToolToggleRequest{Item: bid, Project: "one", On: false}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	_, err = g.store.AssignTool(aid, protocol.ToolAssignment{Scope: "global"}, "owner", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.applyTool(t.Context(), p.ID, "owner"); err == nil {
		t.Fatal("stale group applied")
	}
	var count int
	err = g.store.db.QueryRow(`SELECT COUNT(*) FROM tool_revisions WHERE item=?`, bid).Scan(&count)
	if err != nil || count != 0 {
		t.Fatalf("partial apply: %d %v", count, err)
	}
}
