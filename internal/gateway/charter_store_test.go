package gateway

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// oldCharterStore builds the file an earlier release left behind: the layout
// just before the charter tables merge, with values at all four levels in the
// three old tables. It returns the path.
func oldCharterStore(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway.db")
	old := migrateOnly(t, path, migrationVersion(t, oneCharterStoreMarker)-1)
	defer old.Close()
	for _, q := range []string{
		`INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES('demo','demo','',1,1,0)`,
		`INSERT INTO repos(id,display_name,created_at,updated_at,owner_agent_id) VALUES('demo','demo',1,1,'demo')`,
		`INSERT INTO charter_values VALUES('global','','rules','global taste',1,'owner')`,
		`INSERT INTO charter_values VALUES('global','','refuses','secrets',1,'owner')`,
		`INSERT INTO graph_charter VALUES('demo','refuses','production',2,'owner')`,
		`INSERT INTO graph_charter VALUES('demo','maturity','growing',2,'owner')`,
		`INSERT INTO agent_charter VALUES('demo','refuses','secrets',3,'owner')`,
		`INSERT INTO agent_charter VALUES('demo','accepts','fixes',3,'owner')`,
	} {
		if _, err := old.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return path
}

// restoreOldCharterTables puts the store back in the layout before the charter
// tables merged, so a test can replay a shipped migration step that reads
// graph_charter or agent_charter. Replaying the merge afterwards moves the rows
// forward again.
func restoreOldCharterTables(t testing.TB, s *Store) {
	t.Helper()
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS graph_charter (node TEXT NOT NULL, attr TEXT NOT NULL, value TEXT NOT NULL, updated_at INTEGER NOT NULL, updated_by TEXT NOT NULL, PRIMARY KEY(node, attr))`,
		`CREATE TABLE IF NOT EXISTS agent_charter (agent_id TEXT NOT NULL, attr TEXT NOT NULL, value TEXT NOT NULL, updated_at INTEGER NOT NULL, updated_by TEXT NOT NULL, PRIMARY KEY(agent_id, attr))`,
		`INSERT OR IGNORE INTO graph_charter SELECT scope,attr,value,updated_at,updated_by FROM charter_values WHERE level='repo'`,
		`INSERT OR IGNORE INTO agent_charter SELECT scope,attr,value,updated_at,updated_by FROM charter_values WHERE level='agent'`,
		`DELETE FROM charter_values WHERE level IN ('repo','agent')`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// ensureOldCharterTables puts empty graph_charter and agent_charter tables back
// on a merged store when a test rewinds to a step before the charter merge and
// did not call restoreOldCharterTables itself.
func ensureOldCharterTables(t testing.TB, db repoDB) {
	t.Helper()
	var merged bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='charter_values' AND sql LIKE '%''repo''%') AND NOT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='graph_charter')`).Scan(&merged); err != nil {
		t.Fatal(err)
	}
	if !merged {
		return
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS graph_charter (node TEXT NOT NULL, attr TEXT NOT NULL, value TEXT NOT NULL, updated_at INTEGER NOT NULL, updated_by TEXT NOT NULL, PRIMARY KEY(node, attr));
CREATE TABLE IF NOT EXISTS agent_charter (agent_id TEXT NOT NULL, attr TEXT NOT NULL, value TEXT NOT NULL, updated_at INTEGER NOT NULL, updated_by TEXT NOT NULL, PRIMARY KEY(agent_id, attr))`); err != nil {
		t.Fatal(err)
	}
}

// TestOneCharterStoreMigratesAllLevels shows the effective charter of an
// agent, a repo and a team does not change when the repo and agent values
// move into charter_values, and that the old tables are gone.
func TestOneCharterStoreMigratesAllLevels(t *testing.T) {
	path := oldCharterStore(t)
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, table := range []string{"agent_charter", "graph_charter"} {
		var n int
		if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name=?`, table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s still exists: %d %v", table, n, err)
		}
	}
	rows := map[string]string{}
	r, err := s.db.Query(`SELECT level||'/'||scope||'/'||attr, value FROM charter_values`)
	if err != nil {
		t.Fatal(err)
	}
	for r.Next() {
		var k, v string
		if err := r.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		rows[k] = v
	}
	r.Close()
	for k, want := range map[string]string{
		"global//rules": "global taste", "global//refuses": "secrets",
		"repo/demo/refuses": "production", "repo/demo/maturity": "growing",
		"agent/demo/refuses": "secrets", "agent/demo/accepts": "fixes",
	} {
		if rows[k] != want {
			t.Fatalf("charter_values %s = %q, want %q (all: %v)", k, rows[k], want, rows)
		}
	}
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO gateway_state(key,value,updated_at) VALUES('graph_ref_gate','1',0)`); err != nil {
		t.Fatal(err)
	}
	if err := s.loadGraphRefs(); err != nil {
		t.Fatal(err)
	}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha", Purpose: "Team purpose"})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &protocol.GraphRef{Kind: "repo", ID: "demo"}})
	if err := s.SetLevelCharter(protocol.CharterSetRequest{Level: "team", Node: "alpha", Attr: "refuses", Value: "delete backups"}, "owner", false); err != nil {
		t.Fatal(err)
	}
	// Same expectations as TestEffectiveCharterLevelsAndSources, which wrote the
	// values through the old tables.
	c, err := s.EffectiveCharter("demo", "demo", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Purpose != "Team purpose" || c.Rules != "global taste" || c.Accepts != "fixes" || c.Refuses != "secrets; delete backups; production" || c.Maturity != "growing" {
		t.Fatalf("effective %+v", c)
	}
	repoOnly, err := s.EffectiveCharter("demo", "", "")
	if err != nil || repoOnly.Refuses != "secrets; delete backups; production" || repoOnly.Accepts != "" {
		t.Fatalf("repo only %+v %v", repoOnly, err)
	}
	// A write goes to the merged table and its history keeps the old type names.
	if err := s.SetLevelCharter(protocol.CharterSetRequest{Level: "agent", Node: "demo", Attr: "accepts", Value: "bugs"}, "owner", false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLevelCharter(protocol.CharterSetRequest{Level: "repo", Node: "demo", Attr: "purpose", Value: "docs"}, "owner", false); err != nil {
		t.Fatal(err)
	}
	var types string
	if err := s.db.QueryRow(`SELECT group_concat(type) FROM (SELECT type FROM graph_history WHERE attr IN ('accepts','purpose') AND node='demo' ORDER BY id)`).Scan(&types); err != nil || !strings.Contains(types, "agent_charter") || !strings.Contains(types, "charter") {
		t.Fatalf("history types %q %v", types, err)
	}
	// Undo restores each value. Undo writes its own history row, so undo by id.
	ids, err := s.db.Query(`SELECT id FROM graph_history WHERE node='demo' AND attr IN ('accepts','purpose') ORDER BY id DESC`)
	if err != nil {
		t.Fatal(err)
	}
	var undo []int64
	for ids.Next() {
		var id int64
		if err := ids.Scan(&id); err != nil {
			t.Fatal(err)
		}
		undo = append(undo, id)
	}
	ids.Close()
	for _, id := range undo {
		if err := s.GraphUndo(id, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	c, err = s.EffectiveCharter("demo", "demo", "")
	if err != nil || c.Accepts != "fixes" {
		t.Fatalf("after undo %+v %v", c, err)
	}
}

// TestOneCharterStoreReplays shows the migration runs twice without error, as
// a recovered interrupted upgrade does.
func TestOneCharterStoreReplays(t *testing.T) {
	s, err := OpenStore(oldCharterStore(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, oneCharterStoreMarker)); err != nil {
		t.Fatal(err)
	}
	if err := replayMigrations(t, s); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM charter_values WHERE level IN ('repo','agent')`).Scan(&n); err != nil || n != 4+len(ctoDefaultCharter) {
		t.Fatalf("rows after replay: %d %v", n, err)
	}
}

