package gateway

import (
	"bytes"
	"log"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func resetIdentityMigration(t *testing.T, s *Store) {
	t.Helper()
	var version int
	for i, m := range migrations {
		if strings.Contains(m, "project identity migration") {
			version = i + 1
			break
		}
	}
	if version == 0 {
		t.Fatal("identity migration missing")
	}
	if _, err := s.db.Exec(`DROP TABLE IF EXISTS project_display_names`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, version); err != nil {
		t.Fatal(err)
	}
}

func identityFixture(t *testing.T, s *Store, folder, charter string) {
	t.Helper()
	n, _, err := s.CreateNode(folder, "linux")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpsertSession(n, protocol.SessionInfo{Harness: "pi", NativeID: folder, Repo: protocol.RepoInfo{Root: "/src/" + folder, Project: "agent-net", Remote: "https://example.com/team/agent-net.git"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO graph_nodes VALUES(?, 'component', 1)`, folder); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO graph_charter VALUES(?, 'purpose', ?, 1, 'owner')`, folder, charter); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO graph_edges VALUES(?, 'other', 'depends', '', 'normal')`, folder); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO graph_node_settings VALUES(?, 'sandbox', 'workspace', 1, 'owner')`, folder); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO project_policies VALUES(?, ?, 'ask', 1)`, n, folder); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO projects(node_id,path,name,remote,added_at,checked_at) VALUES(?,?,?,'https://example.com/team/agent-net.git',1,1)`, n, "/src/"+folder, folder); err != nil {
		t.Fatal(err)
	}
	insertLearned(t, s, folder, "needs", "", "add", "tests", "owner")
	if _, err = s.db.Exec(`INSERT INTO graph_history(type,node,attr,old_value,new_value,at,actor) VALUES('charter',?,'needs','','tests',1,'learned')`, folder); err != nil {
		t.Fatal(err)
	}
}

func TestProjectIdentityMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	identityFixture(t, s, "a2a-ocs", "project purpose")
	resetIdentityMigration(t, s)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		s, err = OpenStore(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{
			`SELECT count(*) FROM graph_charter WHERE node='agent-net' AND value='project purpose'`,
			`SELECT count(*) FROM projects WHERE name='agent-net'`,
			`SELECT count(*) FROM sessions WHERE repo_project='agent-net'`,
			`SELECT count(*) FROM graph_edges WHERE source='agent-net' AND target='other'`,
			`SELECT count(*) FROM graph_node_settings WHERE node='agent-net' AND value='workspace'`,
			`SELECT count(*) FROM project_policies WHERE project='agent-net' AND policy='ask'`,
			`SELECT count(*) FROM learned_suggestions WHERE node='agent-net'`,
			`SELECT count(*) FROM graph_history WHERE node='agent-net' AND actor='learned'`,
		} {
			var n int
			if err = s.db.QueryRow(q).Scan(&n); err != nil || n != 1 {
				t.Errorf("pass %d: %s: count=%d err=%v", i, q, n, err)
			}
		}
		if i == 0 {
			resetIdentityMigration(t, s)
		}
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProjectIdentityMigrationMergesChartersAndLogs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	identityFixture(t, s, "a2a-ocs", "first charter")
	identityFixture(t, s, "other-folder", "second charter")
	resetIdentityMigration(t, s)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	g, err := New(Options{DataDir: dir, Logger: log.New(&logs, "", 0)})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	var value string
	if err = g.store.db.QueryRow(`SELECT value FROM graph_charter WHERE node='agent-net' AND attr='purpose'`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(value, "first charter") || !strings.Contains(value, "second charter") {
		t.Fatalf("merged charter %q", value)
	}
	if !strings.Contains(logs.String(), "project.identity_merge") {
		t.Fatalf("missing merge log: %s", logs.String())
	}
}

func TestProjectIdentityMigrationConflictDoesNotBlockOtherProjects(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(filepath.Join(dir, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	identityFixture(t, s, "a2a-ocs", "conflicting charter")
	identityFixture(t, s, "clean-folder", "clean charter")
	if _, err = s.db.Exec(`UPDATE sessions SET repo_project='clean-repo', repo_remote='https://example.com/clean-repo.git' WHERE repo_root='/src/clean-folder'`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE projects SET remote='https://example.com/clean-repo.git' WHERE name='clean-folder'`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO graph_node_settings VALUES('agent-net','sandbox','different',1,'owner')`); err != nil {
		t.Fatal(err)
	}
	resetIdentityMigration(t, s)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	g, err := New(Options{DataDir: dir, Logger: log.New(&logs, "", 0)})
	if err != nil {
		t.Fatalf("gateway did not open: %v", err)
	}
	defer g.Close()
	for _, q := range []string{
		`SELECT count(*) FROM graph_charter WHERE node='a2a-ocs' AND value='conflicting charter'`,
		`SELECT count(*) FROM graph_node_settings WHERE node='agent-net' AND value='different'`,
		`SELECT count(*) FROM graph_charter WHERE node='clean-repo' AND value='clean charter'`,
		`SELECT count(*) FROM projects WHERE name='clean-repo'`,
	} {
		var n int
		if err = g.store.db.QueryRow(q).Scan(&n); err != nil || n != 1 {
			t.Errorf("%s: count=%d err=%v", q, n, err)
		}
	}
	if got := logs.String(); !strings.Contains(got, "project.identity_conflict") || !strings.Contains(got, "a2a-ocs") || !strings.Contains(got, "agent-net") {
		t.Fatalf("missing conflict log: %s", got)
	}
}
