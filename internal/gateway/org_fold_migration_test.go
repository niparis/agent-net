package gateway

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// rewindBeforeOrgFold puts a store back to the shape before orgFoldMigration:
// repos with owner_agent_id, and the four side tables that migration folds.
func rewindBeforeOrgFold(t *testing.T, s *Store) {
	t.Helper()
	// repos keeps the columns later changes added (settings token, bar, merger),
	// so the old shape is made by taking the fold's team_id off and putting
	// owner_agent_id back.
	if _, err := s.db.Exec(`
		DELETE FROM schema_version WHERE version>=` + strconv.Itoa(migrationVersion(t, orgFoldMarker)) + `;
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
		DROP INDEX IF EXISTS repos_team;
		ALTER TABLE repos DROP COLUMN team_id;
		ALTER TABLE repos ADD COLUMN owner_agent_id TEXT REFERENCES agents(id) ON DELETE SET NULL;
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
	`); err != nil {
		t.Fatal(err)
	}
	restoreRepoSideTables(t, s.db)
}

func TestOrgFoldMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	rewindBeforeOrgFold(t, s)
	if _, err := s.db.Exec(`
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
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO gateway_state(key,value,updated_at) VALUES('chief_session',?,1)`, chiefSess); err != nil {
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
	// The Sessions tree places a session through its Agent's lead session
	// (decision 0193), so the visitor session leads its Agent here.
	if _, err := s.db.Exec(`UPDATE agents SET lead_session=? WHERE id='visitor'`, visitorSession); err != nil {
		t.Fatal(err)
	}
	visitorView, _, err := s.Agent(visitorSession)
	if err != nil {
		t.Fatal(err)
	}
	if reportsTo, _, err := s.sessionOrgLines(visitorView); err != nil || !strings.HasPrefix(reportsTo, "lead2") {
		t.Fatalf("sessionOrgLines=%q err=%v, want prefix lead2", reportsTo, err)
	}
	// An explicit move is not refused: it is stored and wins over the lead.
	if err := s.SetEdge(protocol.Edge{From: "visitor", To: "other", Kind: protocol.EdgeReportsTo}, "owner"); err != nil {
		t.Fatalf("explicit reports_to move: %v", err)
	}
	if parents, err = agentParentsQ(s.db); err != nil || parents["visitor"] != "other" {
		t.Fatalf("after an explicit move visitor reports to %q (%v), want other", parents["visitor"], err)
	}
}