// BenchmarkEffectiveCharter reads the effective charter of one agent in a
// repo in a team, and of a list of agents, with values at all four levels.
func BenchmarkEffectiveCharter(b *testing.B) {
	s := teamStore(b)
	owner := protocol.GraphRef{Kind: "agent", ID: "demo"}
	if err := s.WriteTeam(protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha", Purpose: "Team purpose", Owner: &owner}, "owner"); err != nil {
		b.Fatal(err)
	}
	if err := s.WriteTeam(protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &protocol.GraphRef{Kind: "repo", ID: "demo"}}, "owner"); err != nil {
		b.Fatal(err)
	}
	for _, q := range []protocol.CharterSetRequest{
		{Level: "global", Attr: "rules", Value: "global taste"},
		{Level: "team", Node: "alpha", Attr: "refuses", Value: "delete backups"},
		{Level: "repo", Node: "demo", Attr: "refuses", Value: "production"},
		{Level: "repo", Node: "demo", Attr: "maturity", Value: "growing"},
		{Level: "agent", Node: "demo", Attr: "refuses", Value: "secrets"},
		{Level: "agent", Node: "demo", Attr: "accepts", Value: "fixes"},
	} {
		if err := s.SetLevelCharter(q, "owner", false); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.EffectiveCharter("demo", "demo", ""); err != nil {
			b.Fatal(err)
		}
	}
}
