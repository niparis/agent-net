package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A session report arrives on every activity change of every session. The
// standing choice for new projects only has work when a skill, a project or
// a seen mark changed, so a report with nothing new must not read all skills.
func TestSessionReportsSkipStandingChoicesWhenNothingChanged(t *testing.T) {
	g, n, id := toggleFixture(t)
	report := func(kind string) {
		t.Helper()
		var raw []byte
		if kind == "session.sync" {
			raw, _ = json.Marshal(protocol.SessionSync{Sessions: []protocol.SessionInfo{{Harness: "claude", NativeID: "s-1", Cwd: "/repo/one", Repo: protocol.RepoInfo{Root: "/repo/one", Worktree: "/repo/one", Project: "one"}}}})
		} else {
			raw, _ = json.Marshal(protocol.SessionUpsert{Session: protocol.SessionInfo{Harness: "claude", NativeID: "s-1", Cwd: "/repo/one", Repo: protocol.RepoInfo{Root: "/repo/one", Worktree: "/repo/one", Project: "one"}}})
		}
		if _, err := g.handle(context.Background(), &nodeConn{id: n}, kind, raw); err != nil {
			t.Fatal(err)
		}
		g.standingWG.Wait()
	}
	report("session.upsert")
	// The first report may still have marks to freeze. Once the choice has
	// settled, more reports change nothing.
	report("session.upsert")
	before := g.skillScans.Load()
	if before == 0 {
		t.Fatal("the standing choice never ran")
	}
	for i := 0; i < 5; i++ {
		report("session.upsert")
		report("session.sync")
	}
	if got := g.skillScans.Load(); got != before {
		t.Fatalf("%d more skill scans for reports with nothing new", got-before)
	}
	// A project that appears later is still seen by the next report.
	if _, err := g.store.db.Exec(`INSERT INTO projects(node_id,path,name,added_at,checked_at) VALUES(?,?,?,0,0)`, n, "/repo/three", "three"); err != nil {
		t.Fatal(err)
	}
	report("session.upsert")
	var seen int
	if err := g.store.db.QueryRow(`SELECT COUNT(*) FROM tool_project_seen WHERE item=? AND project='three'`, id).Scan(&seen); err != nil || seen != 1 {
		t.Fatalf("the new project was not seen: seen=%d err=%v", seen, err)
	}
	// And a new skill gets its settings frozen by the next report.
	if err := g.store.SetInventory(n, protocol.InventoryReport{State: "ok", ScannedAt: time.Now(), Items: []protocol.InventoryItem{
		{Kind: "skill", Name: "demo", Hash: "abc", State: "ok", Scope: "user", Location: "/skills/demo"},
		{Kind: "skill", Name: "second", Hash: "def", State: "ok", Scope: "user", Location: "/skills/second"}}}); err != nil {
		t.Fatal(err)
	}
	report("session.upsert")
	if missing, err := g.store.skillSettingsMissing(); err != nil || missing {
		t.Fatalf("a new skill kept no settings: missing=%v err=%v", missing, err)
	}
}