// The tree every session sees does not change with the migration, apart from
// the Repo leads a Repo in no Team reports to (decision 0184): those 25 leads
// sit under the CTO. The fixture has the live shape: 51 agents, 44 repos, 4
// teams, 24 agent-to-agent and 2 agent-to-owner stored reports_to edges. After
// the migration the stored edges are gone and the computed parents are the
// ones spelled out in want.
func TestOrgFoldKeepsTheLiveTree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	rewindBeforeOrgFold(t, s)
	node, _, err := s.CreateNode("fixture", "linux")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"cto": protocol.OrgRoot, "chief": protocol.OrgRoot}
	// soloRepoLead holds the leads the old stored tree did not place: their
	// Repo is in no Team, so the new rule puts them under the CTO but the
	// fixture's 26 stored edges do not include them.
	soloRepoLead := map[string]bool{}
	var stmts []string
	add := func(format string, args ...any) { stmts = append(stmts, fmt.Sprintf(format, args...)) }
	agent := func(id, role string) {
		add(`INSERT OR IGNORE INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES('%s','%s','%s',1,1,0)`, id, id, role)
	}
	agent("cto", "")
	agent("chief", "")
	for i := 1; i <= 4; i++ {
		lead := fmt.Sprintf("tl%d", i)
		agent(lead, "")
		add(`INSERT INTO teams(id,name,purpose,owner_agent_id,active,created_at,updated_at) VALUES('t%d','Team %d','','%s',1,1,1)`, i, i, lead)
		want[lead] = "cto"
	}
	for i := 1; i <= 3; i++ {
		member := fmt.Sprintf("m%d", i)
		agent(member, "")
		add(`INSERT INTO team_members(member_kind,member_id,team_id) VALUES('agent','%s','t%d')`, member, i)
		want[member] = fmt.Sprintf("tl%d", i)
	}
	teamOfRepo := map[int]int{0: 1, 1: 1, 2: 1, 3: 2, 4: 2, 5: 3}
	for i := 0; i < 31; i++ {
		lead, repo := fmt.Sprintf("rl%02d", i), fmt.Sprintf("rp%02d", i)
		agent(lead, "")
		// Half of the repos follow their owner, half have an explicit lead.
		explicit := "NULL"
		if i%2 == 1 {
			explicit = "'" + lead + "'"
		}
		add(`INSERT INTO repos(id,display_name,created_at,updated_at,owner_agent_id,lead_agent_id) VALUES('%s','%s',1,1,'%s',%s)`, repo, repo, lead, explicit)
		if team, ok := teamOfRepo[i]; ok {
			add(`INSERT INTO team_members(member_kind,member_id,team_id) VALUES('repo','%s','t%d')`, repo, team)
			want[lead] = fmt.Sprintf("tl%d", team)
		} else {
			// A Repo lead whose Repo is in no Team reports to the CTO
			// (decision 0184): the solo Repo has no Team lead above its lead.
			want[lead] = "cto"
			soloRepoLead[lead] = true
		}
	}
	for i := 0; i < 13; i++ {
		add(`INSERT INTO repos(id,display_name,created_at,updated_at) VALUES('rx%02d','rx%02d',1,1)`, i, i)
	}
	for i := 0; i < 8; i++ {
		helper, lead := fmt.Sprintf("h%d", i), fmt.Sprintf("rl%02d", 10+i)
		agent(helper, "")
		add(`INSERT INTO sessions(id,node_id,harness,native_id,last_seen,repo_id,agent_id) VALUES('s_%s','%s','claude','%s',0,'rp%02d','%s')`, helper, node, helper, 10+i, helper)
		want[helper] = lead
	}
	for i := 20; i < 23; i++ {
		merger := fmt.Sprintf("rp%02d-merger", i)
		agent(merger, "merger")
		want[merger] = fmt.Sprintf("rl%02d", i)
	}
	add(`INSERT INTO sessions(id,node_id,harness,native_id,last_seen,agent_id) VALUES('s_chief','%s','claude','chief',0,'chief')`, node)
	add(`INSERT OR REPLACE INTO gateway_state(key,value,updated_at) VALUES('chief_session','s_chief',1)`)
	stored := make(map[string]string, len(want))
	for child, parent := range want {
		if !soloRepoLead[child] {
			stored[child] = parent
		}
	}
	for child, parent := range stored {
		kind := "agent"
		if parent == protocol.OrgRoot {
			kind = "special"
		}
		add(`INSERT INTO graph_edges(source,target,kind,mode,rule,source_kind,target_kind) VALUES('%s','%s','reports_to','','normal','agent','%s')`, child, parent, kind)
	}
	if err := execAllDB(s.db, stmts); err != nil {
		t.Fatal(err)
	}
	var agents, repos, teams, edges int
	if err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM agents), (SELECT COUNT(*) FROM repos), (SELECT COUNT(*) FROM teams), (SELECT COUNT(*) FROM graph_edges WHERE kind='reports_to')`).Scan(&agents, &repos, &teams, &edges); err != nil {
		t.Fatal(err)
	}
	if agents != 51 || repos != 44 || teams != 4 || edges != len(stored) {
		t.Fatalf("fixture has %d agents, %d repos, %d teams, %d edges; want 51, 44, 4, %d", agents, repos, teams, edges, len(stored))
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = OpenStore(path); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM graph_edges WHERE kind='reports_to'`).Scan(&edges); err != nil || edges != 0 {
		t.Fatalf("stored reports_to edges after the migration: %d %v", edges, err)
	}
	got, err := agentParentsQ(s.db)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("computed %d parents, want %d: %v", len(got), len(want), got)
	}
	for child, parent := range want {
		if got[child] != parent {
			t.Errorf("%s reports to %q, want %q", child, got[child], parent)
		}
	}
}

func execAllDB(db repoDB, stmts []string) error {
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	return nil
}
