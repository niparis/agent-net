package gateway

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestOrgFoldMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	// Rewind to the schema right before orgFoldMigration so we can seed rows
	// in the old shape and verify the upgrade path.
	if _, err := s.db.Exec(`
		DELETE FROM schema_version WHERE version=(SELECT MAX(version) FROM schema_version);
		DROP TRIGGER IF EXISTS repo_live_lead_insert;
		DROP TRIGGER IF EXISTS repo_live_lead_update;
		DROP TRIGGER IF EXISTS repo_lead_delete;
		DROP TRIGGER IF EXISTS repo_lead_tombstone;
		DROP TRIGGER IF EXISTS repo_agent_project_insert;
		DROP TRIGGER IF EXISTS repo_agent_project_update;
		DROP TRIGGER IF EXISTS repo_agent_session_insert;
		DROP TRIGGER IF EXISTS repo_agent_session_update;
		DROP TRIGGER IF EXISTS settings_target_identity_repo_delete;
		DROP TRIGGER IF EXISTS session_identity_repo_update;
		DROP TABLE repos;
		CREATE TABLE repos (
			id TEXT PRIMARY KEY,
			display_name TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			owner_agent_id TEXT REFERENCES agents(id) ON DELETE SET NULL,
			archived INTEGER NOT NULL DEFAULT 0,
			held_back TEXT NOT NULL DEFAULT '',
			lead_agent_id TEXT
		);
		CREATE TABLE team_members (
			member_kind TEXT NOT NULL CHECK(member_kind IN ('agent','repo')),
			member_id TEXT NOT NULL,
			team_id TEXT NOT NULL REFERENCES teams(id),
			PRIMARY KEY(member_kind,member_id)
		);
		CREATE TABLE outcome_overrules (message_id TEXT PRIMARY KEY, reply_id TEXT NOT NULL);
		CREATE TABLE charter_draft_asks (
			level TEXT NOT NULL, scope TEXT NOT NULL, source_hash TEXT NOT NULL,
			asked_at INTEGER NOT NULL, session_id TEXT NOT NULL DEFAULT '',
			PRIMARY KEY(level,scope)
		);
		CREATE TABLE project_display_names (project TEXT PRIMARY KEY, name TEXT NOT NULL);
		INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES
			('a1','A1','',1,1,0),
			('a2','A2','',1,1,0),
			('lead','Lead','',1,1,0);
		INSERT INTO teams(id,name,purpose,owner_agent_id,active,created_at,updated_at) VALUES('t1','Team 1','','lead',1,1,1);
		INSERT INTO repos(id,display_name,created_at,updated_at,owner_agent_id,archived,held_back,lead_agent_id) VALUES
			('r_default','r_default',1,1,'a1',0,'',NULL),
			('r_cleared','r_cleared',1,1,'a1',0,'',''),
			('r_explicit','r_explicit',1,1,'a1',0,'','a2');
		INSERT INTO team_members(member_kind,member_id,team_id) VALUES('agent','a1','t1'),('repo','r_default','t1');
		INSERT INTO project_display_names(project,name) VALUES('r_default','Default Repo');
		INSERT INTO graph_edges(source,target,kind,mode,rule,source_kind,target_kind) VALUES
			('a1','lead','reports_to','','normal','agent','agent'),
			('/delivery/caller','r_default','accepts','','normal','special','repo'),
			('r_default','r_cleared','depends','','normal','repo','repo');
		INSERT INTO graph_outcomes(source,target,message_id,reply_id,outcome,reason,redirect_to,recommendation,at,resolution)
			VALUES('a1','a2','m_req','m_rep','reject','no','','',1,'overrule');
		INSERT INTO outcome_overrules(message_id,reply_id) VALUES('m_over','m_rep');
		INSERT INTO charter_draft_asks(level,scope,source_hash,asked_at,session_id) VALUES('repo','r_default','hash123',42,'s1');
	`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for _, dropped := range []string{"team_members", "outcome_overrules", "charter_draft_asks", "project_display_names"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, dropped).Scan(&n); err != nil || n != 0 {
			t.Fatalf("table %s still exists (count=%d, err=%v)", dropped, n, err)
		}
	}

	var hasOwner int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('repos') WHERE name='owner_agent_id'`).Scan(&hasOwner); err != nil || hasOwner != 0 {
		t.Fatalf("repos.owner_agent_id still exists (count=%d, err=%v)", hasOwner, err)
	}

	checkLead := func(repo, wantLead string, wantNull bool, wantDisplay, wantTeam string) {
		t.Helper()
		var lead sql.NullString
		var display, team string
		if err := s.db.QueryRow(`SELECT lead_agent_id, display_name, team_id FROM repos WHERE id=?`, repo).Scan(&lead, &display, &team); err != nil {
			t.Fatalf("repo %s: %v", repo, err)
		}
		if lead.Valid == wantNull || lead.String != wantLead || display != wantDisplay || team != wantTeam {
			t.Fatalf("repo %s: lead=%v (wantNull=%v %q) display=%q (want %q) team=%q (want %q)", repo, lead, wantNull, wantLead, display, wantDisplay, team, wantTeam)
		}
	}
	checkLead("r_default", "a1", false, "Default Repo", "t1")
	checkLead("r_cleared", "", true, "r_cleared", "")
	checkLead("r_explicit", "a2", false, "r_explicit", "")

	var agentTeam string
	if err := s.db.QueryRow(`SELECT team_id FROM agents WHERE id='a1'`).Scan(&agentTeam); err != nil || agentTeam != "t1" {
		t.Fatalf("agent a1 team_id=%q err=%v", agentTeam, err)
	}

	var reportsCount, otherEdges int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM graph_edges WHERE kind='reports_to'`).Scan(&reportsCount); err != nil || reportsCount != 0 {
		t.Fatalf("reports_to count=%d err=%v", reportsCount, err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM graph_edges WHERE kind IN ('accepts','depends')`).Scan(&otherEdges); err != nil || otherEdges != 2 {
		t.Fatalf("other edges count=%d err=%v", otherEdges, err)
	}

	var overruled int
	var overruleMsg string
	if err := s.db.QueryRow(`SELECT overruled, overrule_message_id FROM graph_outcomes WHERE reply_id='m_rep'`).Scan(&overruled, &overruleMsg); err != nil || overruled != 1 || overruleMsg != "m_over" {
		t.Fatalf("outcome overruled=%d msg=%q err=%v", overruled, overruleMsg, err)
	}

	var hash string
	var askedAt int64
	if err := s.db.QueryRow(`SELECT source_hash, asked_at FROM charter_drafts WHERE level='repo' AND scope='r_default' AND status='asked'`).Scan(&hash, &askedAt); err != nil || hash != "hash123" || askedAt != 42 {
		t.Fatalf("charter_drafts asked: hash=%q askedAt=%d err=%v", hash, askedAt, err)
	}

	// Fresh schema must match upgraded schema.
	fresh, _ := newTestStore(t)
	freshCols := schemaColumns(t, fresh)
	upCols := schemaColumns(t, s)
	if freshCols != upCols {
		t.Fatalf("fresh vs upgraded schema mismatch:\nfresh:\n%s\nupgraded:\n%s", freshCols, upCols)
	}
}

func schemaColumns(t *testing.T, s *Store) string {
	t.Helper()
	rows, err := s.db.Query(`SELECT m.name, p.name, p.type FROM sqlite_master m JOIN pragma_table_info(m.name) p WHERE m.type='table' AND m.name NOT LIKE 'sqlite_%' AND m.name NOT LIKE 'standing_orders_fts%' ORDER BY m.name, p.cid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var tbl, col, typ string
		if err := rows.Scan(&tbl, &col, &typ); err != nil {
			t.Fatal(err)
		}
		b.WriteString(tbl + "." + col + ":" + typ + "\n")
	}
	return b.String()
}

func TestComputedOrgTreeAndStaleLeadBug(t *testing.T) {
	s := teamStore(t)
	lead2Session := agentSession(t, s, "demo", "lead2")
	visitorSession := agentSession(t, s, "demo", "visitor")

	// Set up team + chief + merger to cover the live org shape.
	if err := s.GraphLifecycle("agent.create", protocol.GraphLifecycleRequest{Name: "chief-agent"}, "owner", false); err != nil {
		t.Fatal(err)
	}
	node, _, err := s.CreateNode("h2", "linux")
	if err != nil {
		t.Fatal(err)
	}
	chiefSess, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "chief-nat"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET agent_id='chief-agent' WHERE id=?`, chiefSess); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO owner_settings(key,value,updated_at) VALUES('chief_session',?,1)`, chiefSess); err != nil {
		t.Fatal(err)
	}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "t_eng", Name: "Eng", Owner: &protocol.GraphRef{Kind: "agent", ID: "other"}})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "t_eng", Member: &protocol.GraphRef{Kind: "repo", ID: "demo"}})

	parents, err := agentParentsQ(s.db)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"cto":         protocol.OrgRoot,
		"chief-agent": protocol.OrgRoot,
		"other":       "cto",
		"demo":        "other",
		"visitor":     "demo",
		"lead2":       "demo",
	}
	for k, v := range want {
		if parents[k] != v {
			t.Fatalf("parents[%q] = %q, want %q", k, parents[k], v)
		}
	}

	// Changing the repo lead immediately moves visitor's reports_to without
	// any stored reports_to edge (fixes the stale-edge bug).
	leadSession(t, s, protocol.GraphRef{Kind: "repo", ID: "demo"}, lead2Session)
	parents, err = agentParentsQ(s.db)
	if err != nil {
		t.Fatal(err)
	}
	if parents["visitor"] != "lead2" {
		t.Fatalf("after lead change, visitor reports_to=%q, want lead2", parents["visitor"])
	}
	g, err := s.GraphForRoot(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range g.Nodes {
		if n.Ref != nil && n.Ref.Kind == "agent" && n.Ref.ID == "visitor" {
			found = true
			if n.ReportsTo != "lead2" {
				t.Fatalf("GraphForRoot visitor ReportsTo=%q, want lead2", n.ReportsTo)
			}
		}
	}
	if !found {
		t.Fatal("visitor not found in GraphForRoot")
	}
	if reportsTo, _, err := s.sessionOrgLines(visitorSession); err != nil || !strings.HasPrefix(reportsTo, "lead2") {
		t.Fatalf("sessionOrgLines=%q err=%v, want prefix lead2", reportsTo, err)
	}
	if err := s.SetEdge(protocol.Edge{From: "visitor", To: "other", Kind: protocol.EdgeReportsTo}, "owner"); err == nil {
		t.Fatal("expected SetEdge with reports_to to be rejected")
	}
}
